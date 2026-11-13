package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

// staticFS — поддерево web/ для раздачи /static/*.
var staticFS = func() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return sub
}()

// botMarkers — подстроки User-Agent, запросы с которыми не считаются скачиваниями.
var botMarkers = []string{"bot", "spider", "crawler", "monitor"}

// feedState — кэш собранного фида одного вида.
type feedState struct {
	xml   []byte
	valid bool
}

// Server — HTTP-фасад над индексом и папкой. Реализует http.Server через mux.
type Server struct {
	cfg       Config
	store     *Store
	templates map[string]*template.Template
	static    http.Handler
	mux       *http.ServeMux
	now       func() time.Time // подменяется в тестах

	// AfterRescan вызывается после успешного рескана (внеочередная
	// синхронизация YouTube). Может быть nil.
	AfterRescan func()

	contentMu sync.RWMutex
	content   siteContent

	feedMu sync.Mutex
	feeds  map[string]*feedState
}

// NewServer собирает маршруты. Единственный защищаемый смысл — рескан — остаётся
// публичным: операция идемпотентна; ограничение доступа решается на прокси.
func NewServer(cfg Config, ch Channel, st *Store) (*Server, error) {
	templates, err := parsePageTemplates()
	if err != nil {
		return nil, err
	}
	about, err := LoadAbout(cfg.PodcastDir)
	if err != nil {
		// about.md необязателен: канал публикуется и без него.
		log.Printf("about.md: %v", err)
	}
	static := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	s := &Server{
		cfg:       cfg,
		store:     st,
		templates: templates,
		static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=300")
			static.ServeHTTP(w, r)
		}),
		now:     time.Now,
		content: siteContent{ch: ch, aboutRaw: about},
		feeds:   map[string]*feedState{KindAudio: {}, KindVideo: {}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /episodes", s.handleArchive)
	mux.HandleFunc("GET /episodes/{number}", s.handleEpisodePage)
	// Участники: страница /people/{slug} и файл аватара /people/{slug}/avatar.{ext}.
	// Wildcard {file} занимает целый сегмент, имя файла проверяется по конфигу участника.
	mux.HandleFunc("GET /people/{slug}", s.handlePersonPage)
	mux.HandleFunc("GET /people/{slug}/{file}", s.handleAvatar)
	mux.HandleFunc("GET /sitemap.xml", s.handleSitemap)
	mux.HandleFunc("GET /robots.txt", s.handleRobots)
	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /feed.xml", s.handleFeed(KindAudio))
	mux.HandleFunc("GET /feed-video.xml", s.handleFeed(KindVideo))
	mux.HandleFunc("GET /audio/{number}", s.handleMedia(KindAudio))
	mux.HandleFunc("GET /video/{number}", s.handleMedia(KindVideo))
	// Именованные URL: тот же носитель, что и по номеру, но с именем файла в
	// пути. Имя сегмента декоративное — поиск идёт по номеру, поэтому старые
	// ссылки после смены заголовка продолжают работать.
	mux.HandleFunc("GET /audio/{number}/{name}", s.handleMedia(KindAudio))
	mux.HandleFunc("GET /video/{number}/{name}", s.handleMedia(KindVideo))
	mux.HandleFunc("POST /track/play", s.handlePlay)
	mux.HandleFunc("GET /api/episodes", s.handleAPI)
	mux.HandleFunc("GET /api/people", s.handlePeopleAPI)
	mux.HandleFunc("POST /admin/rescan", s.handleRescan)
	s.mux = mux
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// invalidateFeed сбрасывает кэш обоих фидов (после рескана индекс мог измениться).
func (s *Server) invalidateFeed() {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	for _, f := range s.feeds {
		f.valid = false
	}
}

// parseEpisodeNumber разбирает номер эпизода из пути. Некорректный номер — 404:
// такого эпизода не существует.
func parseEpisodeNumber(r *http.Request) (int64, bool) {
	number, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

// handleMedia отдаёт носитель эпизода (audio/video) с поддержкой Range;
// успешный GET не-бота = скачивание соответствующего вида.
func (s *Server) handleMedia(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		number, ok := parseEpisodeNumber(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		ep, err := s.store.EpisodeByNumber(number)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "ошибка базы", http.StatusInternalServerError)
			return
		}
		file, ok := ep.File(kind)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			s.countDownload(r, ep.Number, kind)
		}
		f, err := os.Open(file.FilePath)
		if err != nil {
			log.Printf("%s %d: файл недоступен: %v", kind, ep.Number, err)
			http.Error(w, "файл эпизода недоступен", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", file.Mime)
		http.ServeContent(w, r, filepath.Base(file.FilePath), time.Unix(file.FileMtime, 0), f)
	}
}

// countDownload применяет правила из specs/audience-metrics: только GET,
// не пустой UA, не бот; дедупликация (эпизод, носитель, ip, ua, сутки) —
// в уникальном индексе.
func (s *Server) countDownload(r *http.Request, number int64, kind string) {
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		return
	}
	uaLower := strings.ToLower(ua)
	for _, marker := range botMarkers {
		if strings.Contains(uaLower, marker) {
			return
		}
	}
	if _, err := s.store.RecordDownload(number, kind, clientIP(r), ua, s.now()); err != nil {
		log.Printf("download %s %d: не записал событие: %v", kind, number, err)
	}
}

