#!/usr/bin/env bash
# Smoke-прогон heavytech: фикстурная папка -> старт -> рескан -> проверка эндпоинтов.
# Запуск: ./scripts/smoke.sh  (требует go и python3; ffmpeg только для генерации фикстуры)
set -euo pipefail
cd "$(dirname "$0")/.."

ROOT="$(pwd)"
FIX="$(mktemp -d /tmp/heavytech-smoke.XXXXXX)"
PORT="${SMOKE_PORT:-18150}"
BASE="http://localhost:${PORT}"
BIN="$FIX/heavytech"

cleanup() { [[ -n "${SRV_PID:-}" ]] && kill "$SRV_PID" 2>/dev/null || true; rm -rf "$FIX"; }
trap cleanup EXIT

echo "==> сборка"
go build -o "$BIN" .

echo "==> фикстура: $FIX/podcast"
mkdir -p "$FIX/podcast"
if [[ ! -f test-fixtures/silent.mp3 ]]; then
  ffmpeg -y -loglevel error -f lavfi -i anullsrc=r=44100:cl=mono -t 3 -b:a 32k test-fixtures/silent.mp3
fi
cp test-fixtures/silent.mp3 "$FIX/podcast/2026-10-01-pervyy-vypusk.mp3"
cat > "$FIX/podcast/podcast.yaml" <<'YAML'
title: Heavy Tech
description: Смок-подкаст smoke.sh
author: Сергей
email: sergey@example.com
category: Technology
explicit: false
YAML
cat > "$FIX/podcast/pervyy-vypusk.yaml" <<'YAML'
title: Первый выпуск
description: Привет, это первый выпуск
duration: 1872
YAML

echo "==> старт на :$PORT"
"$BIN" -addr ":$PORT" -podcast-dir "$FIX/podcast" -db "$FIX/db.sqlite" -base-url "$BASE" &
SRV_PID=$!
for i in $(seq 1 30); do
  curl -sf "$BASE/api/episodes" -o /dev/null 2>/dev/null && break
  sleep 0.2
done

fail() { echo "FAIL: $1" >&2; exit 1; }

echo "==> /  (сайт)"
HOME_HTML=$(curl -sf "$BASE/") || fail "GET /"
grep -q "Первый выпуск" <<<"$HOME_HTML" || fail "на сайте нет эпизода со sidecar-заголовком"
grep -q "data-slug=\"pervyy-vypusk\"" <<<"$HOME_HTML" || fail "на сайте нет плеера"

echo "==> /feed.xml (XML валиден)"
curl -sf "$BASE/feed.xml" > "$FIX/feed.xml" || fail "GET /feed.xml"
python3 -c 'import sys, xml.dom.minidom; xml.dom.minidom.parse(sys.argv[1])' "$FIX/feed.xml" \
  || fail "feed.xml не является валидным XML"
grep -q "itunes:duration>1872<" "$FIX/feed.xml" || fail "в фиде нет длительности из sidecar"

echo "==> /api/episodes (JSON)"
curl -sf "$BASE/api/episodes" | python3 -c '
import json, sys
eps = json.load(sys.stdin)
assert len(eps) == 1, eps
e = eps[0]
assert e["slug"] == "pervyy-vypusk" and e["duration_seconds"] == 1872, e
assert e["audio_url"] == "'"$BASE"'/audio/pervyy-vypusk", e
' || fail "JSON API неожиданный"

echo "==> Range-запрос"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Range: bytes=0-99" "$BASE/audio/pervyy-vypusk")
[[ "$CODE" == "206" ]] || fail "Range-запрос: код $CODE, хотели 206"

echo "==> play-бикон + согласованность счётчиков"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" \
  -d '{"slug":"pervyy-vypusk"}' "$BASE/track/play")
[[ "$CODE" == "204" ]] || fail "track/play: код $CODE, хотели 204"

echo "==> рескан: новый эпизод виден, повтор идемпотентен"
cp test-fixtures/silent.mp3 "$FIX/podcast/2026-10-15-vtoroy.mp3"
RES=$(curl -sf -X POST "$BASE/admin/rescan")
grep -q '"added":1' <<<"$RES" || fail "рескан не увидел новый файл: $RES"
RES2=$(curl -sf -X POST "$BASE/admin/rescan")
grep -q '"added":0,"updated":0,"removed":0' <<<"$RES2" || fail "повторный рескан не идемпотентен: $RES2"
curl -sf "$BASE/api/episodes" | python3 -c '
import json, sys
eps = json.load(sys.stdin)
assert [e["slug"] for e in eps] == ["vtoroy", "pervyy-vypusk"], eps
assert eps[0]["downloads"] == 0 and eps[1]["plays"] == 1, eps
' || fail "счётчики после рескана неожиданны"
grep -q "Прослушиваний: 1" <(curl -sf "$BASE/") || fail "на сайте нет прослушиваний"

echo
echo "SMOKE OK: сайт, фид, API, Range, бикон, рескан — всё работает."
