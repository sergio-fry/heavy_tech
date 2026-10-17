# heavytech

Публикатор подкаста из статичной папки: Go-сервис, который раздаёт готовые MP3,
отдаёт RSS-фид для подкаст-приложений, показывает сайт со списком эпизодов и
плеером и считает скачивания/прослушивания. Дополняет студию записи
[Tributary](https://github.com/Realmullens/tributary): записал выпуск — положил
файл в папку — сервис его публикует.

Управление — **через файлы**: никакой админки. Пользовательская история — через
публичный API и сайт.

## Как это работает

```
data/podcast/                 <- источник истины
  podcast.yaml                <- метаданные канала (обязателен)
  2026-10-01-pervyy-vypusk.mp3
  pervyy-vypusk.yaml          <- опциональный sidecar эпизода
data/heavytech.db             <- SQLite: производный индекс + метрики
```

- Сканер при старте и по `POST /admin/rescan` синхронизирует индекс с папкой:
  новые файлы добавляются, исчезнувшие удаляются (метрики удаляются вместе с
  эпизодом — CASCADE), изменившиеся (size+mtime) обновляются.
- SQLite — только индекс и события; удаление базы нестрашно для контента:
  индекс восстановится ресканом (метрики начнутся с нуля).

## Конвенции папки

### Имя файла эпизода: `YYYY-MM-DD-slug.mp3`

- `YYYY-MM-DD` — дата публикации (реальный календарный день);
- `slug` — `[a-z0-9-]`, стабильный идентификатор эпизода;
- всё, что не подходит под шаблон, игнорируется;
- два файла с одинаковым слагом — ошибка синхронизации.

Заголовок по умолчанию выводится из слага (`pervyy-vypusk` → «Pervyy Vypusk»).

**⚠️ Слаг неизменяем после публикации.** guid эпизода в фиде — это URL
аудиофайла, а счётчики привязаны к слагу. Переименование файла = удаление
старого эпизода и добавление нового (новый guid, нулевые счётчики).

### Sidecar `slug.yaml` (опционален)

```yaml
title: Первый выпуск        # иначе — из слага
description: О чём выпуск   # иначе пусто
duration: 1872              # секунды; иначе оценка по размеру (128 kbps)
```

Битый YAML в sidecar — ошибка синхронизации, а не тихое игнорирование.

### `podcast.yaml` (обязателен)

```yaml
title: Heavy Tech
description: Подкаст о тяжёлом в технологиях
author: Сергей Удалов
email: sergey@example.com    # для itunes:owner
image: https://.../cover.png # абсолютный URL обложки (от 1400x1400 для Apple)
category: Technology
explicit: false
link: https://show.example.com  # необязательно; иначе base-url
```

Без файла или с пустыми `title`/`description` сервис не стартует: канал без
метаданных не публикуется.

## Запуск

```bash
go build -o heavytech .
./heavytech \
  -addr :8080 \
  -base-url https://show.example.com \   # публичный URL для абсолютных ссылок
  -podcast-dir data/podcast \
  -db data/heavytech.db
```

Те же параметры через ENV: `ADDR`, `BASE_URL`, `PODCAST_DIR`, `DB_PATH`.

`-base-url` должен быть настоящим публичным адресом: из него строятся
абсолютные URL в фиде и API — подкаст-приложения ходят в фид снаружи.

## Эндпоинты

| Метод и путь | Что это |
|---|---|
| `GET /` | сайт: список эпизодов, плеер, счётчики |
| `GET /feed.xml` | RSS 2.0 + iTunes для подкаст-приложений |
| `GET /audio/{slug}` | аудио с поддержкой Range (= скачивание) |
| `POST /track/play` | бикон плеера, JSON `{"slug": "...", "progress": 0..100?}` (= прослушивание) |
| `GET /api/episodes` | JSON-список эпизодов со счётчиками |
| `POST /admin/rescan` | синхронизировать папку с индексом |

Всё публично и без авторизации. Если захочется прикрыть статистику — basic auth
вешается на reverse-proxy, код сервиса не меняется.

## Добавление выпуска (эксплуатация)

Управление — через файлы, без админки. Папка подкастов живёт на CT `heavytech`
на data-диске, в `/mnt/storage/heavytech/data/podcast/`; конвенции имён, sidecar
и `podcast.yaml` — выше.

**Канал.** `title`/`description` из `podcast.yaml` попадают в герой-блок сайта
и OG-теги, `image` (абсолютный URL, от 1400x1400 для Apple) — в обложку героя
и `og:image`. Файл правится руками на CT; роль при деплое его не затирает.

**Выпуск:**

1. Назвать файл `YYYY-MM-DD-slug.mp3`. Slug — `[a-z0-9-]`, после публикации
   неизменяем: guid в фиде = URL файла, счётчики привязаны к слагу.
2. Положить рядом sidecar `slug.yaml` с `title`, `description` и `duration`
   (в секундах; реальную можно снять:
   `ffprobe -v error -show_entries format=duration -of csv=p=0 episode.mp3`).
   Без `duration` длительность оценивается по размеру из расчёта 128 kbps.
3. Скопировать на CT и выставить владельца nonroot (65532), чтобы сканер мог
   удалять и заменять файлы:

   ```bash
   scp 2026-10-20-moy-vypusk.mp3 2026-10-20-moy-vypusk.yaml \
       root@192.168.88.134:/mnt/storage/heavytech/data/podcast/
   ssh root@192.168.88.134 chown 65532:65532 \
       /mnt/storage/heavytech/data/podcast/2026-10-20-moy-vypusk.*
   ```

4. Синхронизировать индекс и проверить:

   ```bash
   curl -X POST https://heavytech.udalovs.ru/admin/rescan
   # {"added":1,"updated":0,"removed":0,"total":4}
   curl -s https://heavytech.udalovs.ru/ | grep -o 'data-slug="[^"]*"'
   curl -s https://heavytech.udalovs.ru/feed.xml | grep -c '<item>'
   ```

Грабли: файл не по шаблону молча игнорируется; два файла с одним слагом или
битый YAML в sidecar — ошибка синхронизации; удаление файла после рескана
убирает выпуск вместе с метриками (CASCADE); ID3-теги не читаются —
заголовок берётся из sidecar или выводится из слага.

## Метрики (честно про точность)

Уровень «личное любопытство», не IAB:

- **Скачивание** — успешный `GET /audio/{slug}`: один раз в сутки (UTC) на
  комбинацию (эпизод, IP, User-Agent). Range-докачки не раздувают счётчик;
  HEAD-запросы, пустой UA и боты (`bot|spider|crawler|monitor`) не считаются.
- **Прослушивание** — бикон при нажатии play в веб-плеере.
- В базе только **сольтированные хеши** IP и UA: сырых персональных данных нет.
- IP берётся из `X-Forwarded-For` (сервис за reverse-proxy), иначе RemoteAddr.

## Разработка

```bash
go test ./...        # юнит- и httptest-покрытие всех эндпоинтов
./scripts/smoke.sh   # интеграционный прогон: фикстура -> старт -> проверки
go vet ./... && gofmt -l .
```

## Деплой

Вне этого репозитория — ansible-роль `heavytech` в infra: bump `heavytech_git_ref`
на новый SHA → `make run ANSIBLE_OPTS="--limit heavytech"` (на CT пересобирается
docker compose из `/opt/heavytech`). Состояние — host-volume `/opt/heavytech/data`
(папка подкастов + SQLite-база), наружу — edge-Caddy `heavytech.udalovs.ru`.
Бэкапить `data/` целиком (папка + база).
