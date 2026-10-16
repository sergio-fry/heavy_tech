package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http/httptest"
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

// newTestEnv — папка с каналом и двумя эпизодами, сервер с фиксированным временем.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := writePodcastDir(t)
	st := newTestStore(t)
	// 12345 байт; тихий контент не нужен — сервис не парсит MP3
	writeEpisode(t, dir, "2026-10-01", "pervyy-vypusk", strings.Repeat("A", 12345), &sidecar{
		Description: "О чём этот подкаст", Duration: 1872,
	})
	writeEpisode(t, dir, "2026-10-15", "vtoroy", strings.Repeat("B", 20000), nil)
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

// --- 3.1 аудио-стриминг ---

func TestAudioFull(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("Content-Type %q", ct)
	}
	if got := rec.Body.Len(); got != 12345 {
		t.Fatalf("тело %d байт, хотели 12345", got)
	}
}

func TestAudioRange(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil,
		map[string]string{"Range": "bytes=1000-1999"})
	if rec.Code != 206 {
		t.Fatalf("код %d, хотели 206", rec.Code)
	}
	cr := rec.Header().Get("Content-Range")
	if cr != "bytes 1000-1999/12345" {
		t.Fatalf("Content-Range %q", cr)
	}
	if rec.Body.Len() != 1000 {
		t.Fatalf("тело %d байт, хотели 1000", rec.Body.Len())
	}
}

func TestAudioUnknownSlug(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/audio/net-takogo", "PodcastApp/1.0", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("код %d, хотели 404", rec.Code)
	}
}

// --- 3.2 фид ---

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
	Duration string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
}

func TestFeed(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/feed.xml", "PodcastApp/1.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte(xml.Header)) {
		t.Fatal("нет XML-заголовка")
	}
	// xmlns-объявление Go-декодер поглощает и в поля не отдаёт — проверяем в сырых байтах
	if !bytes.Contains(body, []byte(`xmlns:itunes="`+itunesNS+`"`)) {
		t.Fatal("нет объявления itunes-namespace")
	}
	var feed nsFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		t.Fatalf("фид не разбирается: %v", err)
	}
	if feed.Version != "2.0" {
		t.Fatalf("rss@version=%q", feed.Version)
	}
	if feed.Channel.Title != "Heavy Tech" || feed.Channel.Author != "Сергей" {
		t.Fatalf("метаданные канала: %+v", feed.Channel)
	}
	if feed.Channel.Explicit != "false" {
		t.Fatalf("itunes:explicit = %q", feed.Channel.Explicit)
	}
	if len(feed.Channel.Items) != 2 {
		t.Fatalf("items = %d, хотели 2", len(feed.Channel.Items))
	}
	// новые сверху
	if feed.Channel.Items[0].Title != "Vtoroy" {
		t.Fatalf("первый item %q, хотели «Vtoroy»", feed.Channel.Items[0].Title)
	}
	first := feed.Channel.Items[0]
	wantURL := testBaseURL + "/audio/vtoroy"
	if first.GUID.Value != wantURL || first.GUID.IsPermaLink != "true" {
		t.Fatalf("guid %+v", first.GUID)
	}
	if first.Enclosure.URL != wantURL || first.Enclosure.Length != 20000 || first.Enclosure.Type != "audio/mpeg" {
		t.Fatalf("enclosure %+v", first.Enclosure)
	}
	if _, err := time.Parse(time.RFC1123Z, first.PubDate); err != nil {
		t.Fatalf("pubDate не RFC1123: %q (%v)", first.PubDate, err)
	}
	if first.Duration == "" {
		t.Fatal("itunes:duration пуст у эпизода без sidecar — ожидалась оценка")
	}
	// у эпизода со sidecar длительность из sidecar (1872)
	if feed.Channel.Items[1].Duration != "1872" {
		t.Fatalf("itunes:duration = %q, хотели 1872", feed.Channel.Items[1].Duration)
	}
}

// TestFeedCacheUntilRescan — кэш фида сбрасывается ресканом.
func TestFeedCacheUntilRescan(t *testing.T) {
	env := newTestEnv(t)
	before := env.do(t, "GET", "/feed.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	writeEpisode(t, env.dir, "2026-10-20", "tretiy", "C", nil)
	mid := env.do(t, "GET", "/feed.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	if !bytes.Equal(before, mid) {
		t.Fatal("фид пересобрался без рескана — кэш не работает")
	}
	env.do(t, "POST", "/admin/rescan", "curl/8", nil, nil)
	after := env.do(t, "GET", "/feed.xml", "PodcastApp/1.0", nil, nil).Body.Bytes()
	if bytes.Equal(before, after) {
		t.Fatal("фид не изменился после рескана")
	}
	if !bytes.Contains(after, []byte("/audio/tretiy")) {
		t.Fatal("нового эпизода нет в фиде")
	}
}

// TestFeedEmpty — пустой канал: валидный RSS без items.
func TestFeedEmpty(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{BaseURL: testBaseURL}, ch, st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/feed.xml", nil)
	req.Header.Set("User-Agent", "PodcastApp/1.0")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var feed rssFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("пустой фид невалиден: %v", err)
	}
	if len(feed.Channel.Items) != 0 || feed.Channel.Title != "Heavy Tech" {
		t.Fatalf("канал: %+v", feed.Channel)
	}
}

