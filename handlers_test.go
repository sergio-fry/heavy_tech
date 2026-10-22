package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testBaseURL = "https://pod.example.com"

type testEnv struct {
	dir    string
	server *Server
	store  *Store
}

// addEpisodeFixtures — три выпуска: аудио, аудио+видео, только видео.
func addEpisodeFixtures(t *testing.T, dir string) {
	t.Helper()
	writeEpisode(t, dir, "1", `title: Первый выпуск
published_at: 2026-10-01
description: О чём этот подкаст
audio:
  file: zapis.aac
  duration: 1872
`, map[string]string{"zapis.aac": strings.Repeat("A", 12345)})
	writeEpisode(t, dir, "2", `title: Vtoroy
published_at: 2026-10-15
audio:
  file: zapis.mp3
video:
  file: efir.mp4
`, map[string]string{
		"zapis.mp3": strings.Repeat("B", 20000),
		"efir.mp4":  strings.Repeat("C", 5000),
	})
	writeEpisode(t, dir, "3", `title: Tolko video
published_at: 2026-10-20
video:
  file: efir.mkv
`, map[string]string{"efir.mkv": strings.Repeat("D", 7000)})
}

// newTestEnv — папка с каналом и тремя эпизодами, сервер с подменяемым временем.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := writePodcastDir(t)
	st := newTestStore(t)
	addEpisodeFixtures(t, dir)
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{BaseURL: testBaseURL, PodcastDir: dir}, ch, st)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{dir: dir, server: srv, store: st}
}

// do — запрос к серверу; ua пустой не оставляем, если задан явно.
func (env *testEnv) do(t *testing.T, method, target, ua string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("User-Agent", ua)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	env.server.ServeHTTP(rec, req)
	return rec
}

// --- 3.1 стриминг носителей ---

func TestAudioFull(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/audio/1", "PodcastApp/1.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/aac" {
		t.Fatalf("Content-Type %q, хотели audio/aac", ct)
	}
	if got := rec.Body.Len(); got != 12345 {
		t.Fatalf("тело %d байт, хотели 12345", got)
	}
}

func TestAudioRange(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/audio/1", "PodcastApp/1.0", nil,
		map[string]string{"Range": "bytes=1000-1999"})
	if rec.Code != 206 {
		t.Fatalf("код %d, хотели 206", rec.Code)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 1000-1999/12345" {
		t.Fatalf("Content-Range %q", cr)
	}
	if rec.Body.Len() != 1000 {
		t.Fatalf("тело %d байт, хотели 1000", rec.Body.Len())
	}
}

func TestVideoFull(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/video/2", "PodcastApp/1.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type %q, хотели video/mp4", ct)
	}
	if got := rec.Body.Len(); got != 5000 {
		t.Fatalf("тело %d байт, хотели 5000", got)
	}
}

func TestVideoRange(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/video/2", "PodcastApp/1.0", nil,
		map[string]string{"Range": "bytes=100-199"})
	if rec.Code != 206 {
		t.Fatalf("код %d, хотели 206", rec.Code)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 100-199/5000" {
		t.Fatalf("Content-Range %q", cr)
	}
}

func TestMediaNotFound(t *testing.T) {
	env := newTestEnv(t)
	for _, target := range []string{
		"/audio/999", // неизвестный номер
		"/audio/abc", // не номер
		"/audio/3",   // у эпизода 3 нет аудио
		"/video/1",   // у эпизода 1 нет видео
		"/video/999",
	} {
		rec := env.do(t, "GET", target, "PodcastApp/1.0", nil, nil)
		if rec.Code != 404 {
			t.Fatalf("%s: код %d, хотели 404", target, rec.Code)
		}
	}
}

// --- 3.2 фиды ---

// nsFeed/nsItem — тестовые структуры с URL-namespace тегами: encoding/xml
// unmarshal-ит itunes:* только по полному URL (в проде теги префиксные ради
// де-факто стандартного emit).
type nsFeed struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title    string   `xml:"title"`
		Link     string   `xml:"link"`
		Author   string   `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
		Explicit string   `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
		Items    []nsItem `xml:"item"`
	} `xml:"channel"`
}

type nsItem struct {
	Title string `xml:"title"`
	GUID  struct {
		IsPermaLink string `xml:"isPermaLink,attr"`
		Value       string `xml:",chardata"`
	} `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
	Enclosure   struct {
		URL    string `xml:"url,attr"`
		Length int64  `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
	Episode  string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
	Duration string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
}

