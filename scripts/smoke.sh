#!/usr/bin/env bash
# Smoke-прогон heavytech: папочный layout -> старт -> проверка обоих фидов,
# стриминга (audio/video + Range), API, сайта (markdown-описание, YouTube,
# единый счётчик), бикона и рескана.
# Запуск: ./scripts/smoke.sh  (требует go и python3)
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
POD="$FIX/podcast"
mkdir -p "$POD"
if [[ ! -f test-fixtures/silent.mp3 ]]; then
  ffmpeg -y -loglevel error -f lavfi -i anullsrc=r=44100:cl=mono -t 3 -b:a 32k test-fixtures/silent.mp3
fi
cat > "$POD/podcast.yaml" <<'YAML'
title: Heavy Tech
description: Смок-подкаст smoke.sh
author: Сергей
email: sergey@example.com
category: Technology
explicit: false
YAML

# Выпуск 1: только аудио, длительность из meta, markdown-описание в файле.
mkdir -p "$POD/1"
cp test-fixtures/silent.mp3 "$POD/1/zapis.mp3"
cat > "$POD/1/episode.yaml" <<'YAML'
title: Первый выпуск
published_at: 2026-10-01
audio:
  file: zapis.mp3
  duration: 1872
YAML
cat > "$POD/1/description.md" <<'MD'
**Первый** выпуск и [гость](https://example.com)

- вступление
MD

# Выпуск 2: аудио + видео (содержимое синтетическое — сервис его не парсит).
mkdir -p "$POD/2"
cp test-fixtures/silent.mp3 "$POD/2/zapis.mp3"
printf 'fake-video-bytes' > "$POD/2/efir.mp4"
cat > "$POD/2/episode.yaml" <<'YAML'
title: Vtoroy
published_at: 2026-10-15
youtube: https://youtu.be/smoke
audio:
  file: zapis.mp3
video:
  file: efir.mp4
  resolution: 1920x1080
YAML

echo "==> старт на :$PORT"
"$BIN" -addr ":$PORT" -podcast-dir "$POD" -db "$FIX/db.sqlite" -base-url "$BASE" &
SRV_PID=$!
for _ in $(seq 1 50); do
  curl -sf "$BASE/api/episodes" -o /dev/null 2>/dev/null && break
  sleep 0.2
done

fail() { echo "FAIL: $1" >&2; exit 1; }

echo "==> /  (лендинг)"
HOME_HTML=$(curl -sf "$BASE/") || fail "GET /"
grep -q "Первый выпуск" <<<"$HOME_HTML" || fail "на лендинге нет последних выпусков"
grep -q 'data-episode="1"' <<<"$HOME_HTML" || fail "на лендинге нет кнопки воспроизведения эпизода 1"
grep -q '/feed-video.xml' <<<"$HOME_HTML" || fail "на лендинге нет ссылки на видео-фид"
grep -q 'href="/episodes"' <<<"$HOME_HTML" || fail "на лендинге нет ссылки на архив"
grep -q 'js-copy-rss' <<<"$HOME_HTML" || fail "на лендинге нет копирования RSS"

echo "==> /episodes (архив)"
ARCH_HTML=$(curl -sf "$BASE/episodes") || fail "GET /episodes"
grep -q 'href="'"$BASE"'/episodes/1"' <<<"$ARCH_HTML" || fail "в архиве нет ссылки на выпуск 1"
grep -q 'data-year="2026"' <<<"$ARCH_HTML" || fail "в архиве нет данных для фильтра по году"
grep -q 'id="q"' <<<"$ARCH_HTML" || fail "в архиве нет поиска"

echo "==> /episodes/1 (страница выпуска)"
EP_HTML=$(curl -sf "$BASE/episodes/1") || fail "GET /episodes/1"
grep -q '<strong>Первый</strong>' <<<"$EP_HTML" || fail "markdown-описание не отрендерено на странице выпуска"
grep -q 'href="https://example.com"' <<<"$EP_HTML" || fail "на странице выпуска нет ссылки из markdown-описания"
grep -q 'href="'"$BASE"'/audio/1/001-pervyy-vypusk.mp3"' <<<"$EP_HTML" || fail "на странице выпуска нет именованной ссылки на аудио"
grep -q 'download>Скачать' <<<"$EP_HTML" || fail "у ссылки скачивания нет атрибута download"
grep -q 'id="inline-player"' <<<"$EP_HTML" || fail "на странице выпуска нет плеера"
grep -q 'class="seek js-seek"' <<<"$EP_HTML" || fail "нет доступной полосы перемотки"
grep -q 'Прослушиваний и просмотров: 0' <<<"$EP_HTML" && fail "нулевой счётчик выводится на сайте"

echo "==> /episodes/2 (YouTube и видео)"
EP2_HTML=$(curl -sf "$BASE/episodes/2") || fail "GET /episodes/2"
grep -q 'href="'"$BASE"'/video/2/002-vtoroy.mp4"' <<<"$EP2_HTML" || fail "нет именованной ссылки на видео"
grep -q 'href="https://youtu.be/smoke" target="_blank" rel="noopener noreferrer"' <<<"$EP2_HTML" \
  || fail "на странице нет ссылки «Смотреть на YouTube»"

echo "==> /sitemap.xml и /robots.txt"
SITEMAP=$(curl -sf "$BASE/sitemap.xml") || fail "GET /sitemap.xml"
grep -q "<loc>$BASE/episodes/1</loc>" <<<"$SITEMAP" || fail "в карте сайта нет выпуска 1"
grep -q "<loc>$BASE/episodes</loc>" <<<"$SITEMAP" || fail "в карте сайта нет архива"
ROBOTS=$(curl -sf "$BASE/robots.txt") || fail "GET /robots.txt"
grep -q "Sitemap: $BASE/sitemap.xml" <<<"$ROBOTS" || fail "robots.txt не указывает карту сайта"

echo "==> /feed.xml (аудио-фид)"
curl -sf "$BASE/feed.xml" > "$FIX/feed.xml" || fail "GET /feed.xml"
python3 -c 'import sys, xml.dom.minidom; xml.dom.minidom.parse(sys.argv[1])' "$FIX/feed.xml" \
  || fail "feed.xml не является валидным XML"
grep -q "itunes:duration>1872<" "$FIX/feed.xml" || fail "в аудио-фиде нет duration из meta"
grep -q "guid isPermaLink=\"true\">$BASE/audio/1<" "$FIX/feed.xml" || fail "guid аудио-фида не /audio/1"
grep -q "enclosure url=\"$BASE/audio/1/001-pervyy-vypusk.mp3\"" "$FIX/feed.xml" || fail "enclosure аудио-фида не именованный URL"
grep -q "itunes:episode>1<" "$FIX/feed.xml" || fail "в аудио-фиде нет itunes:episode=1"
python3 - "$FIX/feed.xml" <<'PY' || fail "в аудио-фиде description не HTML из markdown"
import sys, xml.etree.ElementTree as ET
items = ET.parse(sys.argv[1]).getroot().findall("./channel/item")
descs = [(i.findtext("description") or "") for i in items]
assert any("<strong>Первый</strong>" in d and "<a href=\"https://example.com\">" in d for d in descs), descs
assert not any("**Первый**" in d for d in descs), descs
PY
[[ "$(grep -c '<item>' "$FIX/feed.xml")" == "2" ]] || fail "в аудио-фиде не 2 выпуска"

echo "==> /feed-video.xml (видео-фид)"
curl -sf "$BASE/feed-video.xml" > "$FIX/feed-video.xml" || fail "GET /feed-video.xml"
python3 -c 'import sys, xml.dom.minidom; xml.dom.minidom.parse(sys.argv[1])' "$FIX/feed-video.xml" \
  || fail "feed-video.xml не является валидным XML"
[[ "$(grep -c '<item>' "$FIX/feed-video.xml")" == "1" ]] || fail "в видео-фиде не 1 выпуск"
grep -q "guid isPermaLink=\"true\">$BASE/video/2<" "$FIX/feed-video.xml" || fail "guid видео-фида не /video/2"
grep -q "enclosure url=\"$BASE/video/2/002-vtoroy.mp4\"" "$FIX/feed-video.xml" || fail "enclosure видео-фида не именованный URL"
grep -q 'type="video/mp4"' "$FIX/feed-video.xml" || fail "в видео-фиде нет MIME video/mp4"
grep -q "/audio/" "$FIX/feed-video.xml" && fail "в видео-фиде оказался аудио-guid"

echo "==> /api/episodes (JSON)"
curl -sf "$BASE/api/episodes" | python3 -c '
import json, sys
eps = json.load(sys.stdin)
assert [e["number"] for e in eps] == [2, 1], eps
e1, e2 = eps[1], eps[0]
assert e1["title"] == "Первый выпуск" and e1["published_at"] == "2026-10-01", e1
assert [m["kind"] for m in e1["media"]] == ["audio"], e1
assert e1["media"][0]["url"] == "'"$BASE"'/audio/1/001-pervyy-vypusk.mp3", e1
assert e1["media"][0]["duration"] == 1872, e1
assert e1["youtube"] == "", e1
assert e2["youtube"] == "https://youtu.be/smoke", e2
assert e1["description"] == "**Первый** выпуск и [гость](https://example.com)\n\n- вступление", e1
assert [m["kind"] for m in e2["media"]] == ["audio", "video"], e2
assert e2["media"][1]["url"] == "'"$BASE"'/video/2/002-vtoroy.mp4", e2
assert e1["downloads"] == {"audio": 0, "video": 0, "total": 0} or e1["downloads"]["total"] >= 1, e1
' || fail "JSON API неожиданный"

echo "==> Range-запросы: audio и video"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Range: bytes=0-99" "$BASE/audio/1")
[[ "$CODE" == "206" ]] || fail "audio Range: код $CODE, хотели 206"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Range: bytes=0-99" "$BASE/audio/1/001-pervyy-vypusk.mp3")
[[ "$CODE" == "206" ]] || fail "audio Range по именованному URL: код $CODE, хотели 206"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/audio/1")
[[ "$CODE" == "200" ]] || fail "номерной audio URL: код $CODE, хотели 200"
CT=$(curl -s -o /dev/null -w "%{content_type}" "$BASE/video/2")
[[ "$CT" == "video/mp4" ]] || fail "video Content-Type: $CT"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Range: bytes=0-9" "$BASE/video/2")
[[ "$CODE" == "206" ]] || fail "video Range: код $CODE, хотели 206"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Range: bytes=0-9" "$BASE/video/2/002-vtoroy.mp4")
[[ "$CODE" == "206" ]] || fail "video Range по именованному URL: код $CODE, хотели 206"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/video/2")
[[ "$CODE" == "200" ]] || fail "номерной video URL: код $CODE, хотели 200"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/video/1")
[[ "$CODE" == "404" ]] || fail "видео у эпизода без видео: код $CODE, хотели 404"

echo "==> play-бикон по номеру"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" \
  -d '{"episode":1}' "$BASE/track/play")
[[ "$CODE" == "204" ]] || fail "track/play: код $CODE, хотели 204"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" \
  -d '{"slug":"pervyy-vypusk"}' "$BASE/track/play")
[[ "$CODE" == "400" ]] || fail "бикон со slug должен отвергаться 400, код $CODE"

echo "==> рескан: новый эпизод виден, повтор идемпотентен"
mkdir -p "$POD/3"
cp test-fixtures/silent.mp3 "$POD/3/zapis.mp3"
cat > "$POD/3/episode.yaml" <<'YAML'
title: Tretiy
published_at: 2026-10-20
audio:
  file: zapis.mp3
YAML
RES=$(curl -sf -X POST "$BASE/admin/rescan")
grep -q '"added":1' <<<"$RES" || fail "рескан не увидел новый эпизод: $RES"
RES2=$(curl -sf -X POST "$BASE/admin/rescan")
grep -q '"added":0,"updated":0,"removed":0' <<<"$RES2" || fail "повторный рескан не идемпотентен: $RES2"
curl -sf "$BASE/api/episodes" | python3 -c '
import json, sys
eps = json.load(sys.stdin)
assert [e["number"] for e in eps] == [3, 2, 1], eps
assert eps[2]["plays"] == 1, eps
assert eps[0]["downloads"]["total"] == 0, eps
' || fail "после рескана API неожиданный"
python3 - "$BASE" <<'PY' || fail "единый счётчик на сайте не равен скачиваниям + прослушиваниям"
import json, re, sys, urllib.request
base = sys.argv[1]
html = urllib.request.urlopen(base + "/episodes/1").read().decode()
count = re.search(r'Прослушиваний и просмотров: (\d+)', html)
assert count, "нет счётчика на странице эпизода 1"
eps = json.load(urllib.request.urlopen(base + "/api/episodes"))
e1 = next(e for e in eps if e["number"] == 1)
want = e1["downloads"]["total"] + e1["plays"] + e1["youtube_views"]
assert int(count.group(1)) == want, (count.group(1), want)
assert e1["plays"] == 1, e1
PY

echo
echo "SMOKE OK: оба фида, сайт, API, audio/video Range, бикон по номеру, рескан — всё работает."
