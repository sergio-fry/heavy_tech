package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Chapter — глава выпуска из описания YouTube-ролика.
type Chapter struct {
	Start int64  `json:"start"` // секунды от начала
	Title string `json:"title"`
}

const (
	youtubeRefreshInterval = 6 * time.Hour
	youtubeBatchSize       = 50
)

var videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// chapterLineRe — строка главы: необязательный маркер списка, таймкод
// [ч:]мм:сс, необязательный разделитель и название.
var chapterLineRe = regexp.MustCompile(`^\s*(?:[-*]\s*)?(\d{1,2}:\d{2}:\d{2}|\d{1,2}:\d{2})\s*(?:[-–—:|]\s*)?(.+?)\s*$`)

// extractVideoID достаёт идентификатор ролика из поддерживаемых форм ссылок.
func extractVideoID(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "youtu.be":
		return cleanVideoID(strings.Trim(u.Path, "/"))
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		if id := u.Query().Get("v"); id != "" {
			return cleanVideoID(id)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 {
			switch parts[0] {
			case "shorts", "embed", "live", "v":
				return cleanVideoID(parts[1])
			}
		}
	}
	return "", false
}

func cleanVideoID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" || !videoIDRe.MatchString(id) {
		return "", false
	}
	return id, true
}

// parseChapters превращает описание ролика в главы по правилам YouTube:
// не меньше трёх строк, первая начинается с 0:00, время возрастает.
func parseChapters(description string) []Chapter {
	var out []Chapter
	for _, line := range strings.Split(description, "\n") {
		m := chapterLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		start, ok := parseChapterTimestamp(m[1])
		if !ok {
			continue
		}
		title := strings.TrimSpace(m[2])
		if title == "" {
			continue
		}
		out = append(out, Chapter{Start: start, Title: title})
	}
	if len(out) < 3 || out[0].Start != 0 {
		return nil
	}
	for i := 1; i < len(out); i++ {
		if out[i].Start <= out[i-1].Start {
			return nil
		}
	}
	return out
}

func parseChapterTimestamp(ts string) (int64, bool) {
	parts := strings.Split(ts, ":")
	var h, m, s int64
	var err error
	switch len(parts) {
	case 2:
		if m, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
			return 0, false
		}
		if s, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
			return 0, false
		}
	case 3:
		if h, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
			return 0, false
		}
		if m, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
			return 0, false
		}
		if s, err = strconv.ParseInt(parts[2], 10, 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if s >= 60 || (len(parts) == 3 && m >= 60) {
		return 0, false
	}
	return h*3600 + m*60 + s, true
}

// youtubeSyncer периодически обновляет просмотры и главы YouTube-версий.
type youtubeSyncer struct {
	store    *Store
	key      string
	endpoint string
	client   *http.Client
	now      func() time.Time
	interval time.Duration
	logf     func(string, ...any)
	trigger  chan struct{}
}

func newYouTubeSyncer(st *Store, key string) *youtubeSyncer {
	return &youtubeSyncer{
		store:    st,
		key:      key,
		endpoint: "https://www.googleapis.com/youtube/v3/videos",
		client:   &http.Client{Timeout: 10 * time.Second},
		now:      time.Now,
		interval: youtubeRefreshInterval,
		logf:     log.Printf,
		trigger:  make(chan struct{}, 1),
	}
}

// Trigger просит выполнить внеочередной цикл, не накапливая запросы.
func (y *youtubeSyncer) Trigger() {
	select {
	case y.trigger <- struct{}{}:
	default:
	}
}

// Run выполняет цикл сразу при старте и далее по тикеру и триггерам.
func (y *youtubeSyncer) Run(ctx context.Context) {
	y.cycle(ctx)
	ticker := time.NewTicker(y.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			y.cycle(ctx)
		case <-y.trigger:
			y.cycle(ctx)
		}
	}
}

func (y *youtubeSyncer) cycle(ctx context.Context) {
	if err := y.Refresh(ctx); err != nil {
		y.logf("youtube: %v", err)
	}
}

type youtubeTarget struct {
	number  int64
	videoID string
}

// Refresh обновляет снимки для эпизодов, у которых окно обновления истекло.
// Ошибки по батчам не прерывают остальные батчи.
func (y *youtubeSyncer) Refresh(ctx context.Context) error {
	targets, err := y.store.ListYouTubeTargets(y.now().Add(-y.interval))
	if err != nil {
		return fmt.Errorf("выборка эпизодов: %w", err)
	}
	var work []youtubeTarget
	for _, t := range targets {
		id, ok := extractVideoID(t.YouTubeURL)
		if !ok {
			y.logf("youtube: эпизод %d: не удалось извлечь id из %q", t.Number, t.YouTubeURL)
			continue
		}
		work = append(work, youtubeTarget{number: t.Number, videoID: id})
	}

	var firstErr error
	for start := 0; start < len(work); start += youtubeBatchSize {
		end := min(start+youtubeBatchSize, len(work))
		batch := work[start:end]
		ids := make([]string, 0, len(batch))
		seen := map[string]bool{}
		for _, item := range batch {
			if !seen[item.videoID] {
				seen[item.videoID] = true
				ids = append(ids, item.videoID)
			}
		}
		items, err := y.fetch(ctx, ids)
		if err != nil {
			y.logf("youtube: батч %d-%d: %v", start, end-1, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, item := range batch {
			video, ok := items[item.videoID]
			if !ok {
				y.logf("youtube: ролик %s (эпизод %d) не найден", item.videoID, item.number)
				continue
			}
			views, err := strconv.ParseInt(video.Statistics.ViewCount, 10, 64)
			if err != nil {
				y.logf("youtube: эпизод %d: некорректный viewCount %q", item.number, video.Statistics.ViewCount)
				continue
			}
			chapters := parseChapters(video.Snippet.Description)
			if err := y.store.SaveYouTubeMeta(item.number, item.videoID, views, chapters, y.now()); err != nil {
				y.logf("youtube: эпизод %d: не сохранить снимок: %v", item.number, err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

type youtubeItem struct {
	ID      string `json:"id"`
	Snippet struct {
		Description string `json:"description"`
	} `json:"snippet"`
	Statistics struct {
		ViewCount string `json:"viewCount"`
	} `json:"statistics"`
}

// fetch запрашивает метаданные пачки роликов. Ошибки не содержат URL с ключом.
func (y *youtubeSyncer) fetch(ctx context.Context, ids []string) (map[string]youtubeItem, error) {
	u, err := url.Parse(y.endpoint)
	if err != nil {
		return nil, errors.New("некорректный адрес YouTube API")
	}
	q := u.Query()
	q.Set("part", "snippet,statistics")
	q.Set("id", strings.Join(ids, ","))
	q.Set("maxResults", strconv.Itoa(youtubeBatchSize))
	q.Set("key", y.key)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("не удалось собрать запрос к YouTube")
	}
	resp, err := y.client.Do(req)
	if err != nil {
		// url.Error содержит полный URL с ключом: наружу отдаём только факт сбоя.
		return nil, errors.New("запрос к YouTube не удался")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube ответил %d", resp.StatusCode)
	}
	var body struct {
		Items []youtubeItem `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, errors.New("не удалось разобрать ответ YouTube")
	}
	out := make(map[string]youtubeItem, len(body.Items))
	for _, item := range body.Items {
		out[item.ID] = item
	}
	return out, nil
}
