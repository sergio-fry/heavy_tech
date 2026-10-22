package main

import (
	"os"
	"path/filepath"
	"slices"
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

// writeEpisode создаёт папку-эпизод с meta и media-файлами.
// files — имя файла → содержимое (синтетическое: валидность медиа не проверяется).
func writeEpisode(t *testing.T, dir, folder, meta string, files map[string]string) {
	t.Helper()
	sub := filepath.Join(dir, folder)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sub, metaFileName), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustScan — сканирование, падающее на ошибке.
func mustScan(t *testing.T, dir string) []Episode {
	t.Helper()
	eps, err := ScanFolder(dir)
	if err != nil {
		t.Fatalf("ScanFolder: %v", err)
	}
	return eps
}

// metaAudio — минимальный валидный episode.yaml с аудио.
func metaAudio(title, date, file string) string {
	return "title: " + title + "\npublished_at: " + date + "\naudio:\n  file: " + file + "\n"
}

// TestParticipantsMeta — host/guests разбираются, порядок гостей сохраняется;
// поля опциональны, их отсутствие — норма.
func TestParticipantsMeta(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "45", `title: Интервью
published_at: 2026-11-01
host: Сергей Удалов
guests:
  - Мила Шведова
  - Дмитрий Копров
audio:
  file: zapis.m4a
`, map[string]string{"zapis.m4a": "m4a-bytes"})
	writeEpisode(t, dir, "46", metaAudio("Без участников", "2026-11-02", "tishina.m4a"),
		map[string]string{"tishina.m4a": "m4a-bytes"})

	eps := mustScan(t, dir)
	byNumber := map[int64]Episode{}
	for _, e := range eps {
		byNumber[e.Number] = e
	}
	e := byNumber[45]
	if e.Host != "Сергей Удалов" {
		t.Fatalf("host: %q", e.Host)
	}
	if !slices.Equal(e.Guests, []string{"Мила Шведова", "Дмитрий Копров"}) {
		t.Fatalf("guests: %v", e.Guests)
	}
	if e2 := byNumber[46]; e2.Host != "" || len(e2.Guests) != 0 {
		t.Fatalf("без участников: %q / %v", e2.Host, e2.Guests)
	}
}

// TestParticipantsBadType — некорректный тип значения — ошибка разбора meta.
func TestParticipantsBadType(t *testing.T) {
	for name, meta := range map[string]string{
		"строка вместо списка": `title: X
published_at: 2026-11-01
host: Сергей
guests: Мила Шведова
audio:
  file: zapis.m4a
`,
		"число вместо строки": `title: X
published_at: 2026-11-01
host: 42
guests:
  - Мила
audio:
  file: zapis.m4a
`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writePodcastDir(t)
			writeEpisode(t, dir, "7", meta, map[string]string{"zapis.m4a": "m4a-bytes"})
			if _, err := ScanFolder(dir); err == nil {
				t.Fatal("ожидали ошибку типа значения участников")
			}
		})
	}
}

