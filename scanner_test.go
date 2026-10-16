package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// writePodcastDir — фикстурная папка: podcast.yaml и больше ничего.
func writePodcastDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeChannel(t, dir)
	return dir
}

func writeChannel(t *testing.T, dir string) {
	t.Helper()
	const ch = `title: Heavy Tech
description: Тестовый подкаст о тяжёлом
author: Сергей
email: sergey@example.com
category: Technology
explicit: false
`
	if err := os.WriteFile(filepath.Join(dir, "podcast.yaml"), []byte(ch), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeEpisode создаёт MP3-файл с заданным содержимым (и опциональный sidecar).
func writeEpisode(t *testing.T, dir, date, slug, content string, sc *sidecar) {
	t.Helper()
	name := filepath.Join(dir, date+"-"+slug+".mp3")
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if sc != nil {
		data := ""
		if sc.Title != "" {
			data += "title: " + sc.Title + "\n"
		}
		if sc.Description != "" {
			data += "description: " + sc.Description + "\n"
		}
		if sc.Duration > 0 {
			data += "duration: " + strconv.FormatInt(sc.Duration, 10) + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, slug+".yaml"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustScan(t *testing.T, dir string) []Episode {
	t.Helper()
	eps, err := ScanFolder(dir)
	if err != nil {
		t.Fatalf("ScanFolder: %v", err)
	}
	return eps
}

// TestParseFileName — конвенция имени: валидное, без даты, невалидная дата, дубль слага.
func TestParseFileName(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "2026-10-01", "pervyy-vypusk", "mp3-bytes", nil)
	writeEpisode(t, dir, "2026-10-15", "vtoroy", "mp3-bytes-2", nil)
	// игнорируются: без даты, мусорное расширение, вложенная папка
	if err := os.WriteFile(filepath.Join(dir, "my-episode.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-20-encode.ogg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "2026-10-25-nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	eps := mustScan(t, dir)
	if len(eps) != 2 {
		t.Fatalf("хотели 2 эпизода, получили %d: %+v", len(eps), eps)
	}
	// порядок папки не важен; ищем по слагу
	bySlug := map[string]Episode{}
	for _, e := range eps {
		bySlug[e.Slug] = e
	}
	e, ok := bySlug["pervyy-vypusk"]
	if !ok {
		t.Fatal("эпизод pervyy-vypusk не найден")
	}
	want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if !e.PublishedAt.Equal(want) {
		t.Fatalf("дата: хотели %v, получили %v", want, e.PublishedAt)
	}
	if e.Mime != "audio/mpeg" || e.FileSize != int64(len("mp3-bytes")) {
		t.Fatalf("mime/size: %s/%d", e.Mime, e.FileSize)
	}
}

// TestInvalidCalendarDate — 2026-02-30 regexp-ом проходит, календарём — нет.
func TestInvalidCalendarDate(t *testing.T) {
	dir := writePodcastDir(t)
	if err := os.WriteFile(filepath.Join(dir, "2026-02-30-nesuschestvuyuschaya-data.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	eps := mustScan(t, dir)
	if len(eps) != 0 {
		t.Fatalf("невалидная дата должна игнорироваться, получили %+v", eps)
	}
}

// TestDuplicateSlug — один слаг у двух файлов (разные даты) — ошибка папки.
func TestDuplicateSlug(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "2026-10-01", "odinakovyy", "a", nil)
	writeEpisode(t, dir, "2026-10-15", "odinakovyy", "b", nil)
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку дублирующегося слага")
	}
}

// TestSidecarOverrides — sidecar переопределяет заголовок и добавляет описание/длительность.
func TestSidecarOverrides(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "2026-10-01", "so-sidecarom", "content", &sidecar{
		Title: "«Первый выпуск»", Description: "О чём этот подкаст", Duration: 1872,
	})
	eps := mustScan(t, dir)
	if len(eps) != 1 {
		t.Fatalf("хотели 1 эпизод, получили %d", len(eps))
	}
	e := eps[0]
	if e.Title != "«Первый выпуск»" || e.Description != "О чём этот подкаст" || e.DurationSeconds != 1872 {
		t.Fatalf("sidecar не применился: %+v", e)
	}
}

// TestSidecarMissingDefaults — без sidecar заголовок выводится из слага, описание пустое.
func TestSidecarMissingDefaults(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "2026-10-01", "pervyy-vypusk", "content", nil)
	eps := mustScan(t, dir)
	if len(eps) != 1 {
		t.Fatalf("хотели 1 эпизод, получили %d", len(eps))
	}
	e := eps[0]
	if e.Title != "Pervyy Vypusk" {
		t.Fatalf("дефолтный заголовок: %q", e.Title)
	}
	if e.Description != "" || e.DurationSeconds != 0 {
		t.Fatalf("ожидались пустые описание/длительность: %+v", e)
	}
}

// TestBrokenSidecar — битый YAML в sidecar — ошибка скана, а не тихое игнорирование.
func TestBrokenSidecar(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "2026-10-01", "s-bitym-yaml", "content", nil)
	if err := os.WriteFile(filepath.Join(dir, "s-bitym-yaml.yaml"), []byte("title: [ненезакрытая"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку разбора sidecar")
	}
}

// TestDefaultTitle — первая буква каждого слова заглавная.
func TestDefaultTitle(t *testing.T) {
	cases := map[string]string{
		"pervyy-vypusk":   "Pervyy Vypusk",
		"obzor-2026-goda": "Obzor 2026 Goda",
		"odin":            "Odin",
		"dvoynoy--defis":  "Dvoynoy  Defis", // двойной дефис в слаге невозможен по regexp, но функция устойчива
	}
	for slug, want := range cases {
		if got := DefaultTitle(slug); got != want {
			t.Errorf("DefaultTitle(%q) = %q, хотели %q", slug, got, want)
		}
	}
}

// TestSyncLifecycle — добавление, удаление, идемпотентность, потеря БД, переименование.
func TestSyncLifecycle(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)

	// 1) новый файл добавляется
	writeEpisode(t, dir, "2026-10-01", "a", "content-a", nil)
	added, updated, removed, err := SyncPodcastDir(dir, st)
	if err != nil || added != 1 || updated != 0 || removed != 0 {
		t.Fatalf("sync#1: %d/%d/%d, err=%v", added, updated, removed, err)
	}

	// 2) идемпотентность: повторный прогон ничего не меняет
	added, updated, removed, err = SyncPodcastDir(dir, st)
	if err != nil || added+updated+removed != 0 {
		t.Fatalf("sync#2 (идемпотентность): %d/%d/%d, err=%v", added, updated, removed, err)
	}

	// 3) метрики эпизода до переименования
	ep, err := st.EpisodeBySlug("a")
	if err != nil {
		t.Fatalf("эпизод a: %v", err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if ok, err := st.RecordDownload(ep.ID, "203.0.113.5", "TestUA/1.0", now); err != nil || !ok {
		t.Fatalf("RecordDownload: ok=%v err=%v", ok, err)
	}
	if err := st.RecordPlay(ep.ID, nil, now); err != nil {
		t.Fatal(err)
	}

	// 4) добавление второго + удаление первого
	writeEpisode(t, dir, "2026-10-15", "b", "content-b", nil)
	if err := os.Remove(filepath.Join(dir, "2026-10-01-a.mp3")); err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err = SyncPodcastDir(dir, st)
	if err != nil || added != 1 || updated != 0 || removed != 1 {
		t.Fatalf("sync#3: %d/%d/%d, err=%v", added, updated, removed, err)
	}
	if _, err := st.EpisodeBySlug("a"); err == nil {
		t.Fatal("эпизод a должен быть удалён из индекса")
	}

	// 5) переименование = удаление + добавление (новая идентичность, счётчики с нуля)
	oldPath := filepath.Join(dir, "2026-10-15-b.mp3")
	newPath := filepath.Join(dir, "2026-10-15-c.mp3")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err = SyncPodcastDir(dir, st)
	if err != nil || added != 1 || updated != 0 || removed != 1 {
		t.Fatalf("sync#4 (переименование): %d/%d/%d, err=%v", added, updated, removed, err)
	}
	nep, err := st.EpisodeBySlug("c")
	if err != nil {
		t.Fatalf("эпизод c: %v", err)
	}
	if dl, _ := st.CountDownloads(nep.ID); dl != 0 {
		t.Fatalf("у нового эпизода счётчик скачиваний = %d, хотели 0", dl)
	}

	// 6) потеря БД: индекс восстанавливается из папки
	dbPath := filepath.Join(t.TempDir(), "restore.db")
	st2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = SyncPodcastDir(dir, st2); err != nil {
		t.Fatal(err)
	}
	if _, err := st2.EpisodeBySlug("c"); err != nil {
		t.Fatalf("после потери БД эпизод c не восстановлен: %v", err)
	}
	st2.Close()
}

// TestSyncUpdatesChangedFile — изменение size+mtime файла обновляет индекс.
func TestSyncUpdatesChangedFile(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	writeEpisode(t, dir, "2026-10-01", "a", "short", nil)
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	// перезаписали файл другим размером; mtime гарантируем различным
	path := filepath.Join(dir, "2026-10-01-a.mp3")
	future := time.Now().Add(2 * time.Hour)
	if err := os.WriteFile(path, []byte("much longer content now"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err := SyncPodcastDir(dir, st)
	if err != nil || added != 0 || updated != 1 || removed != 0 {
		t.Fatalf("sync: %d/%d/%d, err=%v", added, updated, removed, err)
	}
	ep, err := st.EpisodeBySlug("a")
	if err != nil {
		t.Fatal(err)
	}
	if ep.FileSize != int64(len("much longer content now")) {
		t.Fatalf("size не обновился: %d", ep.FileSize)
	}
}

// TestLoadChannelMissing — канал без podcast.yaml не публикуется.
func TestLoadChannelMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadChannel(dir); err == nil {
		t.Fatal("ожидали ошибку отсутствия podcast.yaml")
	}
}

// TestLoadChannelApplies — метаданные канала читаются.
func TestLoadChannelApplies(t *testing.T) {
	dir := writePodcastDir(t)
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Title != "Heavy Tech" || ch.Author != "Сергей" || ch.Email != "sergey@example.com" || ch.Category != "Technology" {
		t.Fatalf("канал прочитан неверно: %+v", ch)
	}
	if ch.Explicit {
		t.Fatal("explicit должен быть false")
	}
}

// TestLoadChannelEmptyTitle — пустые title/description — ошибка валидации.
func TestLoadChannelEmptyTitle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "podcast.yaml"), []byte("title: \"\"\ndescription: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChannel(dir); err == nil {
		t.Fatal("ожидали ошибку пустого title")
	}
}
