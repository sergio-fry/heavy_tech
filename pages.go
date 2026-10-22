package main

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// mediaLabels — подписи видов носителей для сайта.
var mediaLabels = map[string]string{KindAudio: "Аудио", KindVideo: "Видео"}

// mediaItem — ссылка «скачать» на носитель.
type mediaItem struct {
	Kind      string
	Label     string
	URL       string
	Size      int64
	SizeLabel string
}

// chapterItem — глава в представлении страницы.
type chapterItem struct {
	Start   int64
	Title   string
	Label   string  // «12:30»
	Percent float64 // положение на полосе, 0..99
}

// episodeItem — данные одного эпизода для HTML-страниц.
type episodeItem struct {
	Number             int64
	Title              string
	Host               string        // пусто = линия «Ведущий:» не рисуется
	Guests             []string      // пусто = линия «Гости:» не рисуется
	GuestsLabel        string        // гости через запятую
	Description        template.HTML // отрендеренный markdown; пусто = описания нет
	Summary            string        // короткий текст для списков
	SearchText         string        // для клиентского поиска, в нижнем регистре
	YouTube            string        // пусто = ссылки нет
	DateLabel          string
	DateISO            string
	Year               int
	HasAudio           bool
	HasVideo           bool
	AudioURL           string
	AudioDurationSecs  int64
	AudioDurationLabel string
	Media              []mediaItem
	Downloads          int64
	DownloadsAudio     int64
	DownloadsVideo     int64
	Plays              int64
	YouTubeViews       int64
	Engagement         int64 // downloads.total + plays + youtube views
	PageURL            string
	Chapters           []chapterItem
}

// pageData — общие данные любой HTML-страницы плюс поля конкретного вида.
type pageData struct {
	PageKind        string
	Title           string
	ChannelTitle    string
	Description     string
	Tagline         string
	HasImage        bool
	Image           string
	BaseURL         string
	FeedURL         string
	VideoFeedURL    string
	Canonical       string
	MetaDescription string
	OGType          string
	OGTitle         string
	OGDescription   string
	JSONLD          template.JS
	Subscribe       []ChannelLink
	LatestNumber    int64
	About           template.HTML

	Episodes  []episodeItem
	StartHere []episodeItem
	Years     []int
	Total     int

	Episode *episodeItem
	Latest  *episodeItem
	Prev    *episodeItem
	Next    *episodeItem
}

// chapterTime — «0:00», «12:30», «1:02:10»; ноль не пустой, в отличие от humanDuration.
func chapterTime(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return formatInt(h) + ":" + pad2(m) + ":" + pad2(s)
	}
	return formatInt(m) + ":" + pad2(s)
}

func formatInt(v int64) string { return strconv.FormatInt(v, 10) }

func pad2(v int64) string {
	if v < 10 {
		return "0" + strconv.FormatInt(v, 10)
	}
	return strconv.FormatInt(v, 10)
}

// newEpisodeItem собирает представление эпизода для списков и страницы.
func newEpisodeItem(v EpisodeView, baseURL string) episodeItem {
	item := episodeItem{
		Number:         v.Number,
		Title:          v.Title,
		Host:           v.Host,
		Guests:         v.Guests,
		GuestsLabel:    strings.Join(v.Guests, ", "),
		Description:    RenderDescription(v.Description),
		Summary:        truncateRunes(plainText(v.Description), 220),
		YouTube:        v.YouTube,
		DateLabel:      russianDate(v.PublishedAt),
		DateISO:        v.PublishedAt.Format("2006-01-02"),
		Year:           v.PublishedAt.Year(),
		Downloads:      v.Downloads.Total,
		DownloadsAudio: v.Downloads.Audio,
		DownloadsVideo: v.Downloads.Video,
		Plays:          v.Plays,
		YouTubeViews:   v.YouTubeViews,
		Engagement:     v.Downloads.Total + v.Plays + v.YouTubeViews,
		PageURL:        baseURL + "/episodes/" + strconv.FormatInt(v.Number, 10),
	}
	search := []string{strconv.FormatInt(v.Number, 10), v.Title, plainText(v.Description), item.GuestsLabel}
	for _, f := range v.Files {
		item.Media = append(item.Media, mediaItem{
			Kind:      f.Kind,
			Label:     mediaLabels[f.Kind],
			URL:       MediaURL(baseURL, f.Kind, v.Number, v.Title, f),
			Size:      f.Size,
			SizeLabel: humanSize(f.Size),
		})
		switch f.Kind {
		case KindAudio:
			item.HasAudio = true
			item.AudioURL = MediaURL(baseURL, KindAudio, v.Number, v.Title, f)
			item.AudioDurationSecs = EffectiveDuration(f)
			item.AudioDurationLabel = humanDuration(item.AudioDurationSecs)
		case KindVideo:
			item.HasVideo = true
		}
	}
	item.SearchText = strings.ToLower(strings.Join(search, " "))
	for _, ch := range v.Chapters {
		percent := 0.0
		if item.AudioDurationSecs > 0 {
			percent = float64(ch.Start) * 100 / float64(item.AudioDurationSecs)
			if percent > 99 {
				percent = 99
			}
		}
		item.Chapters = append(item.Chapters, chapterItem{
			Start:   ch.Start,
			Title:   ch.Title,
			Label:   chapterTime(ch.Start),
			Percent: percent,
		})
	}
	return item
}