// clientIP берёт первый адрес X-Forwarded-For (за edge-Caddy), иначе RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type playRequest struct {
	Episode  *int64 `json:"episode"`
	Progress *int   `json:"progress"`
}

// handlePlay — бикон веб-плеера: старт воспроизведения эпизода по номеру.
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	var req playRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "ожидается JSON {episode, progress?}", http.StatusBadRequest)
		return
	}
	if req.Episode == nil || *req.Episode <= 0 {
		http.Error(w, "нужен номер эпизода episode", http.StatusBadRequest)
		return
	}
	if req.Progress != nil && (*req.Progress < 0 || *req.Progress > 100) {
		http.Error(w, "progress должен быть 0..100", http.StatusBadRequest)
		return
	}
	if _, err := s.store.EpisodeByNumber(*req.Episode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	if err := s.store.RecordPlay(*req.Episode, req.Progress, s.now()); err != nil {
		http.Error(w, "не записал событие", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFeed отдаёт кэшированный RSS по видам носителей; пересобирается после рескана.
func (s *Server) handleFeed(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.feedMu.Lock()
		cache := s.feeds[kind]
		if cache == nil {
			cache = &feedState{}
			s.feeds[kind] = cache
		}
		if !cache.valid {
			views, err := s.store.ListEpisodesWithCounts()
			if err != nil {
				s.feedMu.Unlock()
				http.Error(w, "ошибка базы", http.StatusInternalServerError)
				return
			}
			data, err := BuildFeed(s.channel(), s.cfg.BaseURL, views, kind, s.now())
			if err != nil {
				s.feedMu.Unlock()
				http.Error(w, "не собрать фид", http.StatusInternalServerError)
				return
			}
			cache.xml = data
			cache.valid = true
		}
		data := cache.xml
		s.feedMu.Unlock()
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		_, _ = w.Write(data)
	}
}

// apiMedia — носитель эпизода в машинном представлении.
type apiMedia struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	Duration int64  `json:"duration"`
}

// apiDownloads — скачивания с разбивкой по носителям и суммой.
type apiDownloads struct {
	Audio int64 `json:"audio"`
	Video int64 `json:"video"`
	Total int64 `json:"total"`
}

// apiChapter — глава выпуска в машинном представлении.
type apiChapter struct {
	Start int64  `json:"start"`
	Title string `json:"title"`
}

// apiParticipant — участник выпуска в машинном представлении. Slug, URL и
// аватар присутствуют только у участников, описанных в people/ (design D4):
// host/guests остаются без изменений для старых потребителей API.
type apiParticipant struct {
	Role   string `json:"role"` // host | guest
	Name   string `json:"name"`
	Slug   string `json:"slug,omitempty"`
	URL    string `json:"url,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

// apiEpisode — машинное представление эпизода для /api/episodes.
type apiEpisode struct {
	Number       int64            `json:"number"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`  // markdown-исходник
	Host         string           `json:"host"`         // пустая строка = не указан
	Guests       []string         `json:"guests"`       // пустой список = не указаны
	Participants []apiParticipant `json:"participants"` // роли участников с сущностями
	YouTube      string           `json:"youtube"`      // пустая строка = ссылки нет
	PublishedAt  string           `json:"published_at"`
	Media        []apiMedia       `json:"media"`
	Downloads    apiDownloads     `json:"downloads"`
	Plays        int64            `json:"plays"`
	YouTubeViews int64            `json:"youtube_views"`
	Chapters     []apiChapter     `json:"chapters"`
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
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
	byName := peopleByName(personEntities(people))
	out := make([]apiEpisode, 0, len(views))
	for _, v := range views {
		media := make([]apiMedia, 0, len(v.Files))
		for _, f := range v.Files {
			media = append(media, apiMedia{
				Kind:     f.Kind,
				URL:      MediaURL(s.cfg.BaseURL, f.Kind, v.Number, v.Title, f),
				Size:     f.Size,
				Duration: EffectiveDuration(f),
			})
		}
		guests := v.Guests
		if guests == nil {
			guests = []string{}
		}
		chapters := make([]apiChapter, 0, len(v.Chapters))
		for _, ch := range v.Chapters {
			chapters = append(chapters, apiChapter{Start: ch.Start, Title: ch.Title})
		}
		out = append(out, apiEpisode{
			Number:       v.Number,
			Title:        v.Title,
			Description:  v.Description,
			Host:         v.Host,
			Guests:       guests,
			Participants: apiParticipants(v, s.cfg.BaseURL, byName),
			YouTube:      v.YouTube,
			PublishedAt:  v.PublishedAt.Format("2006-01-02"),
			Media:        media,
			Downloads: apiDownloads{
				Audio: v.Downloads.Audio,
				Video: v.Downloads.Video,
				Total: v.Downloads.Total,
			},
			Plays:        v.Plays,
			YouTubeViews: v.YouTubeViews,
			Chapters:     chapters,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}

// apiParticipants собирает участников выпуска в машинном виде: ведущий первым,
// затем гости в сохранённом порядке; slug/url/avatar только у известных.
func apiParticipants(v EpisodeView, baseURL string, byName map[string]Person) []apiParticipant {
	var out []apiParticipant
	appendParticipant := func(role, name string) {
		p := apiParticipant{Role: role, Name: name}
		if person, ok := byName[name]; ok {
			p.Slug = person.Slug
			p.URL = person.PersonURL(baseURL)
			p.Avatar = person.AvatarURL(baseURL)
		}
		out = append(out, p)
	}
	if v.Host != "" {
		appendParticipant(RoleHost, v.Host)
	}
	for _, guest := range v.Guests {
		appendParticipant(RoleGuest, guest)
	}
	return out
}

// apiPerson — участник канала в машинном представлении /api/people.
type apiPerson struct {
	Slug     string  `json:"slug"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Avatar   string  `json:"avatar,omitempty"`
	Episodes []int64 `json:"episodes"`
}

// handlePeopleAPI — машинный список участников канала.
func (s *Server) handlePeopleAPI(w http.ResponseWriter, r *http.Request) {
	people, err := s.store.ListPeople()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	out := make([]apiPerson, 0, len(people))
	for _, v := range people {
		episodes := v.Episodes
		if episodes == nil {
			episodes = []int64{}
		}
		out = append(out, apiPerson{
			Slug:     v.Slug,
			Name:     v.Name,
			URL:      v.PersonURL(s.cfg.BaseURL),
			Avatar:   v.AvatarURL(s.cfg.BaseURL),
			Episodes: episodes,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}

// handleAvatar отдаёт файл аватара участника по каноническому имени avatar.{ext}.
// Имя файла из пути проверяется по конфигу участника: неизвестные файлы и
// участники — 404, путь собирается из данных индекса, а не из запроса.
func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	person, err := s.store.PersonBySlug(slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	if person.AvatarExt == "" || r.PathValue("file") != "avatar"+person.AvatarExt {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(person.AvatarPath)
	if err != nil {
		log.Printf("аватар %s: файл недоступен: %v", slug, err)
		http.Error(w, "файл аватара недоступен", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "файл аватара недоступен", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", avatarMimeByExt[person.AvatarExt])
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, "avatar"+person.AvatarExt, info.ModTime(), f)
}

// russianMonths — названия месяцев в родительном падеже; своя «локализация»
// из двенадцати строк вместо подключения библиотеки.
var russianMonths = [...]string{
	"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

// russianDate — дата словами по-русски: «2 октября 2026».
func russianDate(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), russianMonths[t.Month()-1], t.Year())
}

// humanSize — размер файла по-русски: «980 КБ», «12,3 МБ».
func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d Б", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"КБ", "МБ", "ГБ"}
	label := strconv.FormatFloat(float64(size)/float64(div), 'f', 1, 64)
	label = strings.TrimSuffix(strings.Replace(label, ".", ",", 1), ",0")
	return label + " " + units[exp]
}

type rescanResult struct {
	Added   int   `json:"added"`
	Updated int   `json:"updated"`
	Removed int   `json:"removed"`
	Total   int64 `json:"total"`
}

// handleRescan — ручная синхронизация папки с индексом.
func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	added, updated, removed, err := SyncPodcastDir(s.cfg.PodcastDir, s.store)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.invalidateFeed()
	s.reloadContent()
	if s.AfterRescan != nil {
		s.AfterRescan()
	}
	var total int64
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM episodes`).Scan(&total)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(rescanResult{Added: added, Updated: updated, Removed: removed, Total: total})
}

// humanDuration — секунды в «мм:сс» / «ч:мм:сс»; 0 → пустая строка.
func humanDuration(sec int64) string {
	if sec <= 0 {
		return ""
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