// --- 3.3 JSON API ---

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
	if len(eps) != 2 {
		t.Fatalf("эпизодов %d", len(eps))
	}
	if eps[0].Slug != "vtoroy" || eps[1].Slug != "pervyy-vypusk" {
		t.Fatalf("порядок: %s, %s", eps[0].Slug, eps[1].Slug)
	}
	e := eps[1]
	if e.Title != "Pervyy Vypusk" || e.Description != "О чём этот подкаст" {
		t.Fatalf("метаданные: %+v", e)
	}
	if e.PublishedAt != "2026-10-01" || e.DurationSeconds != 1872 || e.SizeBytes != 12345 {
		t.Fatalf("поля: %+v", e)
	}
	if e.AudioURL != testBaseURL+"/audio/pervyy-vypusk" {
		t.Fatalf("audio_url %q", e.AudioURL)
	}
}

// --- 3.4 HTML ---

func TestHomeShowsEpisode(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "GET", "/", "Mozilla/5.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	html := rec.Body.String()
	for _, want := range []string{
		"Heavy Tech",
		"Pervyy Vypusk",
		"О чём этот подкаст",
		`data-slug="pervyy-vypusk"`,
		`<audio controls preload="none"`,
		"/feed.xml",
		"Скачиваний: 0",
		"31:12", // 1872 c
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("в HTML нет %q", want)
		}
	}
	// новые сверху: vtoroy встречается раньше pervyy-vypusk
	if strings.Index(html, "Vtoroy") > strings.Index(html, "Pervyy Vypusk") {
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
	if !strings.Contains(html, "Эпизодов пока нет") || !strings.Contains(html, "/feed.xml") {
		t.Fatal("нет заглушки пустого списка")
	}
}

// --- 4.1 метрики скачиваний ---

func TestDownloadsDedupPerDay(t *testing.T) {
	env := newTestEnv(t)
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	now := base
	env.server.now = func() time.Time { return now }
	h := map[string]string{"X-Forwarded-For": "203.0.113.9"}

	// одна докачка из трёх Range-запросов за сутки = 1 скачивание
	env.do(t, "GET", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil, h)
	env.do(t, "GET", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil,
		map[string]string{"Range": "bytes=100-199", "X-Forwarded-For": "203.0.113.9"})
	env.do(t, "GET", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil,
		map[string]string{"Range": "bytes=200-299", "X-Forwarded-For": "203.0.113.9"})
	ep, _ := env.store.EpisodeBySlug("pervyy-vypusk")
	if dl, _ := env.store.CountDownloads(ep.ID); dl != 1 {
		t.Fatalf("скачиваний %d, хотели 1", dl)
	}

	// тот же клиент на следующие сутки — ещё 1
	now = base.Add(24 * time.Hour)
	env.do(t, "GET", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil, h)
	if dl, _ := env.store.CountDownloads(ep.ID); dl != 2 {
		t.Fatalf("скачиваний %d, хотели 2", dl)
	}

	// другой клиент те же сутки-1 — отдельное скачивание
	now = base
	env.do(t, "GET", "/audio/pervyy-vypusk", "OtherApp/2.0", nil, h)
	if dl, _ := env.store.CountDownloads(ep.ID); dl != 3 {
		t.Fatalf("скачиваний %d, хотели 3", dl)
	}
}

func TestDownloadsExclusions(t *testing.T) {
	env := newTestEnv(t)
	ep, _ := env.store.EpisodeBySlug("pervyy-vypusk")
	count := func() int64 {
		dl, _ := env.store.CountDownloads(ep.ID)
		return dl
	}

	env.do(t, "HEAD", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil, nil)
	if count() != 0 {
		t.Fatal("HEAD не должен считаться")
	}
	env.do(t, "GET", "/audio/pervyy-vypusk", "Googlebot/2.1", nil, nil)
	if count() != 0 {
		t.Fatal("бот не должен считаться")
	}
	env.do(t, "GET", "/audio/pervyy-vypusk", "site-monitor/1", nil, nil)
	if count() != 0 {
		t.Fatal("монитор не должен считаться")
	}
	req := httptest.NewRequest("GET", "/audio/pervyy-vypusk", nil) // пустой UA
	rec := httptest.NewRecorder()
	env.server.ServeHTTP(rec, req)
	if count() != 0 {
		t.Fatal("пустой User-Agent не должен считаться")
	}
	env.do(t, "GET", "/audio/pervyy-vypusk", "PodcastApp/1.0", nil, nil)
	if count() != 1 {
		t.Fatalf("обычный GET должен считаться, получили %d", count())
	}
}

