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

// participantItem — участник в линии «Ведущий:/Гости:»: имя известного
// участника — ссылка на его страницу, неизвестного — обычный текст.
type participantItem struct {
	Name string
	URL  string // пусто = участник не описан в people/, рисуется текстом
}

// episodeItem — данные одного эпизода для HTML-страниц.
type episodeItem struct {
	Number             int64
	Title              string
	Hosts              []participantItem // линия «Ведущий:»; пусто = не рисуется
	Guests             []participantItem // линия «Гости:» в сохранённом порядке
	HostsText          string            // имена ведущих текстом (архивная строка без вложенных ссылок)
	GuestsText         string            // имена гостей текстом
	Description        template.HTML     // отрендеренный markdown; пусто = описания нет
	Summary            string            // короткий текст для списков
	YouTube            string            // пусто = ссылки нет
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

// personItem — данные участника для страницы /people/{slug}.
type personItem struct {
	Slug        string
	Name        string
	Initial     string // первая буква имени — заглушка вместо аватара
	HasAvatar   bool
	AvatarURL   string
	Description template.HTML // отрендеренный markdown; пусто = описания нет
}

// personEpisodeItem — выпуск на странице участника: карточка с ролью участника.
type personEpisodeItem struct {
	episodeItem
	RoleLabel string // «Ведущий», «Гость»; пусто = роль не определена
}

// roleLabels — подписи ролей участника для страницы участника.
var roleLabels = map[string]string{RoleHost: "Ведущий", RoleGuest: "Гость"}

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
	Total     int

	Episode *episodeItem
	Latest  *episodeItem
	Prev    *episodeItem
	Next    *episodeItem

	Person         *personItem
	PersonEpisodes []personEpisodeItem
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

// peopleByName — индекс «имя участника → участник» для связывания выпусков.
func peopleByName(people []Person) map[string]Person {
	out := make(map[string]Person, len(people))
	for _, p := range people {
		out[p.Name] = p
	}
	return out
}

// participantItems переводит имена участников выпуска в элементы линии:
// известный участник получает URL страницы (design D5), неизвестный остаётся
// текстом — имена без сущности не ломают вывод.
func participantItems(names []string, baseURL string, byName map[string]Person) []participantItem {
	if len(names) == 0 {
		return nil
	}
	out := make([]participantItem, 0, len(names))
	for _, name := range names {
		item := participantItem{Name: name}
		if p, ok := byName[name]; ok {
			item.URL = p.PersonURL(baseURL)
		}
		out = append(out, item)
	}
	return out
}

// participantNames — имена из списка участников одной строкой через запятую.
func participantNames(items []participantItem) string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	return strings.Join(names, ", ")
}