// fetchFeed разбирает фид по адресу и проверяет базовую обвязку.
func fetchFeed(t *testing.T, env *testEnv, path string) nsFeed {
	t.Helper()
	rec := env.do(t, "GET", path, "PodcastApp/1.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("%s: код %d", path, rec.Code)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte(xml.Header)) {
		t.Fatalf("%s: нет XML-заголовка", path)
	}
	// xmlns-объявление Go-декодер поглощает и в поля не отдаёт — проверяем в сырых байтах
	if !bytes.Contains(body, []byte(`xmlns:itunes="`+itunesNS+`"`)) {
		t.Fatalf("%s: нет объявления itunes-namespace", path)
	}
	var feed nsFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		t.Fatalf("%s не разбирается: %v", path, err)
	}
	if feed.Version != "2.0" {
		t.Fatalf("%s: rss@version=%q", path, feed.Version)
	}
	if feed.Channel.Title != "Heavy Tech" || feed.Channel.Author != "Сергей" || feed.Channel.Explicit != "false" {
		t.Fatalf("%s: метаданные канала: %+v", path, feed.Channel)
	}
	return feed
}

func TestAudioFeed(t *testing.T) {
	env := newTestEnv(t)
	feed := fetchFeed(t, env, "/feed.xml")
	// эпизод 3 только с видео в аудио-фид не попадает
	if len(feed.Channel.Items) != 2 {
		t.Fatalf("items = %d, хотели 2", len(feed.Channel.Items))
	}
	if feed.Channel.Items[0].Title != "Vtoroy" {
		t.Fatalf("первый item %q, хотели «Vtoroy»", feed.Channel.Items[0].Title)
	}
	first := feed.Channel.Items[0]
	wantGUID := testBaseURL + "/audio/2"
	wantURL := testBaseURL + "/audio/2/002-vtoroy.mp3"
	if first.GUID.Value != wantGUID || first.GUID.IsPermaLink != "true" {
		t.Fatalf("guid %+v", first.GUID)
	}
	if first.Enclosure.URL != wantURL || first.Enclosure.Length != 20000 || first.Enclosure.Type != "audio/mpeg" {
		t.Fatalf("enclosure %+v", first.Enclosure)
	}
	if first.Episode != "2" {
		t.Fatalf("itunes:episode = %q, хотели 2", first.Episode)
	}
	if _, err := time.Parse(time.RFC1123Z, first.PubDate); err != nil {
		t.Fatalf("pubDate не RFC1123: %q (%v)", first.PubDate, err)
	}
	if first.Duration == "" {
		t.Fatal("itunes:duration пуст у эпизода без duration — ожидалась оценка")
	}
	// второй item — эпизод 1: aac-MIME и duration из meta
	second := feed.Channel.Items[1]
	if second.Enclosure.Type != "audio/aac" || second.Enclosure.Length != 12345 {
		t.Fatalf("enclosure эпизода 1: %+v", second.Enclosure)
	}
	if second.Episode != "1" || second.Duration != "1872" {
		t.Fatalf("эпизод 1: episode=%q duration=%q", second.Episode, second.Duration)
	}
}

func TestVideoFeed(t *testing.T) {
	env := newTestEnv(t)
	feed := fetchFeed(t, env, "/feed-video.xml")
	// только эпизоды 3 (mkv) и 2 (mp4), новые сверху
	if len(feed.Channel.Items) != 2 {
		t.Fatalf("items = %d, хотели 2", len(feed.Channel.Items))
	}
	if feed.Channel.Items[0].Episode != "3" || feed.Channel.Items[1].Episode != "2" {
		t.Fatalf("порядок/номера: %q, %q", feed.Channel.Items[0].Episode, feed.Channel.Items[1].Episode)
	}
	first := feed.Channel.Items[0]
	wantGUID := testBaseURL + "/video/3"
	wantURL := testBaseURL + "/video/3/003-tolko-video.mkv"
	if first.GUID.Value != wantGUID || first.Enclosure.URL != wantURL {
		t.Fatalf("guid/enclosure: %+v / %+v", first.GUID, first.Enclosure)
	}
	if first.Enclosure.Type != "video/x-matroska" || first.Enclosure.Length != 7000 {
		t.Fatalf("enclosure эпизода 3: %+v", first.Enclosure)
	}
	second := feed.Channel.Items[1]
	if second.Enclosure.Type != "video/mp4" || second.Enclosure.Length != 5000 ||
		second.GUID.Value != testBaseURL+"/video/2" ||
		second.Enclosure.URL != testBaseURL+"/video/2/002-vtoroy.mp4" {
		t.Fatalf("эпизод 2: %+v / %+v", second.GUID, second.Enclosure)
	}
	for _, it := range feed.Channel.Items {
		if strings.Contains(it.GUID.Value, "/audio/") {
			t.Fatalf("в видео-фиде аудио-guid: %q", it.GUID.Value)
		}
	}
}

