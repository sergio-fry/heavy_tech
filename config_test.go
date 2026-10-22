package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeChannelYAML кладёт podcast.yaml с заданным содержимым во временную папку.
func writeChannelYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "podcast.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const channelWithExtras = `title: Heavy Tech
description: Подкаст о тяжёлом
tagline:  Разговоры о разработке
links:
  - label: Apple Podcasts
    url: https://podcasts.apple.com/ru/podcast/heavy-tech/id123
  - label: Telegram
    url: https://t.me/heavytech
start_here: [7, 3]
`

// TestLoadChannelExtras — новые поля читаются, тримятся и валидируются.
func TestLoadChannelExtras(t *testing.T) {
	dir := writeChannelYAML(t, channelWithExtras)
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if ch.Tagline != "Разговоры о разработке" {
		t.Fatalf("tagline %q", ch.Tagline)
	}
	if len(ch.Links) != 2 || ch.Links[0].Label != "Apple Podcasts" || ch.Links[1].URL != "https://t.me/heavytech" {
		t.Fatalf("links: %+v", ch.Links)
	}
	if len(ch.StartHere) != 2 || ch.StartHere[0] != 7 || ch.StartHere[1] != 3 {
		t.Fatalf("start_here: %+v", ch.StartHere)
	}
}

// TestLoadChannelWithoutExtras — старый podcast.yaml продолжает работать.
func TestLoadChannelWithoutExtras(t *testing.T) {
	dir := writeChannelYAML(t, "title: Heavy Tech\ndescription: О тяжёлом\n")
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if ch.Tagline != "" || len(ch.Links) != 0 || len(ch.StartHere) != 0 {
		t.Fatalf("неожиданные значения: %+v", ch)
	}
}

// TestLoadChannelRejectsBadLinks — пустой label и не-http(s) URL — ошибка.
func TestLoadChannelRejectsBadLinks(t *testing.T) {
	for name, content := range map[string]string{
		"empty label": "title: T\ndescription: D\nlinks:\n  - label: \" \"\n    url: https://example.com\n",
		"relative":    "title: T\ndescription: D\nlinks:\n  - label: Site\n    url: /about\n",
		"javascript":  "title: T\ndescription: D\nlinks:\n  - label: Bad\n    url: javascript:alert(1)\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadChannel(writeChannelYAML(t, content)); err == nil {
				t.Fatal("ожидали ошибку конфигурации")
			}
		})
	}
}

// TestLoadChannelRejectsWrongTypes — строковые номера и числовой tagline — ошибка.
func TestLoadChannelRejectsWrongTypes(t *testing.T) {
	for name, content := range map[string]string{
		"tagline number":   "title: T\ndescription: D\ntagline: 123\n",
		"start_here str":   "title: T\ndescription: D\nstart_here: ['7']\n",
		"links not list":   "title: T\ndescription: D\nlinks: {}\n",
		"label not string": "title: T\ndescription: D\nlinks:\n  - label: 1\n    url: https://example.com\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadChannel(writeChannelYAML(t, content)); err == nil {
				t.Fatal("ожидали ошибку типа")
			}
		})
	}
}

// TestLoadChannelRejectsBadStartHere — нулевой и отрицательный номера — ошибка.
func TestLoadChannelRejectsBadStartHere(t *testing.T) {
	for _, content := range []string{
		"title: T\ndescription: D\nstart_here: [0]\n",
		"title: T\ndescription: D\nstart_here: [-3]\n",
	} {
		if _, err := LoadChannel(writeChannelYAML(t, content)); err == nil {
			t.Fatalf("ожидали ошибку для %q", content)
		}
	}
}

// TestReadmeChannelExample — пример podcast.yaml из README проходит LoadChannel.
// Маркеры в README держат документацию и конфиг синхронными.
func TestReadmeChannelExample(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	const startMarker = "<!-- podcast-yaml-example:start -->"
	const endMarker = "<!-- podcast-yaml-example:end -->"
	start := strings.Index(text, startMarker)
	if start < 0 {
		t.Fatal("в README нет маркера примера podcast.yaml")
	}
	end := strings.Index(text[start:], endMarker)
	if end < 0 {
		t.Fatal("в README нет закрывающего маркера примера podcast.yaml")
	}
	block := text[start : start+end]
	// убрать маркер, fences и комментарии-пояснения после #
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == startMarker || strings.HasPrefix(trimmed, "```") {
			continue
		}
		lines = append(lines, line)
	}
	dir := writeChannelYAML(t, strings.Join(lines, "\n"))
	ch, err := LoadChannel(dir)
	if err != nil {
		t.Fatalf("пример из README не прошёл LoadChannel: %v", err)
	}
	if ch.Tagline == "" || len(ch.Links) != 2 || len(ch.StartHere) != 3 {
		t.Fatalf("README-пример разобран не полностью: %+v", ch)
	}
}
