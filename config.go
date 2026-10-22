package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config — параметры запуска: флаги > ENV > дефолты.
type Config struct {
	Addr          string // адрес слушателя, например :8080
	BaseURL       string // публичный базовый URL без завершающего слэша (для абсолютных ссылок)
	PodcastDir    string // папка подкастов — источник истины
	DBPath        string // путь к SQLite-базе
	YouTubeAPIKey string // ключ YouTube Data API; пусто = синхронизация отключена
}

func LoadConfig() Config {
	var cfg Config
	flag.StringVar(&cfg.Addr, "addr", envOr("ADDR", ":8080"), "адрес слушателя")
	flag.StringVar(&cfg.BaseURL, "base-url", envOr("BASE_URL", "http://localhost:8080"),
		"публичный базовый URL (для абсолютных ссылок в фиде и API)")
	flag.StringVar(&cfg.PodcastDir, "podcast-dir", envOr("PODCAST_DIR", "data/podcast"),
		"папка подкастов (podcast.yaml и папки-эпизоды с episode.yaml и аудио/видео)")
	flag.StringVar(&cfg.DBPath, "db", envOr("DB_PATH", "data/heavytech.db"), "путь к SQLite-базе")
	flag.StringVar(&cfg.YouTubeAPIKey, "youtube-api-key", envOr("YOUTUBE_API_KEY", ""),
		"ключ YouTube Data API для глав и просмотров (или YOUTUBE_API_KEY)")
	flag.Parse()
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return cfg
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ChannelLink — кнопка подписки/ссылка канала из podcast.yaml.
type ChannelLink struct {
	Label string `yaml:"label"`
	URL   string `yaml:"url"`
}

// Channel — метаданные канала из podcast.yaml в корне папки подкастов.
type Channel struct {
	Title       string        `yaml:"title"`
	Description string        `yaml:"description"`
	Author      string        `yaml:"author"`
	Email       string        `yaml:"email"`
	Image       string        `yaml:"image"`    // абсолютный URL обложки
	Category    string        `yaml:"category"` // категория iTunes
	Explicit    bool          `yaml:"explicit"`
	Link        string        `yaml:"link"`       // ссылка канала; по умолчанию BaseURL
	Tagline     string        `yaml:"tagline"`    // короткое описание для SEO и лендинга
	Links       []ChannelLink `yaml:"links"`      // кнопки подписки в порядке вывода
	StartHere   []int64       `yaml:"start_here"` // номера выпусков для подборки новичку
}

// LoadChannel читает и валидирует podcast.yaml. Отсутствие файла или пустые
// title/description — ошибка: канал без метаданных не публикуется.
func LoadChannel(dir string) (Channel, error) {
	var ch Channel
	path := filepath.Join(dir, "podcast.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ch, fmt.Errorf("нет %s — канал без метаданных не публикуется", path)
		}
		return ch, err
	}
	if err := yaml.Unmarshal(raw, &ch); err != nil {
		return ch, fmt.Errorf("не удалось разобрать %s: %w", path, err)
	}
	if err := validateChannelTypes(raw); err != nil {
		return ch, fmt.Errorf("%s: %w", path, err)
	}
	if strings.TrimSpace(ch.Title) == "" || strings.TrimSpace(ch.Description) == "" {
		return ch, fmt.Errorf("в %s должны быть заполнены title и description", path)
	}
	ch.Tagline = strings.TrimSpace(ch.Tagline)
	normalized, err := validateChannelLinks(ch.Links)
	if err != nil {
		return ch, fmt.Errorf("%s: %w", path, err)
	}
	ch.Links = normalized
	for _, number := range ch.StartHere {
		if number <= 0 {
			return ch, fmt.Errorf("%s: start_here: номер выпуска должен быть положительным, получен %d", path, number)
		}
	}
	return ch, nil
}

// validateChannelTypes — строгая типизация новых полей: yaml.v3 иначе молча
// приводит скаляры к строкам, а спека требует явные типы.
func validateChannelTypes(raw []byte) error {
	var typed struct {
		Tagline   yaml.Node `yaml:"tagline"`
		Links     yaml.Node `yaml:"links"`
		StartHere yaml.Node `yaml:"start_here"`
	}
	if err := yaml.Unmarshal(raw, &typed); err != nil {
		return err
	}
	absent := func(n yaml.Node) bool {
		return n.Kind == 0 || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
	}
	if n := typed.Tagline; !absent(n) && (n.Kind != yaml.ScalarNode || n.Tag != "!!str") {
		return fmt.Errorf("tagline: должен быть строкой")
	}
	if n := typed.Links; !absent(n) {
		if n.Kind != yaml.SequenceNode {
			return fmt.Errorf("links: должен быть списком")
		}
		for i, item := range n.Content {
			if item.Kind != yaml.MappingNode {
				return fmt.Errorf("links[%d]: должен быть объектом label/url", i)
			}
			values := map[string]yaml.Node{}
			for j := 0; j+1 < len(item.Content); j += 2 {
				values[item.Content[j].Value] = *item.Content[j+1]
			}
			for _, key := range []string{"label", "url"} {
				node, ok := values[key]
				if !ok || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
					return fmt.Errorf("links[%d].%s: должен быть строкой", i, key)
				}
			}
		}
	}
	if n := typed.StartHere; !absent(n) {
		if n.Kind != yaml.SequenceNode {
			return fmt.Errorf("start_here: должен быть списком номеров")
		}
		for i, item := range n.Content {
			if item.Kind != yaml.ScalarNode || item.Tag != "!!int" {
				return fmt.Errorf("start_here[%d]: должен быть целым числом", i)
			}
		}
	}
	return nil
}

// validateChannelLinks проверяет ссылки подписки: непустой label и абсолютный
// http(s)-адрес с хостом. Возвращает значения после трима.
func validateChannelLinks(links []ChannelLink) ([]ChannelLink, error) {
	out := make([]ChannelLink, 0, len(links))
	for i, link := range links {
		link.Label = strings.TrimSpace(link.Label)
		link.URL = strings.TrimSpace(link.URL)
		if link.Label == "" {
			return nil, fmt.Errorf("links[%d]: пустой label", i)
		}
		u, err := url.Parse(link.URL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("links[%d]: нужен абсолютный http(s)-URL, получено %q", i, link.URL)
		}
		out = append(out, link)
	}
	return out, nil
}
