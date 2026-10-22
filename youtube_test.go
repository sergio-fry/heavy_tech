package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://youtu.be/abc123?t=30", "abc123", true},
		{"https://www.youtube.com/watch?v=abc123&list=PL1", "abc123", true},
		{"https://youtube.com/shorts/abc123", "abc123", true},
		{"https://www.youtube.com/embed/abc123", "abc123", true},
		{"https://m.youtube.com/live/abc123", "abc123", true},
		{"https://youtube.com/v/abc123", "abc123", true},
		{"https://example.com/video/abc123", "", false},
		{"https://youtube.com/watch", "", false},
		{"youtu.be/abc123", "", false}, // без схемы и хоста
		{"javascript:alert(1)", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := extractVideoID(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("extractVideoID(%q) = %q, %v; хотели %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseChapters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Chapter
	}{
		{
			name: "валидный список",
			in:   "0:00 Вступление\n12:30 Основная часть\n1:02:10 Итоги",
			want: []Chapter{{0, "Вступление"}, {750, "Основная часть"}, {3730, "Итоги"}},
		},
		{
			name: "маркеры и разделители",
			in:   "- 0:00 — Вступление\n- 1:00 - Первая тема\n- 2:00: Итоги",
			want: []Chapter{{0, "Вступление"}, {60, "Первая тема"}, {120, "Итоги"}},
		},
		{
			name: "две главы мало",
			in:   "0:00 Вступление\n12:30 Основная часть",
			want: nil,
		},
		{
			name: "первая не с нуля",
			in:   "0:10 Вступление\n12:30 Основная часть\n1:02:10 Итоги",
			want: nil,
		},
		{
			name: "пустые названия",
			in:   "0:00\n12:30\n1:02:10",
			want: nil,
		},
		{
			name: "время не возрастает",
			in:   "0:00 Вступление\n12:30 Основная часть\n10:00 Итоги",
			want: nil,
		},
		{
			name: "таймкод в абзаце не считается",
			in:   "Мы начали в 0:00 и говорили\n12:30 про всё\n1:02:10 закончили",
			want: nil,
		},
		{
			name: "описание без глав",
			in:   "Просто текст выпуска без таймкодов.",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseChapters(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("глав %d, хотели %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("глава %d: %+v, хотели %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// seedYouTubeEpisodes — эпизоды с ссылками YouTube без файлов (для синхронизатора).
func seedYouTubeEpisodes(t *testing.T, st *Store, count int) {
	t.Helper()
	found := make([]Episode, 0, count)
	for i := 1; i <= count; i++ {
		found = append(found, Episode{
			Number:      int64(i),
			Title:       fmt.Sprintf("Выпуск %d", i),
			YouTube:     fmt.Sprintf("https://youtu.be/vid%d", i),
			PublishedAt: time.Date(2026, 10, i%28+1, 0, 0, 0, 0, time.UTC),
		})
	}
	if _, _, _, err := st.Sync(found); err != nil {
		t.Fatal(err)
	}
}

// fakeYouTube — поддельный videos.list: отдаёт items для запрошенных id.
func fakeYouTube(t *testing.T, calls *int, maxBatch *int, status int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*calls++
		ids := strings.Split(r.URL.Query().Get("id"), ",")
		if len(ids) > *maxBatch {
			*maxBatch = len(ids)
		}
		mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			num, _ := strconv.Atoi(strings.TrimPrefix(id, "vid"))
			items = append(items, map[string]any{
				"id": id,
				"snippet": map[string]any{
					"description": "0:00 Вступление\n12:30 Основная часть\n1:02:10 Итоги",
				},
				"statistics": map[string]any{"viewCount": strconv.Itoa(num * 1000)},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
}

func TestYouTubeRefreshSavesViewsAndChapters(t *testing.T) {
	st := newTestStore(t)
	seedYouTubeEpisodes(t, st, 1)
	calls, maxBatch := 0, 0
	srv := fakeYouTube(t, &calls, &maxBatch, http.StatusOK)
	defer srv.Close()

	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	y := newYouTubeSyncer(st, "test-key")
	y.endpoint = srv.URL
	y.now = func() time.Time { return now }
	y.logf = t.Logf

	if err := y.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	videoID, views, chapters, fetchedAt, err := st.YouTubeMeta(1)
	if err != nil {
		t.Fatal(err)
	}
	if videoID != "vid1" || views != 1000 || len(chapters) != 3 || chapters[1].Start != 750 {
		t.Fatalf("снимок: %q %d %+v", videoID, views, chapters)
	}
	if fetchedAt != now.Unix() {
		t.Fatalf("fetched_at %d, хотели %d", fetchedAt, now.Unix())
	}

	// повторный цикл в пределах окна не должен ходить в API
	if err := y.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if calls != 1 {
		t.Fatalf("запросов %d, хотели 1 (окно 6 часов)", calls)
	}
}

func TestYouTubeRefreshKeepsViewsOnError(t *testing.T) {
	st := newTestStore(t)
	seedYouTubeEpisodes(t, st, 1)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	if err := st.SaveYouTubeMeta(1, "vid1", 12345, nil, now.Add(-7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	calls, maxBatch := 0, 0
	srv := fakeYouTube(t, &calls, &maxBatch, http.StatusInternalServerError)
	defer srv.Close()

	y := newYouTubeSyncer(st, "test-key")
	y.endpoint = srv.URL
	y.now = func() time.Time { return now }
	y.logf = t.Logf

	if err := y.Refresh(context.Background()); err == nil {
		t.Fatal("ожидали ошибку внешнего API")
	}
	_, views, _, _, err := st.YouTubeMeta(1)
	if err != nil {
		t.Fatal(err)
	}
	if views != 12345 {
		t.Fatalf("просмотры %d, хотели прежние 12345", views)
	}
}

func TestYouTubeRefreshBatchesFifty(t *testing.T) {
	st := newTestStore(t)
	seedYouTubeEpisodes(t, st, 51)
	calls, maxBatch := 0, 0
	srv := fakeYouTube(t, &calls, &maxBatch, http.StatusOK)
	defer srv.Close()

	y := newYouTubeSyncer(st, "test-key")
	y.endpoint = srv.URL
	y.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	y.logf = t.Logf
	if err := y.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if calls != 2 || maxBatch != 50 {
		t.Fatalf("запросов %d, максимум id %d; хотели 2 и 50", calls, maxBatch)
	}
}

func TestYouTubeRefreshSkipsUnparsableLink(t *testing.T) {
	st := newTestStore(t)
	if _, _, _, err := st.Sync([]Episode{{
		Number:      1,
		Title:       "Без ссылки",
		YouTube:     "https://example.com/video",
		PublishedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatal(err)
	}
	calls, maxBatch := 0, 0
	srv := fakeYouTube(t, &calls, &maxBatch, http.StatusOK)
	defer srv.Close()
	y := newYouTubeSyncer(st, "test-key")
	y.endpoint = srv.URL
	y.logf = t.Logf
	if err := y.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if calls != 0 {
		t.Fatalf("запросов %d, хотели 0", calls)
	}
}

func TestYouTubeMetaUpsertAndCascade(t *testing.T) {
	st := newTestStore(t)
	seedYouTubeEpisodes(t, st, 2)
	now := time.Now().UTC()
	if err := st.SaveYouTubeMeta(1, "vid1", 10, []Chapter{{0, "A"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveYouTubeMeta(1, "vid1", 20, []Chapter{{0, "B"}}, now); err != nil {
		t.Fatal(err)
	}
	_, views, chapters, _, err := st.YouTubeMeta(1)
	if err != nil || views != 20 || len(chapters) != 1 || chapters[0].Title != "B" {
		t.Fatalf("upsert: %d %+v %v", views, chapters, err)
	}

	// эпизод 1 удалён из папки — каскад должен убрать снимок
	if _, _, _, err := st.Sync([]Episode{{
		Number:      2,
		Title:       "Выпуск 2",
		YouTube:     "https://youtu.be/vid2",
		PublishedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := st.YouTubeMeta(1); err != sql.ErrNoRows {
		t.Fatalf("снимок удалённого эпизода остался: %v", err)
	}
}

func TestListYouTubeTargetsWindow(t *testing.T) {
	st := newTestStore(t)
	seedYouTubeEpisodes(t, st, 2)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	if err := st.SaveYouTubeMeta(1, "vid1", 1, nil, now.Add(-7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveYouTubeMeta(2, "vid2", 2, nil, now.Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}
	targets, err := st.ListYouTubeTargets(now.Add(-6 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Number != 1 {
		t.Fatalf("цели: %+v", targets)
	}
}