// TestNoRawPII — сырые IP и UA не хранятся, только сольтированные хеши.
func TestNoRawPII(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, "GET", "/audio/pervyy-vypusk", "VerySecretAgent/9.9", nil,
		map[string]string{"X-Forwarded-For": "198.51.100.77"})
	env.do(t, "POST", "/track/play", "Mozilla/5.0", strings.NewReader(`{"slug":"pervyy-vypusk","progress":42}`),
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
	if len(downloads[0][1]) != 64 || len(downloads[0][2]) != 64 {
		t.Fatalf("хеши неожиданной длины: %v", downloads[0])
	}
}

// --- 4.2 play-бикон ---

func TestPlayBeacon(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"slug":"pervyy-vypusk","progress":10}`),
		map[string]string{"Content-Type": "application/json"})
	if rec.Code != 204 {
		t.Fatalf("код %d, хотели 204", rec.Code)
	}
	ep, _ := env.store.EpisodeBySlug("pervyy-vypusk")
	if plays, _ := env.store.CountPlays(ep.ID); plays != 1 {
		t.Fatalf("прослушиваний %d, хотели 1", plays)
	}

	// неизвестный слаг — 4xx, событие не записано
	rec = env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"slug":"net-takogo"}`),
		map[string]string{"Content-Type": "application/json"})
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("код %d, хотели 4xx", rec.Code)
	}
	if plays, _ := env.store.CountPlays(ep.ID); plays != 1 {
		t.Fatalf("прослушиваний %d, хотели 1", plays)
	}

	// битый JSON — 400
	rec = env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`не-json`), nil)
	if rec.Code != 400 {
		t.Fatalf("код %d, хотели 400", rec.Code)
	}

	// прогресс вне диапазона — 400
	rec = env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"slug":"pervyy-vypusk","progress":150}`), nil)
	if rec.Code != 400 {
		t.Fatalf("код %d, хотели 400", rec.Code)
	}
}

// --- 4.3 согласованность счётчиков в HTML и JSON ---

func TestCountersConsistentHTMLAndJSON(t *testing.T) {
	env := newTestEnv(t)
	h := map[string]string{"X-Forwarded-For": "203.0.113.1"}
	// 3 скачивания (два клиента в разные сутки + один повторный) и 1 прослушивание
	env.do(t, "GET", "/audio/pervyy-vypusk", "App/1", nil, h)
	env.server.now = func() time.Time { return time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC) }
	env.do(t, "GET", "/audio/pervyy-vypusk", "App/1", nil, h)
	env.do(t, "GET", "/audio/pervyy-vypusk", "App/2", nil, h)
	env.server.now = time.Now
	env.do(t, "POST", "/track/play", "Mozilla/5.0",
		strings.NewReader(`{"slug":"pervyy-vypusk"}`),
		map[string]string{"Content-Type": "application/json"})

	html := env.do(t, "GET", "/", "Mozilla/5.0", nil, nil).Body.String()
	if !strings.Contains(html, "Скачиваний: 3") || !strings.Contains(html, "Прослушиваний: 1") {
		t.Fatal("HTML не показывает 3 скачивания / 1 прослушивание")
	}
	var eps []apiEpisode
	rec := env.do(t, "GET", "/api/episodes", "curl/8", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatal(err)
	}
	for _, e := range eps {
		if e.Slug == "pervyy-vypusk" && (e.Downloads != 3 || e.Plays != 1) {
			t.Fatalf("API: downloads=%d plays=%d, хотели 3/1", e.Downloads, e.Plays)
		}
	}
}

// TestAllPublic — вся публичная поверхность без авторизации.
func TestAllPublic(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct{ method, target string }{
		{"GET", "/"},
		{"GET", "/feed.xml"},
		{"GET", "/api/episodes"},
		{"GET", "/audio/pervyy-vypusk"},
	} {
		req := httptest.NewRequest(tc.method, tc.target, nil)
		rec := httptest.NewRecorder()
		env.server.ServeHTTP(rec, req)
		if rec.Code >= 400 {
			t.Fatalf("%s %s -> %d", tc.method, tc.target, rec.Code)
		}
	}
}