// TestFeedDescriptionHTML — description фида содержит HTML, отрендеренный из
// markdown-исходника, а не саму markdown-разметку.
func TestFeedDescriptionHTML(t *testing.T) {
	env := newTestEnv(t)
	writeEpisode(t, env.dir, "9", `title: Markdown
published_at: 2026-10-25
audio:
  file: zapis.mp3
`, map[string]string{
		"zapis.mp3":      "x",
		"description.md": "**важно** и [гость](https://example.com)",
	})
	env.do(t, "POST", "/admin/rescan", "curl/8", nil, nil)

	feed := fetchFeed(t, env, "/feed.xml")
	it := feed.Channel.Items[0]
	if it.Episode != "9" {
		t.Fatalf("первый item — эпизод %q, хотели 9", it.Episode)
	}
	for _, want := range []string{"<p>", "<strong>важно</strong>", `href="https://example.com"`} {
		if !strings.Contains(it.Description, want) {
			t.Fatalf("в description нет %q: %q", want, it.Description)
		}
	}
	if strings.Contains(it.Description, "**важно**") {
		t.Fatalf("markdown не отрендерен: %q", it.Description)
	}
}

// TestFeedCacheUntilRescan — кэш фидов сбрасывается ресканом.
func TestFeedCacheUntilRescan(t *testing.T) {
	env := newTestEnv(t)
	before := env.do(t, "GET", "/feed.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	beforeVideo := env.do(t, "GET", "/feed-video.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	writeEpisode(t, env.dir, "4", `title: Novyy
published_at: 2026-10-25
audio:
  file: zapis.mp3
video:
  file: efir.mp4
`, map[string]string{"zapis.mp3": "C", "efir.mp4": "D"})
	mid := env.do(t, "GET", "/feed.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	if !bytes.Equal(before, mid) {
		t.Fatal("аудио-фид пересобрался без рескана — кэш не работает")
	}
	env.do(t, "POST", "/admin/rescan", "curl/8", nil, nil)
	after := env.do(t, "GET", "/feed.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	afterVideo := env.do(t, "GET", "/feed-video.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	if bytes.Equal(before, after) || bytes.Equal(beforeVideo, afterVideo) {
		t.Fatal("фиды не изменились после рескана")
	}
	if !bytes.Contains(after, []byte("/audio/4")) {
		t.Fatal("нового эпизода нет в аудио-фиде")
	}
	if !bytes.Contains(afterVideo, []byte("/video/4")) {
		t.Fatal("нового эпизода нет в видео-фиде")
	}
}

// TestFeedEmpty — пустой канал: валидный RSS без items в обоих фидах.
func TestFeedEmpty(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{BaseURL: testBaseURL, PodcastDir: dir}, ch, st)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/feed.xml", "/feed-video.xml"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("User-Agent", "PodcastApp/1.0")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var feed rssFeed
		if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
			t.Fatalf("%s: пустой фид невалиден: %v", path, err)
		}
		if len(feed.Channel.Items) != 0 || feed.Channel.Title != "Heavy Tech" {
			t.Fatalf("%s: канал: %+v", path, feed.Channel)
		}
	}
}

// TestAPIYouTube — поле youtube отдаёт ссылку эпизода и пустую строку без неё.
func TestAPIYouTube(t *testing.T) {
	env := newTestEnv(t)
	writeEpisode(t, env.dir, "2", `title: Vtoroy
published_at: 2026-10-15
youtube: https://youtu.be/abc123
audio:
  file: zapis.mp3
video:
  file: efir.mp4
`, map[string]string{
		"zapis.mp3":      strings.Repeat("B", 20000),
		"efir.mp4":       strings.Repeat("C", 5000),
		"description.md": "**важно** и [гость](https://example.com)",
	})
	if rec := env.do(t, "POST", "/admin/rescan", "curl/8", nil, nil); rec.Code != 200 {
		t.Fatalf("рескан: код %d", rec.Code)
	}

	rec := env.do(t, "GET", "/api/episodes", "curl/8", nil, nil)
	var eps []apiEpisode
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatalf("json: %v", err)
	}
	byNumber := map[int64]apiEpisode{}
	for _, e := range eps {
		byNumber[e.Number] = e
	}
	if got := byNumber[2].YouTube; got != "https://youtu.be/abc123" {
		t.Fatalf("youtube эпизода 2: %q", got)
	}
	if got := byNumber[1].YouTube; got != "" {
		t.Fatalf("youtube эпизода 1: %q", got)
	}
	// Описание в API — markdown-исходник, без HTML.
	if got := byNumber[2].Description; got != "**важно** и [гость](https://example.com)" {
		t.Fatalf("описание эпизода 2: %q", got)
	}
	if byNumber[3].Description != "" {
		t.Fatalf("описание эпизода 3: %q", byNumber[3].Description)
	}
}

// --- 4.2 JSON API ---

