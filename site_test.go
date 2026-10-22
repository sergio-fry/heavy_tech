package main

import (
	"encoding/json"
	"encoding/xml"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeChannelContent — podcast.yaml с произвольным содержимым.
func writeChannelContent(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "podcast.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const channelWithLanding = `title: Heavy Tech
description: Подкаст о тяжёлом
tagline: Разговоры о разработке
image: https://cdn.example.com/cover.jpg
links:
  - label: Apple Podcasts
    url: https://podcasts.apple.com/ru/podcast/heavy-tech/id123
  - label: Telegram
    url: https://t.me/heavytech
start_here: [2, 1]
`

// seedEpisodes — четыре выпуска с аудио и разными датами.
func seedEpisodes(t *testing.T, dir string, st *Store) {
	t.Helper()
	for i, date := range []string{"2026-10-01", "2026-10-05", "2026-10-10", "2026-10-15"} {
		number := string(rune('1' + i))
		writeEpisode(t, dir, number, metaAudio("Выпуск "+number, date, "z.m4a"), map[string]string{"z.m4a": "data"})
	}
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
}

func getPage(t *testing.T, srv *Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestLandingAbout — текст about.md, tagline, подборка и кнопки подписки.
func TestLandingAbout(t *testing.T) {
	dir := writeChannelContent(t, channelWithLanding)
	about := "## О подкасте\n\nДлинный промо-текст для поисковиков."
	if err := os.WriteFile(filepath.Join(dir, "about.md"), []byte(about), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t)
	seedEpisodes(t, dir, st)
	srv := serverOn(t, dir, st)

	code, html := getPage(t, srv, "/")
	if code != 200 {
		t.Fatalf("код %d", code)
	}
	for _, want := range []string{
		"Разговоры о разработке",
		"<h2>О подкасте</h2>",
		"Длинный промо-текст для поисковиков.",
		"С чего начать",
		`href="https://podcasts.apple.com/ru/podcast/heavy-tech/id123" target="_blank" rel="noopener noreferrer"`,
		`href="https://t.me/heavytech" target="_blank" rel="noopener noreferrer"`,
		`href="/episodes"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("на лендинге нет %q", want)
		}
	}
	// подборка идёт в заданном порядке: 2, затем 1
	start := strings.Index(html, "С чего начать")
	end := strings.Index(html, "Последние выпуски")
	if start < 0 || end < start {
		t.Fatal("нет секций «С чего начать» и «Последние выпуски»")
	}
	block := html[start:end]
	if strings.Index(block, "/episodes/2") > strings.Index(block, "/episodes/1") {
		t.Fatal("start_here выведен не в заданном порядке")
	}
}

