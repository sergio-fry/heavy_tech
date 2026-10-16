# Design

## Context

Репозиторий пуст — greenfield. Сервис дополняет студию записи Tributary (CT 132): готовые MP3 попадают в статичную папку, этот сервис их публикует. Ориентиры стека взяты из опыта пользователя в profilerpro: Go 1.26, `modernc.org/sqlite` (pure Go, без CGO), миграции goose. Деплой позже по конвенциям infra (LXC + systemd + edge-Caddy) — вне этого репо.

## Goals / Non-Goals

**Goals:**

- Один бинарник + SQLite-файл + статичная папка; минимум зависимостей.
- Правильный фид с первого дня: валидный RSS 2.0 + iTunes, с которым подкаст-приложения справляются без ручных танцев.
- Честные при Range-докачках счётчики скачиваний; простые сырые события прослушиваний.
- Папка переносима: скопировал каталог на новую машину — контент восстановлен (метрики заново).

**Non-Goals:**

- Никакой админки и авторизации в коде (решается на edge-Caddy при необходимости).
- IAB-сертифицируемая метрика, парсинг MP3-фреймов, транскодирование, генерация обложек.
- Мультишоунесс (несколько подкастов на инстанс), CDN, подписки/платный доступ.

## Decisions

**D1. Стек: stdlib `net/http` (роутинг Go 1.22+), `modernc.org/sqlite`, goose, `html/template`, фид рукописный на `encoding/xml`.**
Альтернативы: chi/gin (роутер не нужен для 6 маршрутов), mattn/go-sqlite3 (CGO усложняет сборку без пользы), gofeed (фид — 150 строк XML-структур, зависимость не окупается). WAL-режим SQLite; чтения и записи лёгкие, конкурентность низкая.

**D2. Папка — истина, SQLite — производный индекс.**
Сканер: при старте + `POST /admin/rescan`. Алгоритм — сверка содержимого папки с таблицей `episodes`: добавить новые, удалить исчезнувшие, обновить size/mtime изменившихся. Альтернатива «SQLite как истина + загрузки» отвергнута — превращает сервис в CMS; fsnotify отвергнут как лишняя зависимость (рескан по кнопке достаточен).

**D3. Именование и метадата:** `YYYY-MM-DD-slug.mp3` + опциональный `slug.yaml` (title, description, duration) + `podcast.yaml` (канал: title, link/URL базы, description, author, email, image, category, explicit). Отсутствие `podcast.yaml` — фатальная ошибка старта: сервис не должен публиковать канал без метаданных. Путь к папке и публичный базовый URL — флаги/ENV конфига.

**D4. Стриминг: `http.ServeContent`.** Даёт Range/If-Range/206 из коробки. Поверх — middleware учёта скачиваний (см. D6). Путь `/audio/{slug}` (без расширения) — стабильный URL для guid.

**D5. Фид:** RSS 2.0 + `xmlns:itunes`, структуры `encoding/xml`. guid = абсолютный URL аудио; enclosure length = `stat` файла; pubDate из даты имени файла; `itunes:duration` из sidecar либо оценка `ceil(bytes / (битрейт_по_умолчанию))` с пометкой в коде; сортировка новых сверху; кэширование собранного XML до рескана (фид пересобирается только при изменении индекса, не на каждый запрос).

**D6. Метрики.**
Скачивания: middleware на `/audio/` — пропускать HEAD, пустой UA и UA с подстроками `bot|spider|crawler|monitor`; ключ `(episode_id, sha256(ip+соль), sha256(ua+соль), UTC-дата)`; вставка в `download_events` с `INSERT OR IGNORE` по уникальному индексу — вставилось = скачали, конфликт = уже считали. IP брать из `X-Forwarded-For` (за edge-Caddy) с фолбэком на RemoteAddr.
Плеи: `POST /track/play` `{slug, progress?}` → запись в `play_events`; слаг валидируется по индексу.
Агрегаты: `SELECT count(DISTINCT ...)` / `count(*)` на лету — объёмы хобби-подкаста это позволяют; сырые события сохраняют возможность графиков по дням позже.

**D7. Схема (goose-миграции):**

```sql
episodes(id INTEGER PK, slug TEXT UNIQUE NOT NULL, title TEXT NOT NULL,
         description TEXT NOT NULL DEFAULT '', published_at DATE NOT NULL,
         file_path TEXT NOT NULL, file_size INTEGER NOT NULL, mime TEXT NOT NULL,
         duration_seconds INTEGER, file_mtime INTEGER NOT NULL,
         discovered_at TIMESTAMP NOT NULL)
download_events(episode_id INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
         ip_hash BLOB NOT NULL, ua_hash BLOB NOT NULL, day DATE NOT NULL,
         created_at TIMESTAMP NOT NULL,
         UNIQUE(episode_id, ip_hash, ua_hash, day))
play_events(id INTEGER PK, episode_id INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
         progress INTEGER, created_at TIMESTAMP NOT NULL)
```

Соль хеширования — случайная при первом старте, хранится в SQLite (таблица `meta`): хеши стабильны в пределах инстанса, но не переносимы между инстансами (сознательно: меньше ценность данных при утечке бэкапа).

**D8. Сайт:** один `html/template` (список + плеер + счётчики), нативный `<audio controls>`, ~20 строк JS на бикон `navigator.sendBeacon`/fetch на play-событие. Без сборщика ассетов, стили инлайновым CSS в шаблоне.

**D9. Компоновка кода:** плоский пакет `main` (или `internal/` из 4-5 файлов: `main.go`, `config.go`, `scanner.go`, `store.go`, `handlers.go`, `feed.go`, `web/`): сервис маленький, слои не плодим.

## Risks / Trade-offs

- [Смена битрейта/перекодирование файла без смены имени] → сканер сравнивает size+mtime и обновляет индекс; гайд по guid не меняется.
- [`X-Forwarded-For` подделываем прямым запросом мимо прокси] → приемлемо для любопытственных метрик; при публичном экспозирировании — файрвол на порте сервиса (UFW по конвенциям infra).
- [Оценка длительности врёт для VBR] → длительность из sidecar приоритетна; в фиде это мягкое поле, приложение пересчитает.
- [SQLite-файл удалён вместе с метриками] → индекс восстанавливается ресканом; метрики теряются. Бэкап SQLite-файла — задача деплоя (вне репо).
- [Рескан под нагрузкой конкурентных запросов] → рескан в транзакции; чтения в WAL не блокируются; частота ресканов мизерная.

## Migration Plan

Greenfield: `go mod init` + скелет, миграции goose embed, конфиг флагами/ENV. Деплой — вне скоупа: собранный бинарник + `data/podcast/` + `data/heavy_tech.db` на новый LXC, systemd-юнит по образцу tributary, блок на edge-Caddy. Ролбэк — остановить юнит; данные живут в файлах, ничего не деградирует.

## Open Questions

- Домен и LXC-слот для деплоя — решается на этапе деплоя в infra-репо (CT 132 занят Tributary), на код не влияет.
