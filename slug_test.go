package main

import (
	"strings"
	"testing"
)

// TestSlugify — транслит, очистка разделителей, регистр.
func TestSlugify(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Все фломастеры разные", "vse-flomastery-raznye"},
		{"«Все фломастеры разные»: Павел Аргентов", "vse-flomastery-raznye-pavel-argentov"},
		{"Ёлка, йод, щука, объём", "elka-yod-schuka-obem"},
		{"  Много   пробелов -- и дефисов  ", "mnogo-probelov-i-defisov"},
		{"Выпуск №39", "vypusk-39"},
		{"🔥🤖", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Slugify(tt.title); got != tt.want {
			t.Errorf("Slugify(%q) = %q, хотели %q", tt.title, got, tt.want)
		}
	}
}

// TestSlugifyTruncatesByWord — длинный заголовок обрезается по границе слова.
func TestSlugifyTruncatesByWord(t *testing.T) {
	title := "Очень длинный заголовок выпуска который должен обрезаться по границе слова а не посередине"
	got := Slugify(title)
	if len(got) > slugMaxLen {
		t.Fatalf("slug длиной %d > %d: %q", len(got), slugMaxLen, got)
	}
	if strings.HasSuffix(got, "-") || strings.HasPrefix(got, "-") || strings.Contains(got, "--") {
		t.Fatalf("некорректные дефисы в slug: %q", got)
	}
	if !strings.HasPrefix(got, "ochen-dlinnyy-zagolovok-vypuska") {
		t.Fatalf("slug обрезан не по началу заголовка: %q", got)
	}
	// Последний сегмент — целое слово (транслит), а не обрубок.
	words := map[string]bool{}
	for _, w := range strings.Fields(title) {
		words[Slugify(w)] = true
	}
	last := got[strings.LastIndex(got, "-")+1:]
	if !words[last] {
		t.Fatalf("последний сегмент slug — обрубок слова: %q", got)
	}
}

// TestMediaFilename — номер с ведущими нулями, slug, реальное расширение.
func TestMediaFilename(t *testing.T) {
	tests := []struct {
		number int64
		title  string
		path   string
		want   string
	}{
		{39, "Все фломастеры разные", "/data/podcast/39/efir.MP4", "039-vse-flomastery-raznye.mp4"},
		{7, "Аким Губанов", "/data/podcast/7/zapis.m4a", "007-akim-gubanov.m4a"},
		{1000, "Юбилей", "/data/podcast/1000/zapis.aac", "1000-yubiley.aac"},
		{7, "🔥", "/data/podcast/7/zapis.m4a", "007.m4a"},
	}
	for _, tt := range tests {
		f := EpisodeFile{FilePath: tt.path}
		if got := MediaFilename(tt.number, tt.title, f); got != tt.want {
			t.Errorf("MediaFilename(%d, %q, %q) = %q, хотели %q", tt.number, tt.title, tt.path, got, tt.want)
		}
	}
}
