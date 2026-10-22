#!/usr/bin/env bash
# Одноразовая миграция плоского layout (YYYY-MM-DD-slug.m4a) на папки-номера
# с обязательным episode.yaml (design D7, change episode-meta-layout).
#
# Что делает:
#   1. проверяет, что контейнер остановлен (и предупреждает, если он жив);
#   2. уводит data/heavytech.db в sidecar-имя с датой (fresh start базы);
#   3. сворачивает каждый плоский медиафайл в папку-номер по хронологии даты
#      (1 = старейший, при равных датах — по имени файла);
#   4. генерирует episode.yaml (title из слага прежним алгоритмом, published_at
#      из имени, duration из ffprobe/старого sidecar — если доступны);
#   5. переносит старые sidecar-файлы в backup-папку и печатает отчёт.
# podcast.yaml не трогается.
#
# Использование:
#   scripts/migrate-to-numbers.sh [ПАПКА_ПОДКАСТОВ] [--db ПУТЬ] [--yes]
#
# --db    путь к SQLite-базе (по умолчанию ../heavytech.db от папки подкастов)
# --yes   не требовать остановки контейнера (для прогона на синтетических папках)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Точное имя контейнера; пусто — искать любой контейнер с «heavytech» в имени
# (docker compose даёт heavytech-heavytech-1).
CONTAINER_NAME="${CONTAINER_NAME:-}"

PODCAST_DIR=""
DB_PATH=""
ASSUME_YES=0