func TestAPIEpisodes(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/api/episodes", "curl/8", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type %q", ct)
	}
	var eps []apiEpisode
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(eps) != 3 {
		t.Fatalf("эпизодов %d, хотели 3", len(eps))
	}
	if eps[0].Number != 3 || eps[1].Number != 2 || eps[2].Number != 1 {
		t.Fatalf("порядок: %d, %d, %d", eps[0].Number, eps[1].Number, eps[2].Number)
	}
	e := eps[2]
	if e.Title != "Первый выпуск" || e.Description != "О чём этот подкаст" {
		t.Fatalf("метаданные: %+v", e)
	}
	if e.YouTube != "" {
		t.Fatalf("youtube эпизода без ссылки: %q", e.YouTube)
	}
	if e.PublishedAt != "2026-10-01" {
		t.Fatalf("published_at %q", e.PublishedAt)
	}
	if len(e.Media) != 1 {
		t.Fatalf("media: %+v", e.Media)
	}
	m := e.Media[0]
	if m.Kind != KindAudio || m.URL != testBaseURL+"/audio/1/001-pervyy-vypusk.aac" || m.Size != 12345 || m.Duration != 1872 {
		t.Fatalf("media эпизода 1: %+v", m)
	}
	// эпизод 2: оба носителя, разбивка счётчиков присутствует
	e2 := eps[1]
	if len(e2.Media) != 2 {
		t.Fatalf("media эпизода 2: %+v", e2.Media)
	}
	if e2.Media[0].Kind != KindAudio || e2.Media[1].Kind != KindVideo {
		t.Fatalf("порядок media: %+v", e2.Media)
	}
	if e2.Media[1].URL != testBaseURL+"/video/2/002-vtoroy.mp4" || e2.Media[1].Size != 5000 {
		t.Fatalf("видео в API: %+v", e2.Media[1])
	}
	if e2.Downloads.Audio != 0 || e2.Downloads.Video != 0 || e2.Downloads.Total != 0 || e2.Plays != 0 {
		t.Fatalf("счётчики эпизода 2: %+v", e2)
	}
}

// --- 4.1 HTML ---

func TestHomeShowsEpisode(t *testing.T) {
	env := newTestEnv(t)
	// markdown-описание и YouTube-ссылка у эпизода 2.
	writeEpisode(t, env.dir, "2", `title: Vtoroy
published_at: 2026-10-15
youtube: https://youtu.be/vtoroy
audio:
  file: zapis.mp3
video:
  file: efir.mp4
`, map[string]string{
		"zapis.mp3":      strings.Repeat("B", 20000),
		"efir.mp4":       strings.Repeat("C", 5000),
		"description.md": "**важно** и [гость](https://example.com)\n\n- пункт",
	})
	if rec := env.do(t, "POST", "/admin/rescan", "curl/8", nil, nil); rec.Code != 200 {
		t.Fatalf("рескан: код %d", rec.Code)
	}
	rec := env.do(t, "GET", "/", "Mozilla/5.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	html := rec.Body.String()
	for _, want := range []string{
		"Heavy Tech",
		"Первый выпуск",
		"О чём этот подкаст",
		"Выпуск №1",
		"Выпуск №2",
		"Выпуск №3",
		`data-episode="1"`, // плеер и кнопка «последний выпуск» работают по номеру
		`data-duration="1872"`,
		"1 октября 2026", // дата словами
		`<button class="play js-play"`,
		`class="track js-track"`,
		`class="btn js-copy-rss"`,
		`js-latest" data-episode="2"`, // последний выпуск с аудио
		"/feed.xml",
		"/feed-video.xml",
		`href="` + testBaseURL + `/audio/1/001-pervyy-vypusk.aac"`,
		`src="` + testBaseURL + `/audio/1/001-pervyy-vypusk.aac"`,
		`href="` + testBaseURL + `/video/2/002-vtoroy.mp4"`,
		`href="` + testBaseURL + `/video/3/003-tolko-video.mkv"`,
		`download>Скачать Аудио`,
		"12,1 КБ",
		"19,5 КБ",
		"Скачиваний и прослушиваний: 0",
		"<strong>важно</strong>",
		`href="https://example.com"`,
		"Смотреть на YouTube",
		`href="https://youtu.be/vtoroy" target="_blank" rel="noopener noreferrer"`,
		"31:12", // 1872 c
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("в HTML нет %q", want)
		}
	}
	if strings.Contains(html, `<audio controls`) || strings.Contains(html, ` controls`) {
		t.Fatal("на странице есть нативный controls у аудио")
	}
	// плеер только у эпизодов 1 и 2 (у эпизода 3 только видео)
	if n := strings.Count(html, `class="play js-play"`); n != 2 {
		t.Fatalf("плееров %d, хотели 2", n)
	}
	// действие YouTube только у эпизода со ссылкой
	if n := strings.Count(html, "Смотреть на YouTube"); n != 1 {
		t.Fatalf("кнопок YouTube %d, хотели 1", n)
	}
	// новые сверху
	if strings.Index(html, "Tolko video") > strings.Index(html, "Vtoroy") ||
		strings.Index(html, "Vtoroy") > strings.Index(html, "Первый выпуск") {
		t.Fatal("эпизоды не отсортированы от новых к старым")
	}
}