// TestParticipantsEmptyGuest — пустой элемент списка гостей — ошибка синхронизации.
func TestParticipantsEmptyGuest(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", `title: X
published_at: 2026-11-01
host: Сергей
guests:
  - ""
  - Мила
audio:
  file: zapis.m4a
`, map[string]string{"zapis.m4a": "m4a-bytes"})
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку пустого элемента guests")
	}
}

// TestScanFolderLayout — валидный эпизод: номер из папки, поля из meta, MIME по расширению.
func TestScanFolderLayout(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", `title: «Первый выпуск»
published_at: 2026-10-01
description: О чём этот подкаст
audio:
  file: zapis.m4a
  duration: 1872
  bitrate_kbps: 128
`, map[string]string{"zapis.m4a": "m4a-bytes"})

	eps := mustScan(t, dir)
	if len(eps) != 1 {
		t.Fatalf("хотели 1 эпизод, получили %d: %+v", len(eps), eps)
	}
	e := eps[0]
	if e.Number != 7 || e.Title != "«Первый выпуск»" || e.Description != "О чём этот подкаст" {
		t.Fatalf("эпизод разобран неверно: %+v", e)
	}
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC); !e.PublishedAt.Equal(want) {
		t.Fatalf("дата: хотели %v, получили %v", want, e.PublishedAt)
	}
	if len(e.Files) != 1 {
		t.Fatalf("носителей %d, хотели 1", len(e.Files))
	}
	f := e.Files[0]
	if f.Kind != KindAudio || f.Mime != "audio/x-m4a" || f.Size != int64(len("m4a-bytes")) || f.DurationSeconds != 1872 {
		t.Fatalf("носитель разобран неверно: %+v", f)
	}
	if f.BitrateKbps != 128 || f.FilePath != filepath.Join(dir, "7", "zapis.m4a") {
		t.Fatalf("характеристики/путь носителя: %+v", f)
	}
}

// TestScanAudioAndVideo — один выпуск с аудио и видео: два носителя, порядок audio→video.
func TestScanAudioAndVideo(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", `title: С видео
published_at: 2026-10-01
audio:
  file: zapis.m4a
video:
  file: efir.mp4
  resolution: 1920x1080
`, map[string]string{"zapis.m4a": "audio", "efir.mp4": "video-bytes"})

	eps := mustScan(t, dir)
	if len(eps) != 1 || len(eps[0].Files) != 2 {
		t.Fatalf("хотели эпизод с двумя носителями: %+v", eps)
	}
	if a := eps[0].Files[0]; a.Kind != KindAudio || a.Mime != "audio/x-m4a" {
		t.Fatalf("аудио: %+v", a)
	}
	if v := eps[0].Files[1]; v.Kind != KindVideo || v.Mime != "video/mp4" || v.Resolution != "1920x1080" {
		t.Fatalf("видео: %+v", v)
	}
	if v := eps[0].Files[1]; v.Size != int64(len("video-bytes")) {
		t.Fatalf("размер видео: %d", v.Size)
	}
}

// TestScanMissingMeta — папка без episode.yaml — ошибка синхронизации.
func TestScanMissingMeta(t *testing.T) {
	dir := writePodcastDir(t)
	sub := filepath.Join(dir, "8")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "zapis.m4a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку отсутствия episode.yaml")
	}
}

// TestScanBrokenYAML — битый YAML в meta — ошибка, а не тихий пропуск.
func TestScanBrokenYAML(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", "title: [ненезакрытая\n", map[string]string{"zapis.m4a": "x"})
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку разбора episode.yaml")
	}
}

// TestScanMissingFile — meta ссылается на несуществующий файл — ошибка.
func TestScanMissingFile(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", metaAudio("Эпизод", "2026-10-01", "net-fayla.m4a"), nil)
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку несуществующего файла")
	}
}

// TestScanMissingRequiredFields — title и published_at обязательны.
func TestScanMissingRequiredFields(t *testing.T) {
	for name, meta := range map[string]string{
		"нет title":        "published_at: 2026-10-01\naudio:\n  file: z.m4a\n",
		"нет published_at": "title: X\naudio:\n  file: z.m4a\n",
		"битая дата":       "title: X\npublished_at: 2026-02-30\naudio:\n  file: z.m4a\n",
		"нет media-секций": "title: X\npublished_at: 2026-10-01\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := writePodcastDir(t)
			writeEpisode(t, dir, "7", meta, map[string]string{"z.m4a": "x"})
			if _, err := ScanFolder(dir); err == nil {
				t.Fatalf("ожидали ошибку валидации meta (%s)", name)
			}
		})
	}
}

// TestScanInvalidFolderNames — 0 и ведущие нули — ошибка, нецифровые папки игнор.
func TestScanInvalidFolderNames(t *testing.T) {
	for _, folder := range []string{"007", "0", "00"} {
		t.Run(folder, func(t *testing.T) {
			dir := writePodcastDir(t)
			writeEpisode(t, dir, folder, metaAudio("X", "2026-10-01", "z.m4a"), map[string]string{"z.m4a": "x"})
			if _, err := ScanFolder(dir); err == nil {
				t.Fatalf("папка %q должна быть ошибкой синхронизации", folder)
			}
		})
	}
}

// TestScanNonNumericFoldersIgnored — служебные папки не эпизоды и не ошибка.
func TestScanNonNumericFoldersIgnored(t *testing.T) {
	dir := writePodcastDir(t)
	sub := filepath.Join(dir, "chernoviki")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "vsyakoe.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// остатки прежнего layout в корне тоже не эпизоды
	if err := os.WriteFile(filepath.Join(dir, "2026-10-01-staryy.m4a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	eps := mustScan(t, dir)
	if len(eps) != 0 {
		t.Fatalf("ожидали пустой индекс, получили %+v", eps)
	}
}

// TestScanAudioInVideoSection — расширение не соответствует секции — ошибка.
func TestScanAudioInVideoSection(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", `title: X
published_at: 2026-10-01
video:
  file: zapis.m4a
`, map[string]string{"zapis.m4a": "x"})
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("аудио-файл в video-секции должен быть ошибкой")
	}
}

// TestScanUnsupportedExtension — неизвестное расширение — ошибка секции.
func TestScanUnsupportedExtension(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", metaAudio("X", "2026-10-01", "zapis.wav"), map[string]string{"zapis.wav": "x"})
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку неподдерживаемого расширения")
	}
}

// TestScanFileOutsideFolder — путь в file: обязан быть именем файла в папке.
func TestScanFileOutsideFolder(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", metaAudio("X", "2026-10-01", "../outside.m4a"), nil)
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("ожидали ошибку пути вне папки эпизода")
	}
}

// TestScanExtraFile — не упомянутый в meta недот-файл — ошибка.
func TestScanExtraFile(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", metaAudio("X", "2026-10-01", "zapis.m4a"),
		map[string]string{"zapis.m4a": "x", "staraya-zapis.mp3": "y"})
	if _, err := ScanFolder(dir); err == nil {
		t.Fatal("лишний файл должен быть ошибкой синхронизации")
	}
}

// TestScanDotFileIgnored — временные dot-файлы копирования не мешают публикации.
func TestScanDotFileIgnored(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", metaAudio("X", "2026-10-01", "zapis.m4a"),
		map[string]string{"zapis.m4a": "x", ".zapis.m4a.gSNpVJ": "tmp"})
	eps := mustScan(t, dir)
	if len(eps) != 1 || len(eps[0].Files) != 1 {
		t.Fatalf("dot-файл не должен ломать скан: %+v", eps)
	}
}

// TestScanOrderByNumber — результат сканера упорядочен по номеру.
func TestScanOrderByNumber(t *testing.T) {
	dir := writePodcastDir(t)
	for _, n := range []string{"10", "2", "1"} {
		writeEpisode(t, dir, n, metaAudio("Эпизод "+n, "2026-10-01", "z.m4a"), map[string]string{"z.m4a": "x"})
	}
	eps := mustScan(t, dir)
	if len(eps) != 3 || eps[0].Number != 1 || eps[1].Number != 2 || eps[2].Number != 10 {
		t.Fatalf("порядок/номера: %+v", eps)
	}
}

// TestSyncLifecycle — добавление, идемпотентность, удаление, переименование, потеря БД.
func TestSyncLifecycle(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)

	// 1) новый эпизод добавляется
	writeEpisode(t, dir, "1", metaAudio("Первый", "2026-10-01", "a.m4a"), map[string]string{"a.m4a": "content-a"})
	added, updated, removed, err := SyncPodcastDir(dir, st)
	if err != nil || added != 1 || updated != 0 || removed != 0 {
		t.Fatalf("sync#1: %d/%d/%d, err=%v", added, updated, removed, err)
	}

	// 2) идемпотентность
	added, updated, removed, err = SyncPodcastDir(dir, st)
	if err != nil || added+updated+removed != 0 {
		t.Fatalf("sync#2 (идемпотентность): %d/%d/%d, err=%v", added, updated, removed, err)
	}

	// 3) метрики эпизода до переименования
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if ok, err := st.RecordDownload(1, KindAudio, "203.0.113.5", "TestUA/1.0", now); err != nil || !ok {
		t.Fatalf("RecordDownload: ok=%v err=%v", ok, err)
	}
	if err := st.RecordPlay(1, nil, now); err != nil {
		t.Fatal(err)
	}

	// 4) добавление второго + удаление первого
	writeEpisode(t, dir, "2", metaAudio("Второй", "2026-10-15", "b.m4a"), map[string]string{"b.m4a": "content-b"})
	if err := os.RemoveAll(filepath.Join(dir, "1")); err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err = SyncPodcastDir(dir, st)
	if err != nil || added != 1 || updated != 0 || removed != 1 {
		t.Fatalf("sync#3: %d/%d/%d, err=%v", added, updated, removed, err)
	}
	if _, err := st.EpisodeByNumber(1); err == nil {
		t.Fatal("эпизод 1 должен быть удалён из индекса")
	}

	// 5) переименование папки = новая идентичность, счётчики с нуля
	if err := os.Rename(filepath.Join(dir, "2"), filepath.Join(dir, "5")); err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err = SyncPodcastDir(dir, st)
	if err != nil || added != 1 || updated != 0 || removed != 1 {
		t.Fatalf("sync#4 (переименование): %d/%d/%d, err=%v", added, updated, removed, err)
	}
	if dl, err := st.CountDownloads(5); err != nil || dl.Total != 0 {
		t.Fatalf("у нового эпизода скачиваний %d, хотели 0 (err=%v)", dl.Total, err)
	}

	// 6) потеря БД: индекс восстанавливается из папок
	st2, err := OpenStore(filepath.Join(t.TempDir(), "restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, _, _, err = SyncPodcastDir(dir, st2); err != nil {
		t.Fatal(err)
	}
	if _, err := st2.EpisodeByNumber(5); err != nil {
		t.Fatalf("после потери БД эпизод 5 не восстановлен: %v", err)
	}
}

// TestSyncUpdatesChangedMedia — изменение размера/mtime носителя обновляет индекс.
func TestSyncUpdatesChangedMedia(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	writeEpisode(t, dir, "1", metaAudio("Первый", "2026-10-01", "a.m4a"), map[string]string{"a.m4a": "short"})
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "1", "a.m4a")
	if err := os.WriteFile(path, []byte("much longer content now"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err := SyncPodcastDir(dir, st)
	if err != nil || added != 0 || updated != 1 || removed != 0 {
		t.Fatalf("sync: %d/%d/%d, err=%v", added, updated, removed, err)
	}
	ep, err := st.EpisodeByNumber(1)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Files[0].Size != int64(len("much longer content now")) {
		t.Fatalf("размер не обновился: %d", ep.Files[0].Size)
	}
}

// TestSyncUpdatesMetaOnly — правка title без изменения файла обновляет эпизод.
func TestSyncUpdatesMetaOnly(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	writeEpisode(t, dir, "1", metaAudio("Старый", "2026-10-01", "a.m4a"), map[string]string{"a.m4a": "x"})
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	writeEpisode(t, dir, "1", metaAudio("Новый", "2026-10-01", "a.m4a"), map[string]string{"a.m4a": "x"})
	if _, updated, _, err := SyncPodcastDir(dir, st); err != nil || updated != 1 {
		t.Fatalf("обновление title: updated=%d err=%v", updated, err)
	}
	ep, _ := st.EpisodeByNumber(1)
	if ep.Title != "Новый" {
		t.Fatalf("title = %q", ep.Title)
	}
}

// TestSyncKeepsIndexOnScanError — ошибка скана не портит индекс.
func TestSyncKeepsIndexOnScanError(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	writeEpisode(t, dir, "1", metaAudio("Первый", "2026-10-01", "a.m4a"), map[string]string{"a.m4a": "x"})
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	writeEpisode(t, dir, "2", metaAudio("Битый", "2026-10-01", "net.m4a"), nil)
	if _, _, _, err := SyncPodcastDir(dir, st); err == nil {
		t.Fatal("ожидали ошибку скана")
	}
	if _, err := st.EpisodeByNumber(1); err != nil {
		t.Fatalf("эпизод 1 пропал из индекса после неудачного скана: %v", err)
	}
}

// TestLoadChannelMissing — канал без podcast.yaml не публикуется.
func TestLoadChannelMissing(t *testing.T) {
	if _, err := LoadChannel(t.TempDir()); err == nil {
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

// TestHumanSize — размеры в подписях ссылок «скачать».
func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:               "0 Б",
		980:             "980 Б",
		1024:            "1 КБ",
		12345:           "12,1 КБ",
		5 * 1024 * 1024: "5 МБ",
		12900000:        "12,3 МБ",
	}
	for size, want := range cases {
		if got := humanSize(size); got != want {
			t.Errorf("humanSize(%d) = %q, хотели %q", size, got, want)
		}
	}
}

// TestScanDescriptionMarkdown — description.md задаёт описание и не считается
// лишним файлом в папке эпизода.
func TestScanDescriptionMarkdown(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", metaAudio("X", "2026-10-01", "z.m4a"), map[string]string{
		"z.m4a":          "x",
		"description.md": "  ## Шоу-ноуты\n\n- пункт\n  ",
	})
	eps := mustScan(t, dir)
	if len(eps) != 1 || eps[0].Description != "## Шоу-ноуты\n\n- пункт" {
		t.Fatalf("описание из description.md разобрано неверно: %+v", eps)
	}
}

// TestScanDescriptionFileWins — при обоих источниках побеждает файл.
func TestScanDescriptionFileWins(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", `title: X
published_at: 2026-10-01
description: Старое описание
audio:
  file: z.m4a
`, map[string]string{"z.m4a": "x", "description.md": "**Новое** описание"})
	eps := mustScan(t, dir)
	if len(eps) != 1 || eps[0].Description != "**Новое** описание" {
		t.Fatalf("файл не приоритетнее meta: %+v", eps)
	}
}

// TestScanDescriptionFallback — без файла описание берётся из meta.
func TestScanDescriptionFallback(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", `title: X
published_at: 2026-10-01
description: О чём выпуск
audio:
  file: z.m4a
`, map[string]string{"z.m4a": "x"})
	eps := mustScan(t, dir)
	if len(eps) != 1 || eps[0].Description != "О чём выпуск" {
		t.Fatalf("фолбэк на description: не сработал: %+v", eps)
	}
}

// TestScanDescriptionOptional — отсутствие описания допустимо.
func TestScanDescriptionOptional(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", metaAudio("X", "2026-10-01", "z.m4a"), map[string]string{"z.m4a": "x"})
	eps := mustScan(t, dir)
	if len(eps) != 1 || eps[0].Description != "" {
		t.Fatalf("описание должно быть пустым: %+v", eps)
	}
}

// TestSyncUpdatesDescriptionFile — правка description.md обновляет индекс.
func TestSyncUpdatesDescriptionFile(t *testing.T) {
	dir := writePodcastDir(t)
	st := newTestStore(t)
	writeEpisode(t, dir, "7", metaAudio("X", "2026-10-01", "z.m4a"), map[string]string{
		"z.m4a":          "x",
		"description.md": "Старое",
	})
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "7", descriptionFileName)
	if err := os.WriteFile(path, []byte("Новое описание"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, updated, _, err := SyncPodcastDir(dir, st); err != nil || updated != 1 {
		t.Fatalf("правка description.md: updated=%d err=%v", updated, err)
	}
	ep, _ := st.EpisodeByNumber(7)
	if ep.Description != "Новое описание" {
		t.Fatalf("описание в индексе: %q", ep.Description)
	}
}

// TestScanYouTubeLink — валидная ссылка сохраняется, отсутствие допустимо.
func TestScanYouTubeLink(t *testing.T) {
	dir := writePodcastDir(t)
	writeEpisode(t, dir, "7", `title: X
published_at: 2026-10-01
youtube: https://youtu.be/abc123
audio:
  file: z.m4a
`, map[string]string{"z.m4a": "x"})
	writeEpisode(t, dir, "8", metaAudio("Y", "2026-10-02", "z.m4a"), map[string]string{"z.m4a": "x"})
	eps := mustScan(t, dir)
	if len(eps) != 2 {
		t.Fatalf("эпизодов %d, хотели 2", len(eps))
	}
	if eps[0].YouTube != "https://youtu.be/abc123" {
		t.Fatalf("youtube эпизода 7: %q", eps[0].YouTube)
	}
	if eps[1].YouTube != "" {
		t.Fatalf("youtube эпизода 8: %q", eps[1].YouTube)
	}
}

// TestScanYouTubeInvalid — не абсолютный http(s) URL — ошибка синхронизации.
func TestScanYouTubeInvalid(t *testing.T) {
	for _, link := range []string{"youtu.be/abc123", "javascript:alert(1)", "ftp://example.com/video"} {
		t.Run(link, func(t *testing.T) {
			dir := writePodcastDir(t)
			writeEpisode(t, dir, "7", `title: X
published_at: 2026-10-01
youtube: `+link+`
audio:
  file: z.m4a
`, map[string]string{"z.m4a": "x"})
			if _, err := ScanFolder(dir); err == nil {
				t.Fatalf("ссылка %q должна быть ошибкой синхронизации", link)
			}
		})
	}
}