// basePage заполняет общие поля страницы.
func basePage(ch Channel, baseURL, kind, path string) pageData {
	canonical := baseURL + path
	return pageData{
		PageKind:        kind,
		ChannelTitle:    ch.Title,
		Description:     ch.Description,
		Tagline:         ch.Tagline,
		HasImage:        ch.Image != "",
		Image:           ch.Image,
		BaseURL:         baseURL,
		FeedURL:         baseURL + "/feed.xml",
		VideoFeedURL:    baseURL + "/feed-video.xml",
		Canonical:       canonical,
		OGType:          "website",
		OGTitle:         ch.Title,
		OGDescription:   ch.Description,
		Subscribe:       ch.Links,
		MetaDescription: truncateRunes(plainText(firstNonEmpty(ch.Tagline, ch.Description)), 160),
	}
}

// buildHomeData — лендинг: промо, три последних выпуска и подборка start_here.
func buildHomeData(ch Channel, about template.HTML, views []EpisodeView, baseURL string) pageData {
	data := basePage(ch, baseURL, "home", "/")
	data.Title = ch.Title
	if ch.Tagline != "" {
		data.Title = ch.Title + " — " + ch.Tagline
	}
	data.About = about
	data.Total = len(views)

	items := make([]episodeItem, 0, len(views))
	for _, v := range views {
		item := newEpisodeItem(v, baseURL)
		items = append(items, item)
		if data.LatestNumber == 0 && item.HasAudio {
			data.LatestNumber = v.Number
			latest := item
			data.Latest = &latest
		}
	}
	if len(items) > 3 {
		data.Episodes = items[:3]
	} else {
		data.Episodes = items
	}

	byNumber := make(map[int64]episodeItem, len(items))
	for _, item := range items {
		byNumber[item.Number] = item
	}
	for _, number := range ch.StartHere {
		if item, ok := byNumber[number]; ok {
			data.StartHere = append(data.StartHere, item)
		} else {
			log.Printf("start_here: выпуск %d не найден, пропущен", number)
		}
	}
	data.JSONLD = seriesJSONLD(ch, baseURL)
	return data
}

// buildArchiveData — архив всех выпусков с данными для клиентского поиска.
func buildArchiveData(ch Channel, views []EpisodeView, baseURL string) pageData {
	data := basePage(ch, baseURL, "archive", "/episodes")
	data.Title = "Все выпуски — " + ch.Title
	data.MetaDescription = "Все выпуски подкаста " + ch.Title + ": " + strconv.Itoa(len(views)) + " записей."
	seenYears := map[int]bool{}
	for _, v := range views {
		item := newEpisodeItem(v, baseURL)
		data.Episodes = append(data.Episodes, item)
		year := v.PublishedAt.Year()
		if !seenYears[year] {
			seenYears[year] = true
			data.Years = append(data.Years, year)
		}
	}
	data.Total = len(views)
	return data
}

// buildEpisodePageData — страница одного выпуска и его соседи по порядку публикации.
func buildEpisodePageData(ch Channel, views []EpisodeView, number int64, baseURL string) (pageData, bool) {
	index := -1
	for i, v := range views {
		if v.Number == number {
			index = i
			break
		}
	}
	if index < 0 {
		return pageData{}, false
	}
	item := newEpisodeItem(views[index], baseURL)
	data := basePage(ch, baseURL, "episode", "/episodes/"+strconv.FormatInt(number, 10))
	data.Title = item.Title + " — " + ch.Title
	data.OGType = "article"
	data.OGTitle = item.Title
	data.MetaDescription = truncateRunes(plainText(views[index].Description), 160)
	if data.MetaDescription == "" {
		data.MetaDescription = truncateRunes(plainText(ch.Description), 160)
	}
	data.OGDescription = data.MetaDescription
	data.Episode = &item
	if index > 0 {
		next := newEpisodeItem(views[index-1], baseURL)
		data.Next = &next
	}
	if index+1 < len(views) {
		prev := newEpisodeItem(views[index+1], baseURL)
		data.Prev = &prev
	}
	data.JSONLD = episodeJSONLD(item, ch, baseURL)
	return data, true
}

// handleHome — лендинг канала.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	ch, about := s.lockContent()
	s.render(w, "home", buildHomeData(ch, about, views, s.cfg.BaseURL))
}

// handleArchive — полный архив выпусков.
func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	ch, _ := s.lockContent()
	s.render(w, "episodes", buildArchiveData(ch, views, s.cfg.BaseURL))
}

// handleEpisodePage — страница выпуска; неизвестный номер — 404.
func (s *Server) handleEpisodePage(w http.ResponseWriter, r *http.Request) {
	number, ok := parseEpisodeNumber(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	ch, _ := s.lockContent()
	data, ok := buildEpisodePageData(ch, views, number, s.cfg.BaseURL)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "episode", data)
}

func (s *Server) render(w http.ResponseWriter, page string, data pageData) {
	tmpl, ok := s.templates[page]
	if !ok {
		http.Error(w, "шаблон не найден", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("%s: шаблон: %v", page, err)
	}
}

// parsePageTemplates собирает по одному набору на страницу: layout + content.
func parsePageTemplates() (map[string]*template.Template, error) {
	pages := map[string]string{
		"home":     "home.html",
		"episodes": "episodes.html",
		"episode":  "episode.html",
	}
	out := make(map[string]*template.Template, len(pages))
	for name, file := range pages {
		tmpl, err := template.ParseFS(webFS, "web/layout.html", "web/"+file)
		if err != nil {
			return nil, err
		}
		out[name] = tmpl
	}
	return out, nil
}

// firstNonEmpty возвращает первое непустое значение.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
