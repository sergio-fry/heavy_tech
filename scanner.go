package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// epFileRe — конвенция имени эпизода: YYYY-MM-DD-slug.mp3, слаг из [a-z0-9-].
var epFileRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-([a-z0-9]+(?:-[a-z0-9]+)*)\.mp3$`)

// ScanFolder читает папку подкастов и возвращает желаемое состояние индекса.
// Файлы, не соответствующие конвенции имени, игнорируются. Дублирующийся слаг
// (одинаковый slug у двух файлов) — ошибка конфигурации папки.
func ScanFolder(dir string) ([]Episode, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("не открыть папку подкастов %s: %w", dir, err)
	}
	var found []Episode
	seenSlugs := map[string]string{} // slug -> имя файла (для сообщения о дубле)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		m := epFileRe.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		date, err := time.Parse("2006-01-02", m[1])
		if err != nil {
			// regexp допустил, календарь — нет (например, 2026-02-30).
			continue
		}
		slug := m[2]
		if prev, dup := seenSlugs[slug]; dup {
			return nil, fmt.Errorf("дублирующийся слаг %q: %s и %s", slug, prev, entry.Name())
		}
		seenSlugs[slug] = entry.Name()

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		ep := Episode{
			Slug:        slug,
			Title:       DefaultTitle(slug),
			PublishedAt: date,
			FilePath:    filepath.Join(dir, entry.Name()),
			FileSize:    info.Size(),
			Mime:        "audio/mpeg",
			FileMtime:   info.ModTime().Unix(),
		}
		sc, err := readSidecar(dir, slug)
		if err != nil {
			return nil, err
		}
		if sc != nil {
			if strings.TrimSpace(sc.Title) != "" {
				ep.Title = strings.TrimSpace(sc.Title)
			}
			ep.Description = strings.TrimSpace(sc.Description)
			if sc.Duration > 0 {
				ep.DurationSeconds = sc.Duration
			}
		}
		found = append(found, ep)
	}
	return found, nil
}

// sidecar — поля опционального файла slug.yaml рядом с аудиофайлом.
type sidecar struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Duration    int64  `yaml:"duration"` // секунды
}

// readSidecar возвращает nil, если sidecar-файла нет; битый YAML — ошибка.
func readSidecar(dir, slug string) (*sidecar, error) {
	path := filepath.Join(dir, slug+".yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sc sidecar
	if err := yaml.Unmarshal(raw, &sc); err != nil {
		return nil, fmt.Errorf("не удалось разобрать %s: %w", path, err)
	}
	return &sc, nil
}

// DefaultTitle выводит заголовок из слага: слова из дефисов, первая буква заглавная.
func DefaultTitle(slug string) string {
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// SyncPodcastDir сканирует папку и приводит индекс в соответствие с ней.
func SyncPodcastDir(dir string, st *Store) (added, updated, removed int, err error) {
	found, err := ScanFolder(dir)
	if err != nil {
		return 0, 0, 0, err
	}
	return st.Sync(found)
}
