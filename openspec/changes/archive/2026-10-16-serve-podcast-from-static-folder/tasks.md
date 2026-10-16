# Tasks

## 1. Скелет и конфигурация

- [x] 1.1 `go mod init` (go 1.26), подключить `modernc.org/sqlite` и goose; структура пакетов по design.md (main + internal); `go build ./...` проходит
- [x] 1.2 Конфиг: флаги/ENV — путь к папке подкастов, публичный базовый URL, порт, путь к SQLite; при отсутствии `podcast.yaml` — понятная ошибка старта; проверить запуск на фикстурной папке
- [x] 1.3 Goose-миграции (embed): таблицы `episodes`, `download_events`, `play_events`, `meta` (соль хеширования) по design.md; проверить подъём накатом на временную БД

## 2. Сканер и индекс (episode-publishing)

- [x] 2.1 Парсер имён `YYYY-MM-DD-slug.mp3` + чтение sidecar `slug.yaml` (title, description, duration) и `podcast.yaml`; юнит-тесты на конвенции (валидное имя, файл без даты, sidecar переопределяет, дефолтный заголовок из слага)
- [x] 2.2 Синхронизация папка → SQLite: добавить новые, обновить изменившиеся (size+mtime), удалить исчезнувшие (CASCADE на события), идемпотентность повторного прогона; юнит-тесты на фикстурной папке (добавление, удаление, потеря БД, переименование файла)
- [x] 2.3 Хендлер `POST /admin/rescan` + автоскан при старте; проверка curl-ом: рескан после подкладывания файла делает эпизод видимым, повторный рескан идемпотентен

## 3. Публичная раздача (podcast-serving)

- [x] 3.1 Хендлер `GET /audio/{slug}` на `http.ServeContent` (Content-Type, Range/206); тесты httptest: полный 200, `Range: bytes=1000-1999` → 206 с Content-Range, неизвестный slug → 404
- [x] 3.2 Генератор фида: RSS 2.0 + itunes-namespace на `encoding/xml` (метаданные канала, новые сверху, enclosure с точным размером и mime, guid = абсолютный URL аудио, pubDate RFC 1123, itunes:duration из sidecar/оценки); тест на валидность XML и порядок/поля items; кэш собранного XML до рескана
- [x] 3.3 Хендлеры `GET /feed.xml` и `GET /api/episodes` (JSON: slug, title, description, published_at, duration, size, счётчики, абсолютный URL); тесты: фид на пустом индексе валиден без items, JSON упорядочен и полон
- [x] 3.4 HTML-страница `GET /` на `html/template`: список от новых к старым (дата, заголовок, описание, `<audio>` со ссылкой на /audio/{slug}, счётчики, ссылка на фид), заглушка пустого списка, инлайновый CSS; тест: эпизод виден на странице, пустой индекс даёт заглушку

## 4. Метрики (audience-metrics)

- [x] 4.1 Middleware скачиваний на `/audio/`: пропуск HEAD/пустого UA/UA с `bot|spider|crawler|monitor`; сольтированные sha256 от IP (X-Forwarded-For с фолбэком RemoteAddr) и UA; `INSERT OR IGNORE` по уникальному индексу (episode, ip_hash, ua_hash, day UTC); тесты: три Range-запроса за сутки = 1 скачивание, следующий день = ещё 1, HEAD/бот/пустой UA не считаются, в БД нет сырых IP/UA
- [x] 4.2 Хендлер `POST /track/play` (JSON `{slug, progress?}`): валидация слага по индексу, запись события, 4xx на неизвестный слаг; тесты: бикон растит plays на 1, неизвестный слаг → 4xx без записи
- [x] 4.3 Агрегаты на сайте и в API: `GET /` и `GET /api/episodes` показывают суммарные downloads/plays из сырых событий; тест согласованности: 3 скачивания и 1 прослушивание видны одинаково в HTML и JSON

## 5. Сборка и интеграционная проверка

- [x] 5.1 Демо-фикстура: папка с `podcast.yaml`, сгенерированным тихим MP3 и sidecar; smoke-прогон: старт → rescan → `/`, `/feed.xml`, `/api/episodes`, Range-запрос, play-бикон; числа в HTML/JSON согласованы, XML валиден (`xmllint` или парсером)
- [x] 5.2 README: конвенции папки (имена, sidecar, podcast.yaml), запуск, эндпоинты, предупреждение о неизменяемости слага, заметка про деплой (systemd/Caddy — указатель на infra-конвенции)
- [x] 5.3 `go vet ./...`, `gofmt -l .` чисты; `go test ./...` зелёный целиком

## Workflow follow-up

- Деплой на LXC + systemd + edge-Caddy — отдельная работа в infra-репо, не в этой change.
- Архивировать change после ревью артефактов и зелёных тестов.
