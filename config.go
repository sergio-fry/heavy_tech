package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config — параметры запуска: флаги > ENV > дефолты.
type Config struct {
	Addr       string // адрес слушателя, например :8080
	BaseURL    string // публичный базовый URL без завершающего слэша (для абсолютных ссылок)
	PodcastDir string // папка подкастов — источник истины
	DBPath     string // путь к SQLite-базе
}

func LoadConfig() Config {
	var cfg Config
	flag.StringVar(&cfg.Addr, "addr", envOr("ADDR", ":8080"), "адрес слушателя")
	flag.StringVar(&cfg.BaseURL, "base-url", envOr("BASE_URL", "http://localhost:8080"),
		"публичный базовый URL (для абсолютных ссылок в фиде и API)")
	flag.StringVar(&cfg.PodcastDir, "podcast-dir", envOr("PODCAST_DIR", "data/podcast"),
		"папка подкастов (podcast.yaml и MP3-эпизоды)")
	flag.StringVar(&cfg.DBPath, "db", envOr("DB_PATH", "data/heavytech.db"), "путь к SQLite-базе")
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

// Channel — метаданные канала из podcast.yaml в корне папки подкастов.
type Channel struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Author      string `yaml:"author"`
	Email       string `yaml:"email"`
	Image       string `yaml:"image"`    // абсолютный URL обложки
	Category    string `yaml:"category"` // категория iTunes
	Explicit    bool   `yaml:"explicit"`
	Link        string `yaml:"link"` // ссылка канала; по умолчанию BaseURL
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
	if strings.TrimSpace(ch.Title) == "" || strings.TrimSpace(ch.Description) == "" {
		return ch, fmt.Errorf("в %s должны быть заполнены title и description", path)
	}
	return ch, nil
}