// TestLandingAboutReloadedByRescan — правка about.md подхватывается ресканом.
func TestLandingAboutReloadedByRescan(t *testing.T) {
	dir := writeChannelContent(t, "title: Heavy Tech\ndescription: О тяжёлом\n")
	st := newTestStore(t)
	seedEpisodes(t, dir, st)
	srv := serverOn(t, dir, st)

	if _, html := getPage(t, srv, "/"); strings.Contains(html, "Новый текст") {
		t.Fatal("до правки нового текста быть не должно")
	}
	if err := os.WriteFile(filepath.Join(dir, "about.md"), []byte("Новый текст лендинга"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/admin/rescan", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("рескан: код %d", rec.Code)
	}
	if _, html := getPage(t, srv, "/"); !strings.Contains(html, "Новый текст лендинга") {
		t.Fatal("рескан не перечитал about.md")
	}
}

// TestLandingAboutSanitized — сырой HTML из about.md не исполняется.
func TestLandingAboutSanitized(t *testing.T) {
	dir := writeChannelContent(t, "title: Heavy Tech\ndescription: О тяжёлом\n")
	if err := os.WriteFile(filepath.Join(dir, "about.md"), []byte("Текст\n\n<script>alert(1)</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t)
	srv := serverOn(t, dir, st)
	_, html := getPage(t, srv, "/")
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("about.md исполнил сырой script")
	}
}

// TestLandingWithoutAbout — без файла лендинг показывает описание канала.
func TestLandingWithoutAbout(t *testing.T) {
	dir := writeChannelContent(t, "title: Heavy Tech\ndescription: Описание без файла\n")
	st := newTestStore(t)
	seedEpisodes(t, dir, st)
	_, html := getPage(t, serverOn(t, dir, st), "/")
	if !strings.Contains(html, "Описание без файла") {
		t.Fatal("нет фолбэка на описание канала")
	}
}

// TestSitemapAndRobots — карта сайта и robots с абсолютными URL.
func TestSitemapAndRobots(t *testing.T) {
	env := newTestEnv(t)
	code, body := getPage(t, env.server, "/sitemap.xml")
	if code != 200 {
		t.Fatalf("sitemap: код %d", code)
	}
	var doc struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("sitemap XML: %v", err)
	}
	locs := map[string]bool{}
	for _, u := range doc.URLs {
		locs[u.Loc] = true
	}
	for _, want := range []string{
		testBaseURL + "/",
		testBaseURL + "/episodes",
		testBaseURL + "/episodes/1",
		testBaseURL + "/episodes/2",
		testBaseURL + "/episodes/3",
	} {
		if !locs[want] {
			t.Fatalf("в карте нет %q: %+v", want, locs)
		}
	}

	code, robots := getPage(t, env.server, "/robots.txt")
	if code != 200 || !strings.Contains(robots, "Sitemap: "+testBaseURL+"/sitemap.xml") {
		t.Fatalf("robots: %d %q", code, robots)
	}

	// удалённый выпуск исчезает из карты после рескана
	if err := os.RemoveAll(filepath.Join(env.dir, "2")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/admin/rescan", nil)
	rec := httptest.NewRecorder()
	env.server.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("рескан: %d", rec.Code)
	}
	_, body = getPage(t, env.server, "/sitemap.xml")
	if strings.Contains(body, testBaseURL+"/episodes/2") {
		t.Fatal("удалённый выпуск остался в карте сайта")
	}
}

// jsonLDBlock вырезает JSON-LD со страницы и разбирает его.
func jsonLDBlock(t *testing.T, html string) map[string]any {
	t.Helper()
	const open = `<script type="application/ld+json">`
	start := strings.Index(html, open)
	if start < 0 {
		t.Fatal("нет JSON-LD")
	}
	start += len(open)
	end := strings.Index(html[start:], "</script>")
	if end < 0 {
		t.Fatal("JSON-LD не закрыт")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(html[start:start+end]), &doc); err != nil {
		t.Fatalf("JSON-LD не разобрался: %v", err)
	}
	return doc
}

// TestSEOEpisode —— canonical, meta description, OG и PodcastEpisode.
func TestSEOEpisode(t *testing.T) {
	env := newTestEnv(t)
	_, html := getPage(t, env.server, "/episodes/1")
	for _, want := range []string{
		"<title>Первый выпуск — Heavy Tech</title>",
		`<link rel="canonical" href="` + testBaseURL + `/episodes/1">`,
		`<meta name="description" content="О чём этот подкаст">`,
		`property="og:type" content="article"`,
		`property="og:url" content="` + testBaseURL + `/episodes/1"`,
		`rel="alternate" type="application/rss+xml"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("в SEO-разметке нет %q", want)
		}
	}
	doc := jsonLDBlock(t, html)
	if doc["@type"] != "PodcastEpisode" || doc["name"] != "Первый выпуск" ||
		doc["datePublished"] != "2026-10-01" || doc["duration"] != "PT31M12S" {
		t.Fatalf("PodcastEpisode: %+v", doc)
	}
	series, ok := doc["partOfSeries"].(map[string]any)
	if !ok || series["name"] != "Heavy Tech" {
		t.Fatalf("partOfSeries: %+v", doc["partOfSeries"])
	}
}

// TestSEOHome — лендинг объявляет PodcastSeries.
func TestSEOHome(t *testing.T) {
	env := newTestEnv(t)
	_, html := getPage(t, env.server, "/")
	doc := jsonLDBlock(t, html)
	if doc["@type"] != "PodcastSeries" || doc["name"] != "Heavy Tech" || doc["url"] != testBaseURL+"/" {
		t.Fatalf("PodcastSeries: %+v", doc)
	}
}

// TestSubscribeLinks — кнопки из links в заданном порядке; без links остаётся RSS.
func TestSubscribeLinks(t *testing.T) {
	dir := writeChannelContent(t, channelWithLanding)
	st := newTestStore(t)
	seedEpisodes(t, dir, st)
	_, html := getPage(t, serverOn(t, dir, st), "/")
	if strings.Index(html, "Apple Podcasts") > strings.Index(html, "Telegram") {
		t.Fatal("кнопки подписки не в заданном порядке")
	}

	dir2 := writeChannelContent(t, "title: Heavy Tech\ndescription: О тяжёлом\n")
	st2 := newTestStore(t)
	seedEpisodes(t, dir2, st2)
	_, html = getPage(t, serverOn(t, dir2, st2), "/")
	if !strings.Contains(html, "js-copy-rss") || !strings.Contains(html, testBaseURL+"/feed.xml") {
		t.Fatal("без links нет копирования RSS")
	}
}

// TestZeroCounterHiddenAndYouTubeViews — нулевой счётчик скрыт, просмотры входят в сумму.
func TestZeroCounterHiddenAndYouTubeViews(t *testing.T) {
	env := newTestEnv(t)
	_, html := getPage(t, env.server, "/episodes/1")
	if strings.Contains(html, "Прослушиваний и просмотров:") {
		t.Fatal("нулевой счётчик не должен выводиться")
	}

	if err := env.store.SaveYouTubeMeta(1, "abcdefghijk", 200, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.RecordDownload(1, KindAudio, "203.0.113.5", "App/1", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, html = getPage(t, env.server, "/episodes/1")
	if !strings.Contains(html, "Прослушиваний и просмотров: 201") {
		t.Fatal("счётчик не сложил скачивание и просмотры YouTube")
	}
}

// TestEpisodeChaptersRendered — главы отдельным разделом, не в описании.
func TestEpisodeChaptersRendered(t *testing.T) {
	env := newTestEnv(t)
	chapters := []Chapter{{Start: 0, Title: "Вступление"}, {Start: 750, Title: "Основная часть"}}
	if err := env.store.SaveYouTubeMeta(1, "abcdefghijk", 10, chapters, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, html := getPage(t, env.server, "/episodes/1")
	for _, want := range []string{
		"Главы · 2",
		`class="chapter js-chapter" data-start="750"`,
		`class="mark"`,
		"12:30",
		"Основная часть",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("нет главы %q", want)
		}
	}
	descStart := strings.Index(html, `class="desc"`)
	if descStart >= 0 && strings.Contains(html[descStart:], "Основная часть") {
		t.Fatal("главы попали в шоу-ноуты")
	}
}

// TestAPIExtendedFields — youtube_views и chapters в машинном ответе.
func TestAPIExtendedFields(t *testing.T) {
	env := newTestEnv(t)
	chapters := []Chapter{{Start: 0, Title: "Начало"}, {Start: 30, Title: "Дальше"}}
	if err := env.store.SaveYouTubeMeta(1, "abcdefghijk", 77, chapters, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec := env.do(t, "GET", "/api/episodes", "curl/8", nil, nil)
	var eps []apiEpisode
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatal(err)
	}
	for _, e := range eps {
		if e.Number != 1 {
			continue
		}
		if e.YouTubeViews != 77 || len(e.Chapters) != 2 || e.Chapters[1].Start != 30 || e.Chapters[1].Title != "Дальше" {
			t.Fatalf("расширенные поля: %+v", e)
		}
		return
	}
	t.Fatal("эпизод 1 не найден")
}

// TestStaticAssets — CSS и JS раздаются из embed.
func TestStaticAssets(t *testing.T) {
	env := newTestEnv(t)
	for _, path := range []string{"/static/style.css", "/static/app.js"} {
		rec := env.do(t, "GET", path, "Mozilla/5.0", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: код %d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: пустой ответ", path)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Fatalf("%s: нет Cache-Control: %q", path, cc)
		}
	}
	rec := env.do(t, "GET", "/static/app.js", "Mozilla/5.0", nil, nil)
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("Content-Type JS: %q", ct)
	}
}

// TestEpisodePageNeighbors — ссылки на соседей в порядке публикации.
func TestEpisodePageNeighbors(t *testing.T) {
	env := newTestEnv(t)
	_, html := getPage(t, env.server, "/episodes/2")
	// 3 новее, 1 старее
	if !strings.Contains(html, `href="`+testBaseURL+`/episodes/3"`) || !strings.Contains(html, `href="`+testBaseURL+`/episodes/1"`) {
		t.Fatal("нет ссылок на соседние выпуски")
	}
	_, html = getPage(t, env.server, "/episodes/3")
	if strings.Contains(html, `href="`+testBaseURL+`/episodes/4"`) {
		t.Fatal("у самого нового выпуска не должно быть ссылки на более новый")
	}
}

// TestEpisodePageVideoOnly — у видео-выпуска нет плеера, но есть скачивание.
func TestEpisodePageVideoOnly(t *testing.T) {
	env := newTestEnv(t)
	_, html := getPage(t, env.server, "/episodes/3")
	if strings.Contains(html, `id="inline-player"`) {
		t.Fatal("у видео-выпуска не должно быть аудиоплеера")
	}
	if !strings.Contains(html, `download>Скачать Видео`) {
		t.Fatal("нет ссылки на скачивание видео")
	}
}
