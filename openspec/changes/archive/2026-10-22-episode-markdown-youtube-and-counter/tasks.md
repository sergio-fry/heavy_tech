# Tasks

## 1. Зависимость, схема, рендер markdown

- [x] 1.1 Добавить `github.com/yuin/goldmark` в `go.mod` (`go get`, `go mod tidy`) и проверить чистую сборку без cgo: `CGO_ENABLED=0 go build -o /tmp/heavytech .` завершается успешно.
- [x] 1.2 Миграция goose `migrations/0003_episode_youtube.sql`: `ALTER TABLE episodes ADD COLUMN youtube TEXT NOT NULL DEFAULT ''` + Down-секция. Проверка: на временной базе `Up`/`Down`/`Up` проходят, существующие строки получают пустой `youtube`, `go test -run TestStore ./...` зелёный.
- [x] 1.3 Новый `markdown.go`: `RenderDescription(src string) template.HTML` на goldmark с hard-wraps, без raw HTML, с AST-санитайзером адресов ссылок/картинок (разрешены http/https, для ссылок также mailto и относительные). Проверка: новый `markdown_test.go` — абзацы и переносы, списки, `**выделение**`, ссылка, `javascript:`/`data:` превращаются в текст, `<script>` и `onerror` не попадают в вывод; `go test -run TestRenderDescription ./...` зелёный.
- [x] 1.4 `store.go`: поле `Episode.YouTube`, чтение/запись колонки, участие в `episodeEqual` (правка ссылки обновляет индекс). Проверка: `store_test.go` — сохранение и обновление `youtube` после повторного `Sync`; `go test ./...` проходит.

## 2. Сканер и конвенции папки

- [x] 2.1 `scanner.go`: чтение необязательного `description.md` (приоритет над `description:` в `episode.yaml`, предупреждение в лог при обоих, файл входит в известные), чтение и валидация поля `youtube:` (абсолютный http/https URL с host; иначе ошибка синхронизации). Проверка: `scanner_test.go` покрывает markdown-файл, приоритет, фолбэк на meta, отсутствие описания, правку `description.md` с обновлением индекса, валидную и некорректную YouTube-ссылку, отсутствие ошибки «лишний файл» на `description.md`; `go test -run TestScan ./...` зелёный.
- [x] 2.2 README: конвенция `description.md` (необязательный файл, приоритет над legacy-полем, правка требует рескана) и поле `youtube:` (необязательное, абсолютный http/https URL). Проверка: описанные имена и поля совпадают с кодом (`grep` по `scanner.go`), а прогон на фикстуре (`description.md` + `youtube:` → `POST /admin/rescan`) показывает описание и ссылку в `/api/episodes`.

## 3. Поверхности: фид, API, сайт

- [x] 3.1 `feed.go`: `description` item'а — HTML, отрендеренный из markdown-исходника (XML-экранирование остаётся на encoding/xml). Проверка: тест фида разбирает XML и убеждается, что в `description` есть `<p>`/`<strong>`/`<a>`, а исходной markdown-разметки нет; `go test -run TestFeed ./...` зелёный.
- [x] 3.2 `handlers.go` (`/api/episodes`): поле `youtube` (пустая строка, когда не задано), `description` остаётся markdown-исходником. Проверка: тест API проверяет оба случая (со ссылкой и без) и отсутствие HTML в `description`; `go test -run TestAPI ./...` зелёный.
- [x] 3.3 Сайт (`handlers.go` + `web/index.html`): `episodeItem.Engagement = downloads.total + plays` и одна подпись «Скачиваний и прослушиваний: N» вместо двух; описание выводится отрендеренным HTML; при заданной YouTube-ссылке — действие «Смотреть на YouTube» (`target="_blank" rel="noopener noreferrer"`). Проверка: обновлённые `TestHome`/`TestBuildPageData` — единый счётчик 155 для 120+30+5, отрендеренная разметка, кнопка YouTube и её отсутствие без ссылки, сохранены проверки ссылок `/audio/…` и `/video/…`; `go test ./...` зелёный.
- [x] 3.4 README: семантика единого счётчика на сайте («потребление» = скачивания + прослушивания, возможен двойной счёт одного человека), сохранение раздельных агрегатов в API, YouTube-ссылка на сайте и в API. Проверка: текст README соответствует фактическому выводу локального прогона (curl на фикстуре: одна цифра на сайте, `downloads`/`plays` и `youtube` в API).

## 4. Интеграция

- [x] 4.1 `scripts/smoke.sh`: в фикстуру добавить `description.md` с markdown и `youtube:`, добавить проверки отрендеренного описания на `/`, HTML-описания в `/feed.xml`, поля `youtube` в `/api/episodes`, единого счётчика. Проверка: `./scripts/smoke.sh` завершается `SMOKE OK`.
- [x] 4.2 Полный прогон качества: `go vet ./...`, `gofmt -l .` (пусто), `go test ./...`, `CGO_ENABLED=0 go build -o /tmp/heavytech .`. Проверка: все команды успешны, вывод `gofmt` пуст.

## Workflow follow-up

- Выкатка на CT `heavytech` (по явной команде пользователя): bump `heavytech_git_ref` в infra-репозитории, `make run ANSIBLE_OPTS="--limit heavytech"`; миграция `0003` применится при старте контейнера.
- Прод-смоук: `/` с единым счётчиком и отрендеренным описанием, `/feed.xml` с HTML-описанием, `/api/episodes` с `youtube`; при необходимости — рескан после добавления `description.md`/`youtube:` в папку выпусков.
- Архивировать изменение после ревью и завершения всех задач.