func TestHomeEmpty(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	ch, _ := LoadChannel(dir)
	srv, err := NewServer(Config{BaseURL: testBaseURL}, ch, st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	html := rec.Body.String()
	if !strings.Contains(html, "Выпуски скоро появятся") || !strings.Contains(html, "/feed.xml") {
		t.Fatal("нет заглушки пустого списка")
	}
	if strings.Contains(html, `<button class="play js-play"`) {
		t.Fatal("в пустом состоянии не должно быть плееров")
	}
}

// --- данные страницы ---

func TestRussianDate(t *testing.T) {
	tests := []struct {
		when time.Time
		want string
	}{
		{time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "2 октября 2026"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "1 января 2026"},
		{time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC), "31 декабря 2026"},
		{time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), "15 марта 2026"},
	}
	for _, tt := range tests {
		if got := russianDate(tt.when); got != tt.want {
			t.Errorf("russianDate(%v) = %q, хотели %q", tt.when, got, tt.want)
		}
	}
}

func TestBuildPageData(t *testing.T) {
	videoOnly := EpisodeView{
		Episode: Episode{
			Number:      3,
			Title:       "Только видео",
			PublishedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
			Files:       []EpisodeFile{{Kind: KindVideo, FilePath: "/p/3/efir.mkv", Size: 7000, Mime: "video/mp4"}},
		},
	}
	second := EpisodeView{
		Episode: Episode{
			Number:      2,
			Title:       "Vtoroy",
			Description: "**важно**",
			YouTube:     "https://youtu.be/vtoroy",
			PublishedAt: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
			Files: []EpisodeFile{
				{Kind: KindAudio, FilePath: "/p/2/zapis.mp3", Size: 20000, Mime: "audio/mpeg"},
				{Kind: KindVideo, FilePath: "/p/2/efir.mp4", Size: 5000, Mime: "video/mp4"},
			},
		},
		Downloads: DownloadCounts{Audio: 120, Video: 30, Total: 150},
		Plays:     5,
	}
	first := EpisodeView{
		Episode: Episode{
			Number:      1,
			Title:       "Первый выпуск",
			PublishedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			Files:       []EpisodeFile{{Kind: KindAudio, FilePath: "/p/1/zapis.aac", Size: 12345, Mime: "audio/aac", DurationSeconds: 1872}},
		},
	}
	views := []EpisodeView{videoOnly, second, first}
	ch := Channel{Title: "Heavy Tech", Description: "О тяжёлом", Image: "https://pod.example.com/cover.jpg"}
	data := buildPageData(ch, testBaseURL, views)

	if data.Title != "Heavy Tech" || data.Description != "О тяжёлом" || !data.HasImage || data.Image != ch.Image {
		t.Fatalf("метаданные канала: %+v", data)
	}
	if data.FeedURL != testBaseURL+"/feed.xml" || data.VideoFeedURL != testBaseURL+"/feed-video.xml" {
		t.Fatalf("FeedURL %q VideoFeedURL %q", data.FeedURL, data.VideoFeedURL)
	}
	// «последний выпуск» — самый новый эпизод С АУДИО, а не самый новый вообще
	if data.LatestNumber != 2 {
		t.Fatalf("LatestNumber %d, хотели 2", data.LatestNumber)
	}
	if len(data.Episodes) != 3 {
		t.Fatalf("эпизодов %d, хотели 3", len(data.Episodes))
	}
	newest, mid, oldest := data.Episodes[0], data.Episodes[1], data.Episodes[2]
	if newest.Number != 3 || newest.HasAudio || len(newest.Media) != 1 || newest.Media[0].Kind != KindVideo {
		t.Fatalf("видео-выпуск: %+v", newest)
	}
	if newest.Media[0].URL != testBaseURL+"/video/3/003-tolko-video.mkv" {
		t.Fatalf("видео-ссылка: %q", newest.Media[0].URL)
	}
	if mid.Number != 2 || !mid.HasAudio || len(mid.Media) != 2 {
		t.Fatalf("эпизод 2: %+v", mid)
	}
	if mid.Downloads != 150 || mid.DownloadsAudio != 120 || mid.DownloadsVideo != 30 || mid.Plays != 5 {
		t.Fatalf("счётчики эпизода 2: %+v", mid)
	}
	if mid.Engagement != 155 {
		t.Fatalf("единый счётчик эпизода 2: %d, хотели 155", mid.Engagement)
	}
	if !strings.Contains(string(mid.Description), "<strong>важно</strong>") {
		t.Fatalf("описание не отрендерено: %q", mid.Description)
	}
	if mid.YouTube != "https://youtu.be/vtoroy" {
		t.Fatalf("youtube эпизода 2: %q", mid.YouTube)
	}
	if mid.Media[0].URL != testBaseURL+"/audio/2/002-vtoroy.mp3" ||
		mid.Media[1].URL != testBaseURL+"/video/2/002-vtoroy.mp4" {
		t.Fatalf("ссылки на носители: %+v", mid.Media)
	}
	if mid.AudioURL != testBaseURL+"/audio/2/002-vtoroy.mp3" {
		t.Fatalf("AudioURL: %q", mid.AudioURL)
	}
	if oldest.DateLabel != "1 октября 2026" || oldest.AudioDurationSecs != 1872 || oldest.AudioDurationLabel != "31:12" {
		t.Fatalf("эпизод 1: %+v", oldest)
	}

	// без обложки HasImage=false, без эпизодов LatestNumber 0
	data = buildPageData(Channel{Title: "X"}, testBaseURL, nil)
	if data.HasImage || data.Image != "" || data.LatestNumber != 0 {
		t.Fatalf("пустой канал собран неверно: %+v", data)
	}
}

