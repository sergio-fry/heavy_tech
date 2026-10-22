package main

import (
	"path/filepath"
	"testing"
	"time"
)

// newTestStore — база в temp-каталоге с накатанными миграциями.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestOpenMigrations — после наката есть все таблицы и соль хеширования.
func TestOpenMigrations(t *testing.T) {
	st := newTestStore(t)
	for _, table := range []string{"episodes", "episode_files", "download_events", "play_events", "meta"} {
		var name string
		err := st.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("таблица %s не создана: %v", table, err)
		}
	}
	var salt string
	if err := st.db.QueryRow(`SELECT value FROM meta WHERE key = 'hash_salt'`).Scan(&salt); err != nil {
		t.Fatalf("соль не создана: %v", err)
	}
	if len(salt) != 32 { // 16 байт hex
		t.Fatalf("неожиданная соль: %q", salt)
	}
}

// TestSchemaNumberedModel — ключ эпизода — номер, slug-полей прежней схемы нет.
func TestSchemaNumberedModel(t *testing.T) {
	st := newTestStore(t)
	cols := func(table string) map[string]bool {
		rows, err := st.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			out[name] = true
		}
		return out
	}
	ep := cols("episodes")
	if !ep["number"] || !ep["youtube"] || ep["slug"] || ep["file_path"] {
		t.Fatalf("episodes: %v", ep)
	}
	files := cols("episode_files")
	for _, want := range []string{"number", "kind", "file_path", "mime", "size", "duration_seconds", "bitrate_kbps", "resolution", "file_mtime"} {
		if !files[want] {
			t.Fatalf("episode_files: нет колонки %s (%v)", want, files)
		}
	}
	events := cols("download_events")
	if !events["number"] || !events["kind"] || events["episode_id"] {
		t.Fatalf("download_events: %v", events)
	}
	if plays := cols("play_events"); !plays["number"] || plays["episode_id"] {
		t.Fatalf("play_events: %v", plays)
	}
}

// TestHashStableAndSalted — хеши детерминированы в пределах инстанса и разные между инстансами.
func TestHashStableAndSalted(t *testing.T) {
	a := newTestStore(t)
	b := newTestStore(t)
	h1 := a.hash("203.0.113.7")
	h2 := a.hash("203.0.113.7")
	if string(h1) != string(h2) {
		t.Fatal("хеш в пределах инстанса нестабилен")
	}
	if string(h1) == string(b.hash("203.0.113.7")) {
		t.Fatal("хеши разных инстансов совпали — соль не работает")
	}
}

// TestStoreSyncCRUD — прямой CRUD индекса: вставка, no-op, обновление, удаление.
func TestStoreSyncCRUD(t *testing.T) {
	st := newTestStore(t)
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	ep := Episode{
		Number:      7,
		Title:       "Седьмой",
		Description: "Описание",
		YouTube:     "https://youtu.be/abc123",
		PublishedAt: pub,
		Files: []EpisodeFile{
			{Kind: KindAudio, FilePath: "/p/7/a.m4a", Mime: "audio/x-m4a", Size: 100, DurationSeconds: 60, FileMtime: 111},
			{Kind: KindVideo, FilePath: "/p/7/v.mp4", Mime: "video/mp4", Size: 900, Resolution: "1920x1080", FileMtime: 222},
		},
	}
	if added, updated, removed, err := st.Sync([]Episode{ep}); err != nil || added != 1 || updated+removed != 0 {
		t.Fatalf("вставка: %d/%d/%d err=%v", added, updated, removed, err)
	}
	got, err := st.EpisodeByNumber(7)
	if err != nil {
		t.Fatalf("EpisodeByNumber: %v", err)
	}
	if got.Title != "Седьмой" || len(got.Files) != 2 || !got.PublishedAt.Equal(pub) ||
		got.YouTube != "https://youtu.be/abc123" {
		t.Fatalf("прочитан неверно: %+v", got)
	}
	if v, ok := got.File(KindVideo); !ok || v.Resolution != "1920x1080" || v.Size != 900 {
		t.Fatalf("видео: %+v (%v)", v, ok)
	}

	// повтор без изменений — no-op (идемпотентность)
	if added, updated, removed, err := st.Sync([]Episode{ep}); err != nil || added+updated+removed != 0 {
		t.Fatalf("no-op: %d/%d/%d err=%v", added, updated, removed, err)
	}

	// обновление заголовка, YouTube-ссылки и носителя
	ep.Title = "Седьмой (обновлён)"
	ep.YouTube = "https://www.youtube.com/watch?v=xyz"
	ep.Files[0].Size = 200
	if added, updated, removed, err := st.Sync([]Episode{ep}); err != nil || added != 0 || updated != 1 || removed != 0 {
		t.Fatalf("обновление: %d/%d/%d err=%v", added, updated, removed, err)
	}
	got, _ = st.EpisodeByNumber(7)
	if got.Title != "Седьмой (обновлён)" || got.YouTube != "https://www.youtube.com/watch?v=xyz" ||
		got.Files[0].Size != 200 || len(got.Files) != 2 {
		t.Fatalf("после обновления: %+v", got)
	}

	// снятие ссылки — тоже изменение записи
	ep.YouTube = ""
	if _, updated, _, err := st.Sync([]Episode{ep}); err != nil || updated != 1 {
		t.Fatalf("снятие youtube: updated=%d err=%v", updated, err)
	}
	if got, _ = st.EpisodeByNumber(7); got.YouTube != "" {
		t.Fatalf("youtube не снят: %+v", got)
	}

	// метрики и их сброс вместе с эпизодом
	if ok, err := st.RecordDownload(7, KindAudio, "1.2.3.4", "UA", pub); err != nil || !ok {
		t.Fatalf("RecordDownload: %v %v", ok, err)
	}
	if ok, _ := st.RecordDownload(7, KindAudio, "1.2.3.4", "UA", pub); ok {
		t.Fatal("повторное скачивание в те же сутки не должно засчитываться")
	}
	if err := st.RecordPlay(7, nil, pub); err != nil {
		t.Fatal(err)
	}
	if dl, _ := st.CountDownloads(7); dl.Audio != 1 || dl.Total != 1 {
		t.Fatalf("скачивания: %+v", dl)
	}

	// удаление эпизода уносит носители и события (CASCADE)
	if _, _, removed, err := st.Sync(nil); err != nil || removed != 1 {
		t.Fatalf("удаление: removed=%d err=%v", removed, err)
	}
	if _, err := st.EpisodeByNumber(7); err == nil {
		t.Fatal("эпизод не удалён")
	}
	var files, events, plays int
	_ = st.db.QueryRow(`SELECT COUNT(*) FROM episode_files`).Scan(&files)
	_ = st.db.QueryRow(`SELECT COUNT(*) FROM download_events`).Scan(&events)
	_ = st.db.QueryRow(`SELECT COUNT(*) FROM play_events`).Scan(&plays)
	if files != 0 || events != 0 || plays != 0 {
		t.Fatalf("после удаления остались строки: files=%d events=%d plays=%d", files, events, plays)
	}
}
