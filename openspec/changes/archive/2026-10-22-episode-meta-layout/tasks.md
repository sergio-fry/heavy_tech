# Tasks

## 1. Схема БД и хранилище

- [x] 1.1 Миграция `migrations/0002_*.sql` (goose): `episodes(number PK, title, description, published_at, discovered_at)`, `episode_files(number, kind, file_path, mime, size, duration_seconds, bitrate_kbps, resolution, file_mtime, UNIQUE(number, kind))`, `kind` в `download_events` с уникальным индексом `(number, kind, ip_hash, ua_hash, day)`, `play_events` на номер; прежние таблицы пересоздаются (fresh start — design D5, Down-секция восстанавливает пустую старую схему). Проверка: `go test ./...` — тесты store зелёные.
- [x] 1.2 Переписать `store.go` на номерную модель (CRUD эпизодов и носителей, агрегаты downloads по kind, plays), обновить `store_test.go`. Проверка: `go test ./...` проходит.

## 2. Сканер папок

- [x] 2.1 `scanner.go`: обход подпапок с числовыми именами (`^[1-9][0-9]*$`; `0`/ведущие нули — ошибка, нецифровые — игнор), разбор и валидация `episode.yaml` (обязательные поля, секции, существование файлов, расширение по kind, лишние недот-файлы — ошибка, dot-файлы — игнор), таблица расширений → kind/MIME (design D3). Проверка: `go test ./...` — тесты сканера зелёные.
- [x] 2.2 Тесты `scanner_test.go` под папочный layout: валидный эпизод, нет meta, битый YAML, несуществующий файл, `007`, нецифровая папка, аудио+видео, аудио-файл в video-секции, лишний файл, dot-файл, idempotent-sync. Проверка: `go test ./...` проходит.

## 3. Роуты, фиды, счётчики

- [x] 3.1 `handlers.go`: роуты `GET /audio/{n}` и `GET /video/{n}` (Range, Content-Type по носителю, 404 для неизвестного номера/отсутствующего kind), счётчик скачиваний с kind; бикон `/track/play` на payload `{"episode": n}`; обновить `handlers_test.go`. Проверка: `go test ./...` — тесты стриминга и бикона зелёные.
- [x] 3.2 `feed.go`: два фида — `/feed.xml` (аудио-эпизоды) и `/feed-video.xml` (видео-эпизоды), enclosure и guid на носитель, `itunes:episode` = номер, `itunes:duration` из meta носителя (фолбэк-оценка 128 kbps для аудио), сортировка по `published_at`; обновить тесты фида (числа, оба фида, MIME по расширению). Проверка: `go test ./...` проходит.

## 4. Сайт и API

- [x] 4.1 `web/index.html`: карточка «Выпуск №N» (номер, дата словами, заголовок, описание, суммарные счётчики скачиваний и прослушиваний), аудиоплеер с `data-episode` (только для эпизодов с аудио), ссылки «скачать» по носителям с размерами, ссылка на видео-фид; обновить тесты сайта. Проверка: `go test ./...` — тесты страницы зелёные.
- [x] 4.2 `/api/episodes`: `number`, `title`, `description`, `published_at`, `media[{kind, url, size, duration}]`, `downloads{audio, video, total}`, `plays`; обновить тесты API. Проверка: `go test ./...` проходит.

## 5. Миграция и документация

- [x] 5.1 `scripts/migrate-to-numbers.sh` по design D7: стоп-контейнера подсказка, backup `heavytech.db` в sidecar, плоские `YYYY-MM-DD-slug.m4a` → папки-номера хронологически, генерация `episode.yaml` (title из слага прежним алгоритмом), `duration` через ffprobe если доступен, отчёт; прогнать на синтетической папке локально. Проверка: на тестовой папке структура и meta соответствуют ожиданиям сканера, рескан индексирует всё.
- [x] 5.2 README (конвенции папок: номера, `episode.yaml`, носители, два фида, неизменяемость номера, миграция), тексты флагов в `config.go`, обновить `scripts/smoke.sh` под папочный layout и оба фида. Проверка: `go build -o heavytech .` собирается; `scripts/smoke.sh` проходит локально на синтетической папке.
- [x] 5.3 Интеграционный прогон: поднять бинарь на папке из эпизодов 1..3 (audio; у одного — video), проверить `/feed.xml` (guid `/audio/1`, itunes:episode), `/feed-video.xml` (только с видео), `/audio/{n}`/`/video/{n}` Content-Type и Range, суммы счётчиков в `/` и `/api/episodes`, бикон по номеру. Проверка: вывод curl соответствует сценариям дельт.

## 6. Выкатка на сервер (выполняется по явной команде пользователя)

- [x] 6.1 На CT `heavytech`: остановить контейнер, прогнать `scripts/migrate-to-numbers.sh` на `/mnt/storage/heavytech/data/podcast`, сверить отчёт и структуру папок. Проверка: отчёт миграции без ошибок, у каждой папки есть валидный `episode.yaml`, права 65532 сохранены.
- [x] 6.2 Деплой: bump `heavytech_git_ref` в infra-репо на новый SHA, commit+push, `make run ANSIBLE_OPTS="--limit heavytech"`. Проверка: ansible recap без failed, сервис отвечает.
- [x] 6.3 Прод-смоук: `/feed.xml` и `/feed-video.xml` валидны и содержат выпуски с новыми guid, `/audio/{n}` отдаёт 200, суммы счётчиков на нуле, сайт рендерит карточки. Проверка: curl-вывод соответствует сценариям дельт.