// writeChannelWithImage — канал с заданной обложкой для тестов героя и OG.
func writeChannelWithImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const ch = `title: Heavy Tech
description: Тестовый подкаст о тяжёлом
author: Сергей
email: sergey@example.com
category: Technology
explicit: false
image: https://cdn.example.com/cover.jpg
`
	if err := os.WriteFile(filepath.Join(dir, "podcast.yaml"), []byte(ch), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// serverOn — сервер поверх готовой папки и стора.
func serverOn(t *testing.T, dir string, st *Store) *Server {
	t.Helper()
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{BaseURL: testBaseURL, PodcastDir: dir}, ch, st)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func homeHTML(t *testing.T, srv *Server) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	return rec.Body.String()
}

func TestHomeOpenGraphWithImage(t *testing.T) {
	dir := writeChannelWithImage(t)
	st := newTestStore(t)
	writeEpisode(t, dir, "1", metaAudio("Эпизод", "2026-10-01", "z.m4a"), map[string]string{"z.m4a": "data"})
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	html := homeHTML(t, serverOn(t, dir, st))
	for _, want := range []string{
		`property="og:title" content="Heavy Tech"`,
		`property="og:description" content="Тестовый подкаст о тяжёлом"`,
		`property="og:type" content="website"`,
		`property="og:url" content="` + testBaseURL + `"`,
		`property="og:image" content="https://cdn.example.com/cover.jpg"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("в HTML нет %q", want)
		}
	}
}

func TestHomeOpenGraphWithoutImage(t *testing.T) {
	env := newTestEnv(t)
	html := homeHTML(t, env.server)
	if strings.Contains(html, "og:image") {
		t.Fatal("og:image не должен выводиться без обложки")
	}
	for _, want := range []string{
		`property="og:title" content="Heavy Tech"`,
		`property="og:type" content="website"`,
		`property="og:url" content="` + testBaseURL + `"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("в HTML нет %q", want)
		}
	}
}

func TestHomeOpenGraphEscaping(t *testing.T) {
	dir := t.TempDir()
	const ch = `title: Heavy "Tech" & <Co>
description: О тяжёлом "и светлом"
`
	if err := os.WriteFile(filepath.Join(dir, "podcast.yaml"), []byte(ch), 0o644); err != nil {
		t.Fatal(err)
	}
	html := homeHTML(t, serverOn(t, dir, newTestStore(t)))
	if !strings.Contains(html, `content="Heavy &#34;Tech&#34; &amp; &lt;Co&gt;"`) {
		t.Fatal("og:title не экранирован как HTML-атрибут")
	}
}

func TestHomeHeroBlock(t *testing.T) {
	dir := writeChannelWithImage(t)
	st := newTestStore(t)
	writeEpisode(t, dir, "1", metaAudio("Первый", "2026-10-01", "a.m4a"), map[string]string{"a.m4a": "aaa"})
	writeEpisode(t, dir, "2", metaAudio("Vtoroy", "2026-10-15", "b.m4a"), map[string]string{"b.m4a": "bbb"})
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	html := homeHTML(t, serverOn(t, dir, st))
	for _, want := range []string{
		`class="cover" src="https://cdn.example.com/cover.jpg"`, // обложка из podcast.yaml
		`js-latest" data-episode="2"`,                           // кнопка последнего выпуска
		`<h1>Heavy Tech</h1>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("в герое нет %q", want)
		}
	}
}

func TestHomeCopyRSS(t *testing.T) {
	env := newTestEnv(t)
	html := homeHTML(t, env.server)
	if !strings.Contains(html, `class="btn js-copy-rss" data-url="`+testBaseURL+`/feed.xml"`) {
		t.Fatal("нет элемента копирования RSS с абсолютным URL фида")
	}
	// и в пустом состоянии копирование тоже доступно
	dir := writePodcastDir(t)
	html = homeHTML(t, serverOn(t, dir, newTestStore(t)))
	if !strings.Contains(html, `js-copy-rss" data-url="`+testBaseURL+`/feed.xml"`) {
		t.Fatal("в пустом состоянии нет копирования RSS")
	}
}

