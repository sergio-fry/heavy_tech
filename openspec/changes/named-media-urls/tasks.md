# Tasks

## 1. Имя файла: slug и построение URL

- [x] 1.1 Новый `slug.go`: `Slugify(title)` (транслит RU→Latin, нижний регистр, `[a-z0-9-]`, схлопывание дефисов, обрезка по слову до 60) и `MediaFilename(number, title, file)` (номер `%03d`, slug, реальное расширение в нижнем регистре; пустой slug → `039.ext`). Проверка: `slug_test.go` — «Все фломастеры разные» → `vse-flomastery-raznye`, номер 39 → `039`, 1000 → `1000`, обрезка длинного заголовка без хвостового дефиса, эмодзи-заголовок без slug, сохранение `.m4a`/`.mp4`; `go test -run TestSlug ./...` зелёный.
- [x] 1.2 Хелперы `MediaURL(baseURL, kind, number, title, file)` и `MediaGUID(baseURL, kind, number)` (в `handlers.go` или отдельном `links.go`), использующие `MediaFilename`. Проверка: юнит-тест — именованный URL вида `https://host/video/39/039-<slug>.mp4` и номерной guid `https://host/video/39`; `go test ./...` проходит.

## 2. Роуты и учёт скачиваний

- [x] 2.1 `handlers.go`: маршруты `GET /audio/{number}/{name}` и `GET /video/{number}/{name}` (в дополнение к номерным), имя сегмента игнорируется, поиск по номеру; `Content-Disposition` не ставится. Проверка: тесты — именованный URL 200 с правильным Content-Type и полным телом, Range 206, 404 на неизвестный номер, 200 на ранее опубликованное/произвольное имя после смены заголовка, номерной URL по-прежнему 200; `go test -run 'TestAudio|TestVideo' ./...` зелёный.
- [x] 2.2 Учёт по именованным URL: тот же клиент в те же сутки запрашивает именованный и номерной URL одного носителя — событие одно, счётчик вырос на 1. Проверка: тест на `RecordDownload` через HTTP (два GET, один счётчик); `go test ./...` зелёный.

## 3. Поверхности: фиды, сайт, API

- [x] 3.1 `feed.go`: enclosure — `MediaURL`, guid — `MediaGUID` (оба фида). Проверка: тесты фидов — enclosure оканчивается на `/039-<slug>.<ext>` с точным размером и MIME, guid равен `/audio/{n}` или `/video/{n}`, пустой канал и AAC-сценарий не сломаны; `go test -run TestFeed ./...` зелёный.
- [x] 3.2 Сайт: `buildPageData` отдаёт именованные `Media[].URL` и `AudioURL`; `<audio src>` и ссылки «скачать» используют их, атрибут `download` сохранён. Проверка: `TestHome`/`TestBuildPageData` — именованные href и `src`, наличие `download`, старые проверки размеров/плеера/счётчика сохранены; `go test ./...` зелёный.
- [x] 3.3 API: `media[].url` — именованный URL. Проверка: `TestAPI` — URL равен `MediaURL` для аудио и видео; `go test -run TestAPI ./...` зелёный.
- [x] 3.4 README: форма медиа-URL, правила имени (номер 3 цифры, транслит-slug, обрезка, расширение), один URL на стриминг и скачивание, неизменность guid, работа старых ссылок; обновить строки таблицы эндпоинтов. Проверка: curl локального прогона на фикстуре — ссылка на сайте, enclosure в фиде и `media[].url` в API совпадают по форме, номерной URL отдаёт 200.

## 4. Интеграция

- [x] 4.1 `scripts/smoke.sh`: проверить именованные ссылки в HTML (href и `src`, `download`), enclosure именованный и guid номерной в `/feed.xml` и `/feed-video.xml`, `media[].url` в `/api/episodes`, 206 по именованному Range-запросу, 200 по номерному URL. Проверка: `./scripts/smoke.sh` завершается `SMOKE OK`.
- [x] 4.2 Полный прогон качества: `go vet ./...`, `gofmt -l .` (пусто), `go test ./...`, `CGO_ENABLED=0 go build -o /tmp/heavytech .`. Проверка: все команды успешны, `gofmt` печатает пусто.

## Workflow follow-up

- Выкатка на CT `heavytech` по явной команде: bump `heavytech_git_ref` в infra-репозитории, `make run ANSIBLE_OPTS="--limit heavytech"`; схемных миграций нет, фиды пересоберутся на старте.
- Прод-смоук: ссылки на сайте и enclosure в фидах с именем файла, guid прежний, `/audio/{n}` и `/video/{n}` отдают 200, скачивание сохраняет файл с именем из URL.
- Архивировать изменение после ревью и завершения задач.
