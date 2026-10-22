package main

import (
	"strings"
	"testing"
)

// TestRenderDescriptionFormatting — markdown становится разметкой, одиночные
// переводы строк сохраняются (hard wraps).
func TestRenderDescriptionFormatting(t *testing.T) {
	src := "Первый абзац\nвторая строка\n\n- один\n- два\n\n**важно** и [гость](https://example.com)"
	got := string(RenderDescription(src))

	for _, want := range []string{
		"<p>Первый абзац<br",
		"вторая строка",
		"<li>один</li>",
		"<li>два</li>",
		"<strong>важно</strong>",
		`href="https://example.com"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("в HTML нет %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "**важно**") || strings.Contains(got, "- один") {
		t.Fatalf("markdown не отрендерен:\n%s", got)
	}
}

// TestRenderDescriptionSafety — сырой HTML не исполняется, активные схемы
// ссылок превращаются в текст, небезопасные картинки удаляются.
func TestRenderDescriptionSafety(t *testing.T) {
	src := `<script>alert(1)</script>

<img src=x onerror=alert(1)>

[клик](javascript:alert(1)) и [почта](mailto:author@example.com)

![картинка](data:image/png;base64,AAAA)`
	got := string(RenderDescription(src))
	lower := strings.ToLower(got)

	for _, forbidden := range []string{"<script", "onerror", "javascript:", "<img"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("в HTML осталось %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{"клик", "mailto:author@example.com"} {
		if !strings.Contains(got, want) {
			t.Fatalf("в HTML нет %q:\n%s", want, got)
		}
	}
}

// TestRenderDescriptionRelativeLinks — якоря и относительные ссылки разрешены.
func TestRenderDescriptionRelativeLinks(t *testing.T) {
	got := string(RenderDescription("[выпуск](/audio/7) и [якорь](#top)"))
	for _, want := range []string{`href="/audio/7"`, `href="#top"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("в HTML нет %q:\n%s", want, got)
		}
	}
}

// TestRenderDescriptionEmpty — пустое описание не даёт блока.
func TestRenderDescriptionEmpty(t *testing.T) {
	for _, src := range []string{"", "   \n\t\n  "} {
		if got := string(RenderDescription(src)); got != "" {
			t.Fatalf("для %q получили %q, хотели пустую строку", src, got)
		}
	}
}