// --- метрики скачиваний ---

func TestDownloadsDedupPerDay(t *testing.T) {
	env := newTestEnv(t)
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	now := base
	env.server.now = func() time.Time { return now }
	h := map[string]string{"X-Forwarded-For": "203.0.113.9"}

	// одна докачка из трёх Range-запросов за сутки = 1 скачивание
	env.do(t, "GET", "/audio/1", "PodcastApp/1.0", nil, h)
	env.do(t, "GET", "/audio/1", "PodcastApp/1.0", nil,
		map[string]string{"Range": "bytes=100-199", "X-Forwarded-For": "203.0.113.9"})
	env.do(t, "GET", "/audio/1", "PodcastApp/1.0", nil,
		map[string]string{"Range": "bytes=200-299", "X-Forwarded-For": "203.0.113.9"})
	if dl, _ := env.store.CountDownloads(1); dl.Total != 1 {
		t.Fatalf("скачиваний %d, хотели 1", dl.Total)
	}

	// тот же клиент на следующие сутки — ещё 1
	now = base.Add(24 * time.Hour)
	env.do(t, "GET", "/audio/1", "PodcastApp/1.0", nil, h)
	if dl, _ := env.store.CountDownloads(1); dl.Total != 2 {
		t.Fatalf("скачиваний %d, хотели 2", dl.Total)
	}

	// другой клиент те же сутки — отдельное скачивание
	now = base
	env.do(t, "GET", "/audio/1", "OtherApp/2.0", nil, h)
	if dl, _ := env.store.CountDownloads(1); dl.Total != 3 {
		t.Fatalf("скачиваний %d, хотели 3", dl.Total)
	}
}

// TestDownloadsSeparateKinds — один клиент качает аудио и видео: счётчики независимы.
func TestDownloadsSeparateKinds(t *testing.T) {
	env := newTestEnv(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	env.server.now = func() time.Time { return now }
	h := map[string]string{"X-Forwarded-For": "203.0.113.9"}

	env.do(t, "GET", "/audio/2", "PodcastApp/1.0", nil, h)
	env.do(t, "GET", "/video/2", "PodcastApp/1.0", nil, h)
	dl, err := env.store.CountDownloads(2)
	if err != nil {
		t.Fatal(err)
	}
	if dl.Audio != 1 || dl.Video != 1 || dl.Total != 2 {
		t.Fatalf("скачивания %+v, хотели audio=1 video=1 total=2", dl)
	}
}

func TestDownloadsExclusions(t *testing.T) {
	env := newTestEnv(t)
	count := func() int64 {
		dl, _ := env.store.CountDownloads(1)
		return dl.Total
	}

	env.do(t, "HEAD", "/audio/1", "PodcastApp/1.0", nil, nil)
	if count() != 0 {
		t.Fatal("HEAD не должен считаться")
	}
	env.do(t, "GET", "/audio/1", "Googlebot/2.1", nil, nil)
	if count() != 0 {
		t.Fatal("бот не должен считаться")
	}
	env.do(t, "GET", "/audio/1", "site-monitor/1", nil, nil)
	if count() != 0 {
		t.Fatal("монитор не должен считаться")
	}
	req := httptest.NewRequest("GET", "/audio/1", nil) // пустой UA
	rec := httptest.NewRecorder()
	env.server.ServeHTTP(rec, req)
	if count() != 0 {
		t.Fatal("пустой User-Agent не должен считаться")
	}
	env.do(t, "GET", "/audio/1", "PodcastApp/1.0", nil, nil)
	if count() != 1 {
		t.Fatalf("обычный GET должен считаться, получили %d", count())
	}
}

// TestNoRawPII — сырые IP и UA не хранятся, только сольтированные хеши.
func TestNoRawPII(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, "GET", "/audio/1", "VerySecretAgent/9.9", nil,
		map[string]string{"X-Forwarded-For": "198.51.100.77"})
	env.do(t, "POST", "/track/play", "Mozilla/5.0", strings.NewReader(`{"episode":1,"progress":42}`),
		map[string]string{"Content-Type": "application/json"})

	downloads, plays, err := env.store.DumpEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 1 || len(plays) != 1 {
		t.Fatalf("события: %d/%d", len(downloads), len(plays))
	}
	for _, row := range downloads {
		for _, cell := range row {
			if strings.Contains(cell, "198.51.100.77") || strings.Contains(cell, "VerySecretAgent") {
				t.Fatalf("сырые данные в download_events: %v", row)
			}
		}
	}
	// хеши 32-байтные (64 hex-символа) — сольтированы, не равны сырому значению
	if len(downloads[0][2]) != 64 || len(downloads[0][3]) != 64 {
		t.Fatalf("хеши неожиданной длины: %v", downloads[0])
	}
	if downloads[0][0] != "1" || downloads[0][1] != KindAudio {
		t.Fatalf("эпизод/вид в событии: %v", downloads[0])
	}
}