// newEpisodeItem собирает представление эпизода для списков и страницы.
func newEpisodeItem(v EpisodeView, baseURL string, byName map[string]Person) episodeItem {
	var hosts []participantItem
	if v.Host != "" {
		hosts = participantItems([]string{v.Host}, baseURL, byName)
	}
	guests := participantItems(v.Guests, baseURL, byName)
	item := episodeItem{
		Number:         v.Number,
		Title:          v.Title,
		Hosts:          hosts,
		Guests:         guests,
		HostsText:      participantNames(hosts),
		GuestsText:     participantNames(guests),
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
func buildHomeData(ch Channel, about template.HTML, views []EpisodeView, people []Person, baseURL string) pageData {
	byName := peopleByName(people)
	data := basePage(ch, baseURL, "home", "/")
	data.Title = ch.Title
	if ch.Tagline != "" {
		data.Title = ch.Title + " — " + ch.Tagline
	}
	data.About = about
	data.Total = len(views)

	items := make([]episodeItem, 0, len(views))
	for _, v := range views {
		item := newEpisodeItem(v, baseURL, byName)
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
func buildArchiveData(ch Channel, views []EpisodeView, people []Person, baseURL string) pageData {
	byName := peopleByName(people)
	data := basePage(ch, baseURL, "archive", "/episodes")
	data.Title = "Все выпуски — " + ch.Title
	data.MetaDescription = "Все выпуски подкаста " + ch.Title + ": " + strconv.Itoa(len(views)) + " записей."
	for _, v := range views {
		data.Episodes = append(data.Episodes, newEpisodeItem(v, baseURL, byName))
	}
	data.Total = len(views)
	return data
}

// buildEpisodePageData — страница одного выпуска и его соседи по порядку публикации.
func buildEpisodePageData(ch Channel, views []EpisodeView, people []Person, number int64, baseURL string) (pageData, bool) {
	byName := peopleByName(people)
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
	item := newEpisodeItem(views[index], baseURL, byName)
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
		next := newEpisodeItem(views[index-1], baseURL, byName)
		data.Next = &next
	}
	if index+1 < len(views) {
		prev := newEpisodeItem(views[index+1], baseURL, byName)
		data.Prev = &prev
	}
	data.JSONLD = episodeJSONLD(item, ch, baseURL)
	return data, true
}

// buildPersonPageData — страница участника: аватар, имя, описание и выпуски
// с участием новыми сверху, каждая запись с ролью участника (design D6).
func buildPersonPageData(ch Channel, p PersonView, views []EpisodeView, roles map[int64]string, people []Person, baseURL string) pageData {
	byName := peopleByName(people)
	data := basePage(ch, baseURL, "person", "/people/"+p.Slug)
	data.Title = p.Name + " — " + ch.Title
	data.OGTitle = p.Name
	data.MetaDescription = truncateRunes(plainText(p.Description), 160)
	if data.MetaDescription == "" {
		data.MetaDescription = truncateRunes(plainText(ch.Description), 160)
	}
	data.OGDescription = data.MetaDescription
	item := personItem{
		Slug:        p.Slug,
		Name:        p.Name,
		Initial:     personInitial(p.Name),
		HasAvatar:   p.AvatarExt != "",
		AvatarURL:   p.AvatarURL(baseURL),
		Description: RenderDescription(p.Description),
	}
	data.Person = &item
	for _, v := range views {
		pi := personEpisodeItem{
			episodeItem: newEpisodeItem(v, baseURL, byName),
			RoleLabel:   roleLabels[roles[v.Number]],
		}
		data.PersonEpisodes = append(data.PersonEpisodes, pi)
	}
	return data
}

// personInitial — первая буква имени в верхнем регистре: заглушка аватара.
func personInitial(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	r := []rune(name)[0]
	return strings.ToUpper(string(r))
}

// handleHome — лендинг канала.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	people, err := s.store.ListPeople()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	ch, about := s.lockContent()
	data := buildHomeData(ch, about, views, personEntities(people), s.cfg.BaseURL)
	data.Subscribe = ch.Links
	s.render(w, "home", data)
}

// personEntities — участники списком сущностей (без выпусков) для шаблонов.
func personEntities(views []PersonView) []Person {
	out := make([]Person, 0, len(views))
	for _, v := range views {
		out = append(out, v.Person)
	}
	return out
}

// handleArchive — полный архив выпусков.
func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	people, err := s.store.ListPeople()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	ch, _ := s.lockContent()
	s.render(w, "episodes", buildArchiveData(ch, views, personEntities(people), s.cfg.BaseURL))
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
	people, err := s.store.ListPeople()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	ch, _ := s.lockContent()
	data, ok := buildEpisodePageData(ch, views, personEntities(people), number, s.cfg.BaseURL)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "episode", data)
}

// handlePersonPage — страница участника; неизвестный slug — 404.
func (s *Server) handlePersonPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	views, err := s.store.ListPeople()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	var person PersonView
	found := false
	for _, v := range views {
		if v.Slug == slug {
			person = v
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	episodes, roles, err := s.store.PersonEpisodes(slug)
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	ch, _ := s.lockContent()
	data := buildPersonPageData(ch, person, episodes, roles, personEntities(views), s.cfg.BaseURL)
	s.render(w, "person", data)
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
		"person":   "person.html",
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
