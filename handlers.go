package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

// botMarkers — подстроки User-Agent, запросы с которыми не считаются скачиваниями.
var botMarkers = []string{"bot", "spider", "crawler", "monitor"}

// Server — HTTP-фасад над индексом и папкой. Реализует http.Server через mux.
type Server struct {
	cfg   Config
	ch    Channel
	store *Store
	tmpl  *template.Template
	mux   *http.ServeMux
	now   func() time.Time // подменяется в тестах

	feedMu    sync.Mutex
	feedXML   []byte
	feedValid bool
}

// NewServer собирает маршруты. Единственный защищаемый смысл — рескан — остаётся
// публичным: операция идемпотентна; ограничение доступа решается на прокси.
func NewServer(cfg Config, ch Channel, st *Store) (*Server, error) {
	tmpl, err := template.ParseFS(webFS, "web/index.html")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:   cfg,
		ch:    ch,
		store: st,
		tmpl:  tmpl,
		now:   time.Now,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /feed.xml", s.handleFeed)
	mux.HandleFunc("GET /audio/{slug}", s.handleAudio)
	mux.HandleFunc("POST /track/play", s.handlePlay)
	mux.HandleFunc("GET /api/episodes", s.handleAPI)
	mux.HandleFunc("POST /admin/rescan", s.handleRescan)
	s.mux = mux
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// invalidateFeed сбрасывает кэш фида (после рескана индекс мог измениться).
func (s *Server) invalidateFeed() {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	s.feedValid = false
}

// handleAudio отдаёт аудио с поддержкой Range; успешный GET не-бота = скачивание.
func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	ep, err := s.store.EpisodeBySlug(r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		s.countDownload(r, ep)
	}
	f, err := os.Open(ep.FilePath)
	if err != nil {
		log.Printf("audio %s: файл недоступен: %v", ep.Slug, err)
		http.Error(w, "файл эпизода недоступен", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ep.Mime)
	http.ServeContent(w, r, filepath.Base(ep.FilePath), time.Unix(ep.FileMtime, 0), f)
}

// countDownload применяет правила из specs/audience-metrics: только GET,
// не пустой UA, не бот; дедупликация (эпизод, ip, ua, сутки) — в уникальном индексе.
func (s *Server) countDownload(r *http.Request, ep Episode) {
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
	if _, err := s.store.RecordDownload(ep.ID, clientIP(r), ua, s.now()); err != nil {
		log.Printf("download %s: не записал событие: %v", ep.Slug, err)
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
	Slug     string `json:"slug"`
	Progress *int   `json:"progress"`
}

// handlePlay — бикон веб-плеера: старт воспроизведения эпизода.
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	var req playRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "ожидается JSON {slug, progress?}", http.StatusBadRequest)
		return
	}
	if req.Progress != nil && (*req.Progress < 0 || *req.Progress > 100) {
		http.Error(w, "progress должен быть 0..100", http.StatusBadRequest)
		return
	}
	ep, err := s.store.EpisodeBySlug(req.Slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	if err := s.store.RecordPlay(ep.ID, req.Progress, s.now()); err != nil {
		http.Error(w, "не записал событие", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFeed отдаёт кэшированный RSS; пересобирается после рескана.
func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	s.feedMu.Lock()
	if !s.feedValid {
		views, err := s.store.ListEpisodesWithCounts()
		if err != nil {
			s.feedMu.Unlock()
			http.Error(w, "ошибка базы", http.StatusInternalServerError)
			return
		}
		data, err := BuildFeed(s.ch, s.cfg.BaseURL, views, s.now())
		if err != nil {
			s.feedMu.Unlock()
			http.Error(w, "не собрать фид", http.StatusInternalServerError)
			return
		}
		s.feedXML = data
		s.feedValid = true
	}
	data := s.feedXML
	s.feedMu.Unlock()
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	_, _ = w.Write(data)
}

// apiEpisode — машинное представление эпизода для /api/episodes.
type apiEpisode struct {
	Slug            string `json:"slug"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	PublishedAt     string `json:"published_at"`
	DurationSeconds int64  `json:"duration_seconds"`
	SizeBytes       int64  `json:"size_bytes"`
	Downloads       int64  `json:"downloads"`
	Plays           int64  `json:"plays"`
	AudioURL        string `json:"audio_url"`
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	out := make([]apiEpisode, 0, len(views))
	for _, v := range views {
		out = append(out, apiEpisode{
			Slug:            v.Slug,
			Title:           v.Title,
			Description:     v.Description,
			PublishedAt:     v.PublishedAt.Format("2006-01-02"),
			DurationSeconds: EffectiveDuration(v.Episode),
			SizeBytes:       v.FileSize,
			Downloads:       v.Downloads,
			Plays:           v.Plays,
			AudioURL:        s.cfg.BaseURL + "/audio/" + v.Slug,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
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

// episodeItem — данные одного эпизода для HTML-страницы.
type episodeItem struct {
	Slug            string
	Title           string
	Description     string
	DateLabel       string
	DurationSeconds int64
	DurationLabel   string
	AudioURL        string
	Downloads       int64
	Plays           int64
	Number          int // номер выпуска: 1 у самого старого
}

type pageData struct {
	Title       string
	Description string
	HasImage    bool
	Image       string
	BaseURL     string
	LatestSlug  string // slug самого нового эпизода; пусто, если эпизодов нет
	FeedURL     string
	Episodes    []episodeItem
}

// buildPageData — сборка данных HTML-страницы из метаданных канала и индекса
// (эпизоды приходят от новых к старым).
func buildPageData(ch Channel, baseURL string, views []EpisodeView) pageData {
	data := pageData{
		Title:       ch.Title,
		Description: ch.Description,
		HasImage:    ch.Image != "",
		Image:       ch.Image,
		BaseURL:     baseURL,
		FeedURL:     baseURL + "/feed.xml",
	}
	for i, v := range views {
		if i == 0 {
			data.LatestSlug = v.Slug
		}
		data.Episodes = append(data.Episodes, episodeItem{
			Slug:            v.Slug,
			Title:           v.Title,
			Description:     v.Description,
			DateLabel:       russianDate(v.PublishedAt),
			DurationSeconds: EffectiveDuration(v.Episode),
			DurationLabel:   humanDuration(EffectiveDuration(v.Episode)),
			AudioURL:        baseURL + "/audio/" + v.Slug,
			Downloads:       v.Downloads,
			Plays:           v.Plays,
			Number:          len(views) - i,
		})
	}
	return data
}

// handleHome — сайт: список эпизодов с плеером и счётчиками.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	views, err := s.store.ListEpisodesWithCounts()
	if err != nil {
		http.Error(w, "ошибка базы", http.StatusInternalServerError)
		return
	}
	data := buildPageData(s.ch, s.cfg.BaseURL, views)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, data); err != nil {
		log.Printf("home: шаблон: %v", err)
	}
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