// --- play-бикон ---

func TestPlayBeacon(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"episode":1,"progress":10}`),
		map[string]string{"Content-Type": "application/json"})
	if rec.Code != 204 {
		t.Fatalf("код %d, хотели 204", rec.Code)
	}
	if plays, _ := env.store.CountPlays(1); plays != 1 {
		t.Fatalf("прослушиваний %d, хотели 1", plays)
	}

	// неизвестный номер — 4xx, событие не записано
	rec = env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"episode":999}`),
		map[string]string{"Content-Type": "application/json"})
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("код %d, хотели 4xx", rec.Code)
	}
	if plays, _ := env.store.CountPlays(1); plays != 1 {
		t.Fatalf("прослушиваний %d, хотели 1", plays)
	}

	// битый JSON — 400
	if rec = env.do(t, "POST", "/track/play", "Mozilla/5.0", strings.NewReader(`не-json`), nil); rec.Code != 400 {
		t.Fatalf("код %d, хотели 400", rec.Code)
	}
	// нет номера — 400
	if rec = env.do(t, "POST", "/track/play", "Mozilla/5.0", strings.NewReader(`{"slug":"1"}`), nil); rec.Code != 400 {
		t.Fatalf("код %d, хотели 400", rec.Code)
	}
	// прогресс вне диапазона — 400
	rec = env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"episode":1,"progress":150}`), nil)
	if rec.Code != 400 {
		t.Fatalf("код %d, хотели 400", rec.Code)
	}
}

// TestDownloadDedupAcrossNamedAndNumberedURLs — один клиент в те же сутки,
// запросив именованный и номерной URL одного носителя, даёт одно событие.
func TestDownloadDedupAcrossNamedAndNumberedURLs(t *testing.T) {
	env := newTestEnv(t)
	env.server.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	headers := map[string]string{"X-Forwarded-For": "203.0.113.9"}
	for _, path := range []string{"/audio/1/001-pervyy-vypusk.aac", "/audio/1"} {
		if rec := env.do(t, "GET", path, "App/1", nil, headers); rec.Code != 200 {
			t.Fatalf("%s: код %d", path, rec.Code)
		}
	}
	counts, err := env.store.CountDownloads(1)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Audio != 1 || counts.Total != 1 {
		t.Fatalf("скачивания: %+v, хотели ровно 1", counts)
	}
}

// --- согласованность счётчиков в HTML и JSON ---

func TestCountersConsistentHTMLAndJSON(t *testing.T) {
	env := newTestEnv(t)
	h := map[string]string{"X-Forwarded-For": "203.0.113.1"}
	// эпизод 2: 3 скачивания аудио (два клиента в разные сутки + один повторный),
	// 1 скачивание видео и 1 прослушивание
	env.server.now = func() time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) }
	env.do(t, "GET", "/audio/2", "App/1", nil, h)
	env.server.now = func() time.Time { return time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC) }
	env.do(t, "GET", "/audio/2", "App/1", nil, h)
	env.do(t, "GET", "/audio/2", "App/2", nil, h)
	env.do(t, "GET", "/video/2", "App/1", nil, h)
	env.server.now = time.Now
	env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"episode":2}`),
		map[string]string{"Content-Type": "application/json"})

	html := env.do(t, "GET", "/", "Mozilla/5.0", nil, nil).Body.String()
	// сайт показывает единый счётчик: 4 скачивания + 1 прослушивание = 5
	if !strings.Contains(html, "Скачиваний и прослушиваний: 5") {
		t.Fatal("HTML не показывает единый счётчик 5 (4 скачивания + 1 прослушивание)")
	}
	var eps []apiEpisode
	rec := env.do(t, "GET", "/api/episodes", "curl/8", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatal(err)
	}
	for _, e := range eps {
		if e.Number == 2 {
			if e.Downloads.Audio != 3 || e.Downloads.Video != 1 || e.Downloads.Total != 4 || e.Plays != 1 {
				t.Fatalf("API: %+v", e.Downloads)
			}
			return
		}
	}
	t.Fatal("эпизод 2 не найден в API")
}

// TestAllPublic — вся публичная поверхность без авторизации.
func TestAllPublic(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct{ method, target string }{
		{"GET", "/"},
		{"GET", "/feed.xml"},
		{"GET", "/feed-video.xml"},
		{"GET", "/api/episodes"},
		{"GET", "/audio/1"},
		{"GET", "/video/2"},
	} {
		req := httptest.NewRequest(tc.method, tc.target, nil)
		rec := httptest.NewRecorder()
		env.server.ServeHTTP(rec, req)
		if rec.Code >= 400 {
			t.Fatalf("%s %s -> %d", tc.method, tc.target, rec.Code)
		}
	}
}
