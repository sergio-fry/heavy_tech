package main

import (
	"encoding/json"
	"encoding/xml"
	"html"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// plainText превращает markdown в плоский текст: рендер, снятие тегов,
// раскрытие сущностей и схлопывание пробелов. Нужен для meta description,
// поиска и JSON-LD.
func plainText(src string) string {
	rendered := string(RenderDescription(src))
	if rendered == "" {
		return ""
	}
	text := htmlTagRe.ReplaceAllString(rendered, " ")
	text = html.UnescapeString(text)
	return strings.Join(strings.Fields(text), " ")
}

// truncateRunes обрезает строку по рунам, не разрывая многобайтовый символ.
func truncateRunes(s string, limit int) string {
	runes := []rune(strings.TrimSpace(s))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	cut := strings.TrimRight(string(runes[:limit]), " ,.;:—–-")
	return cut + "…"
}

// jsonLD кодирует структуру в JSON, безопасный для вставки в <script>:
// encoding/json экранирует <, > и & в \u003c и т.п.
func jsonLD(v any) template.JS {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return template.JS(raw)
}

// seriesJSONLD — структурированная разметка канала для поисковиков.
func seriesJSONLD(ch Channel, baseURL string) template.JS {
	doc := map[string]any{
		"@context":    "https://schema.org",
		"@type":       "PodcastSeries",
		"name":        ch.Title,
		"description": ch.Description,
		"url":         baseURL + "/",
		"webFeed":     baseURL + "/feed.xml",
	}
	if ch.Image != "" {
		doc["image"] = ch.Image
	}
	if ch.Author != "" {
		doc["author"] = map[string]string{"@type": "Person", "name": ch.Author}
	}
	return jsonLD(doc)
}

// episodeJSONLD — структурированная разметка выпуска.
func episodeJSONLD(item episodeItem, ch Channel, baseURL string) template.JS {
	doc := map[string]any{
		"@context":      "https://schema.org",
		"@type":         "PodcastEpisode",
		"name":          item.Title,
		"url":           item.PageURL,
		"datePublished": item.DateISO,
		"description":   item.Summary,
		"partOfSeries": map[string]string{
			"@type": "PodcastSeries",
			"name":  ch.Title,
			"url":   baseURL + "/",
		},
	}
	if item.AudioDurationSecs > 0 {
		doc["duration"] = isoDuration(item.AudioDurationSecs)
	}
	if item.HasAudio {
		doc["associatedMedia"] = map[string]string{
			"@type":      "AudioObject",
			"contentUrl": item.AudioURL,
		}
	}
	if item.YouTube != "" {
		doc["sameAs"] = item.YouTube
	}
	return jsonLD(doc)
}

// isoDuration — длительность в ISO 8601: PT1H35M24S.
func isoDuration(sec int64) string {
	if sec <= 0 {
		return "PT0S"
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	var b strings.Builder
	b.WriteString("PT")
	if h > 0 {
		b.WriteString(strconv.FormatInt(h, 10))
		b.WriteString("H")
	}
	if m > 0 {
		b.WriteString(strconv.FormatInt(m, 10))
		b.WriteString("M")
	}
	if s > 0 {
		b.WriteString(strconv.FormatInt(s, 10))
		b.WriteString("S")
	}
	return b.String()
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemapDoc struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

// BuildSitemap собирает карту сайта: лендинг, архив и страницы выпусков.
func BuildSitemap(baseURL string, views []EpisodeView) ([]byte, error) {
	doc := sitemapDoc{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs: []sitemapURL{
			{Loc: baseURL + "/"},
			{Loc: baseURL + "/episodes"},
		},
	}
	for _, v := range views {
		doc.URLs = append(doc.URLs, sitemapURL{
			Loc:     baseURL + "/episodes/" + strconv.FormatInt(v.Number, 10),
			LastMod: v.PublishedAt.Format("2006-01-02"),
		})
	}
	raw, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), raw...), nil
}

// handleSitemap — карта сайта, собирается из индекса на каждый запрос.
func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	raw, err := BuildSitemap(s.cfg.BaseURL, views)
	if err != nil {
		http.Error(w, "не собрать карту сайта", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(raw)
}

// handleRobots — robots.txt с абсолютным адресом карты сайта.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\n\nSitemap: " + s.cfg.BaseURL + "/sitemap.xml\n"))
}
