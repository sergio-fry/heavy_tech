#!/usr/bin/env bash
# Локальный прогон сайта для просмотра правок без деплоя на CT heavytech.
#
# Собирает heavytech из текущего чекаута и поднимает на :4110 с зеркалом
# прод-данных. Сайт доступен снаружи: https://heavytech.dev.apps.udalovs.ru
# (edge-Caddy 192.168.88.35 -> 192.168.88.47:4110, блок в infra-репо
# ansible/roles/caddy/templates/Caddyfile.j2).
#
# Данные: /mnt/storage/heavytech-dev/data — зеркало прод-папки CT heavytech
# (192.168.88.134), SQLite-база своя: метрики в dev не копируются.
# Обновление зеркала после выхода нового выпуска на проде:
#   rsync -a --exclude='heavytech.db*' \
#     root@192.168.88.134:/mnt/storage/heavytech/data/ /mnt/storage/heavytech-dev/data/
#
# Правки кода: Ctrl-C -> повторный запуск (пересборка ~секунды).
set -euo pipefail
cd "$(dirname "$0")/.."

ADDR=":4110" \
BASE_URL="https://heavytech.dev.apps.udalovs.ru" \
PODCAST_DIR="/mnt/storage/heavytech-dev/data/podcast" \
DB_PATH="/mnt/storage/heavytech-dev/data/heavytech.db" \
YOUTUBE_API_KEY="${YOUTUBE_API_KEY:-}" \
go run .