usage() {
  sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db)
      [[ $# -ge 2 ]] || { echo "ошибка: --db требует путь" >&2; exit 2; }
      DB_PATH="$2"; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "ошибка: неизвестный флаг $1" >&2; exit 2 ;;
    *)
      [[ -z "$PODCAST_DIR" ]] || { echo "ошибка: лишний аргумент $1" >&2; exit 2; }
      PODCAST_DIR="$1"; shift ;;
  esac
done

PODCAST_DIR="${PODCAST_DIR:-data/podcast}"
[[ -d "$PODCAST_DIR" ]] || { echo "ошибка: нет папки подкастов $PODCAST_DIR" >&2; exit 1; }
[[ -f "$PODCAST_DIR/podcast.yaml" ]] || { echo "ошибка: в $PODCAST_DIR нет podcast.yaml" >&2; exit 1; }
DB_PATH="${DB_PATH:-$(dirname "$PODCAST_DIR")/heavytech.db}"

# --- 1. собираем плоские медиафайлы прежней конвенции -----------------------
mapfile -t FILES < <(
  find "$PODCAST_DIR" -maxdepth 1 -type f \( -name '*.m4a' -o -name '*.mp3' -o -name '*.aac' \) 2>/dev/null |
    grep -E '/[0-9]{4}-[0-9]{2}-[0-9]{2}-[^/]+\.(m4a|mp3|aac)$' |
    LC_ALL=C sort || true
)

if [[ ${#FILES[@]} -eq 0 ]]; then
  echo "==> плоских медиафайлов нет — мигрировать нечего"
  exit 0
fi

# Папки-номера уже существуют — это либо повторный запуск, либо ручной layout.
for d in "$PODCAST_DIR"/*/; do
  [[ -d "$d" ]] || continue
  name="$(basename "$d")"
  if [[ "$name" =~ ^[0-9]+$ ]]; then
    echo "ошибка: в папке уже есть эпизод-папка $name — миграция не выполняется" >&2
    exit 1
  fi
done

# Один выпуск в двух форматах (2026-10-01-slug.m4a и .mp3) — неоднозначно.
declare -A SEEN_STEM=()
for f in "${FILES[@]}"; do
  base="$(basename "$f")"
  stem="${base%.*}"
  if [[ -n "${SEEN_STEM[$stem]:-}" ]]; then
    echo "ошибка: два файла одного выпуска: ${SEEN_STEM[$stem]} и $base — оставьте один" >&2
    exit 1
  fi
  SEEN_STEM[$stem]="$base"
done

# --- 2. контейнер должен быть остановлен ------------------------------------
if command -v docker >/dev/null 2>&1; then
  if [[ -n "$CONTAINER_NAME" ]]; then
    RUNNING="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -x "$CONTAINER_NAME" || true)"
  else
    RUNNING="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -i 'heavytech' || true)"
  fi
  if [[ -n "$RUNNING" ]]; then
    if [[ $ASSUME_YES -ne 1 ]]; then
      cat >&2 <<EOF
ошибка: контейнер(ы) запущены:
$(sed 's/^/  /' <<<"$RUNNING")
Сначала остановите их, чтобы сервис не писал в базу во время миграции:
  docker stop $RUNNING
(для прогона на синтетической папке без docker передайте --yes)
EOF
      exit 1
    fi
    echo "==> ВНИМАНИЕ: контейнер(ы) запущены ($(tr '\n' ' ' <<<"$RUNNING")), продолжаю по --yes"
  fi
fi

# --- 3. база уводится в sidecar-имя -----------------------------------------
TS="$(date +%Y%m%d-%H%M%S)"
if [[ -f "$DB_PATH" ]]; then
  BACKUP="${DB_PATH}.pre-numbers-${TS}"
  mv -- "$DB_PATH" "$BACKUP"
  for ext in -wal -shm; do
    [[ -f "${DB_PATH}${ext}" ]] && mv -- "${DB_PATH}${ext}" "${BACKUP}${ext}"
  done
  echo "==> база сохранена: $BACKUP"
else
  echo "==> базы $DB_PATH нет — новая схема начнётся с пустой"
fi

# --- 4. конвертация ---------------------------------------------------------
FFPROBE="$(command -v ffprobe || true)"
if [[ -n "$FFPROBE" ]]; then
  echo "==> ffprobe: $FFPROBE (duration будет заполнен)"
else
  echo "==> ffprobe недоступен — duration останется пустым (это допустимо)"
fi

SIDECARS_DIR="$(dirname "$PODCAST_DIR")/sidecars-backup-${TS}"
sidecars_moved=0

# yaml_value FILE KEY — грубое чтение скалярного значения из прежнего sidecar.
yaml_value() {
  awk -v k="$2" '
    $0 ~ "^" k ":[[:space:]]*" {
      sub("^" k ":[[:space:]]*", "")
      sub("[[:space:]]+$", "")
      if ($0 ~ /^".*"$/ || $0 ~ /^\x27.*\x27$/) { $0 = substr($0, 2, length($0) - 2) }
      print
      exit
    }' "$1"
}

# yaml_quote TEXT — безопасный скаляр в двойных кавычках.
yaml_quote() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  printf '"%s"' "$s"
}

number=0
report=()
for f in "${FILES[@]}"; do
  base="$(basename "$f")"
  stem="${base%.*}"
  date="${stem:0:10}"
  slug="${stem:11}"

  if ! date -d "$date" +%F >/dev/null 2>&1; then
    echo "ошибка: в имени $base некорректная дата $date" >&2
    exit 1
  fi

  number=$((number + 1))
  target="$PODCAST_DIR/$number"
  mkdir -p "$target"

  # title прежним алгоритмом: слова из дефисов, первая буква заглавная.
  title=""
  IFS='-' read -r -a words <<<"$slug"
  for w in "${words[@]}"; do
    [[ -z "$w" ]] && continue
    w="${w^}"
    [[ -n "$title" ]] && title+=" "
    title+="$w"
  done

  # Прежний sidecar <slug>.yaml: title/description/duration переезжают в meta.
  description=""
  sidecar_duration=""
  sidecar="$PODCAST_DIR/$slug.yaml"
  if [[ -f "$sidecar" ]]; then
    sidecar_title="$(yaml_value "$sidecar" title)"
    sidecar_desc="$(yaml_value "$sidecar" description)"
    sidecar_duration="$(yaml_value "$sidecar" duration)"
    [[ -n "$sidecar_title" ]] && title="$sidecar_title"
    description="$sidecar_desc"
    mkdir -p "$SIDECARS_DIR"
    mv -- "$sidecar" "$SIDECARS_DIR/"
    sidecars_moved=$((sidecars_moved + 1))
  fi

  duration=""
  if [[ -n "$FFPROBE" ]]; then
    raw="$("$FFPROBE" -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 "$f" 2>/dev/null || true)"
    raw="${raw%.*}"
    if [[ "$raw" =~ ^[0-9]+$ ]] && (( raw > 0 )); then
      duration="$raw"
    fi
  fi
  if [[ -z "$duration" && "$sidecar_duration" =~ ^[0-9]+$ ]] && (( sidecar_duration > 0 )); then
    duration="$sidecar_duration"
  fi

  mv -- "$f" "$target/$base"
  {
    echo "title: $(yaml_quote "$title")"
    echo "published_at: $date"
    [[ -n "$description" ]] && echo "description: $(yaml_quote "$description")"
    echo "audio:"
    echo "  file: $(yaml_quote "$base")"
    [[ -n "$duration" ]] && echo "  duration: $duration"
  } >"$target/episode.yaml"

  # Папка и meta наследуют владельца перенесённого файла (на CT — 65532).
  # Папке нужен x-бит для обхода (как у podcast/, 0775), meta — обычные 0644.
  chown --reference="$target/$base" "$target" "$target/episode.yaml" 2>/dev/null || true
  chmod 0775 "$target" 2>/dev/null || true
  chmod 0644 "$target/episode.yaml" 2>/dev/null || true

  dur_label="${duration:+${duration}s}"
  report+=("$(printf '%4d  %s -> %d/%s%s' "$number" "$base" "$number" "$base" "${dur_label:+ (duration ${dur_label})}")")
done

# --- 5. отчёт ---------------------------------------------------------------
echo
echo "==> мигрировано эпизодов: $number (нумерация от старейшего)"
printf '%s\n' "${report[@]}"
[[ $sidecars_moved -gt 0 ]] && echo "==> старых sidecar-файлов перенесено: $sidecars_moved ($SIDECARS_DIR)"
echo "==> podcast.yaml не тронут: $PODCAST_DIR/podcast.yaml"
echo "==> дальше: деплой новой версии, рескан пересоберёт индекс из папок"
