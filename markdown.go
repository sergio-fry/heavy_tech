package main

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// descriptionMarkdown — единственный рендерер описаний выпусков. WithHardWraps:
// одиночный перевод строки в шоу-ноутах значим и превращается в <br>. html.WithUnsafe
// не включён, поэтому сырой HTML из исходника отбрасывается рендерером.
var descriptionMarkdown = goldmark.New(
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// RenderDescription превращает markdown-описание выпуска в безопасный HTML для
// сайта и RSS. Активные схемы ссылок нейтрализуются, пустое описание — пустая строка.
func RenderDescription(src string) template.HTML {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	source := []byte(src)
	doc := descriptionMarkdown.Parser().Parse(text.NewReader(source))
	sanitizeDescription(doc)

	var buf bytes.Buffer
	if err := descriptionMarkdown.Renderer().Render(&buf, source, doc); err != nil {
		// Рендер не должен падать на пользовательском тексте; на всякий случай
		// отдаём экранированный исходник, а не пустоту.
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(buf.String())
}

// sanitizeDescription убирает активные адреса: небезопасная ссылка становится
// текстом, небезопасная картинка удаляется. Сырой HTML отбрасывает goldmark.
func sanitizeDescription(doc ast.Node) {
	var badLinks []*ast.Link
	var badImages []*ast.Image
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Link:
			if !safeDestination(string(node.Destination), true) {
				badLinks = append(badLinks, node)
			}
		case *ast.Image:
			if !safeDestination(string(node.Destination), false) {
				badImages = append(badImages, node)
			}
		}
		return ast.WalkContinue, nil
	})

	// Мутации — после обхода: подмена узлов во время Walk сбивает итерацию.
	for _, link := range badLinks {
		replaceWithChildren(link)
	}
	for _, img := range badImages {
		if parent := img.Parent(); parent != nil {
			parent.RemoveChild(parent, img)
		}
	}
}

// replaceWithChildren заменяет узел его содержимым (текстом ссылки).
func replaceWithChildren(n ast.Node) {
	parent := n.Parent()
	if parent == nil {
		return
	}
	for child := n.FirstChild(); child != nil; {
		next := child.NextSibling()
		parent.InsertBefore(parent, n, child)
		child = next
	}
	parent.RemoveChild(parent, n)
}

// safeDestination пропускает http/https, а для ссылок ещё mailto, якоря и
// относительные адреса. javascript:, data:, vbscript: и прочие схемы — отказ.
func safeDestination(dst string, allowMail bool) bool {
	dst = strings.TrimSpace(dst)
	if dst == "" {
		return false
	}
	u, err := url.Parse(dst)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return allowMail
	case "":
		// Относительная ссылка или якорь: активной схемы нет.
		return true
	default:
		return false
	}
}
