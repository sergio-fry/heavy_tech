package main

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// episodeFolderRe — имя папки-эпизода: номер без ведущих нулей и без 0.
var episodeFolderRe = regexp.MustCompile(`^[1-9][0-9]*$`)

// digitsFolderRe ловит «похоже на номер, но невалидно»: 0, 007, 00.
var digitsFolderRe = regexp.MustCompile(`^[0-9]+$`)

// metaFileName — обязательный манифест эпизода внутри папки.
const metaFileName = "episode.yaml"

// descriptionFileName — необязательное markdown-описание выпуска в папке эпизода.
// Приоритетнее поля description: в episode.yaml.
const descriptionFileName = "description.md"

// mediaFormat — классификация носителя по расширению (design D3):
// единственное место правды о виде и MIME.
type mediaFormat struct {
	Kind string
	Mime string
}

var mediaByExt = map[string]mediaFormat{
	".mp3":  {Kind: KindAudio, Mime: "audio/mpeg"},
	".aac":  {Kind: KindAudio, Mime: "audio/aac"},
	".m4a":  {Kind: KindAudio, Mime: "audio/x-m4a"},
	".mp4":  {Kind: KindVideo, Mime: "video/mp4"},
	".mov":  {Kind: KindVideo, Mime: "video/quicktime"},
	".mkv":  {Kind: KindVideo, Mime: "video/x-matroska"},
	".webm": {Kind: KindVideo, Mime: "video/webm"},
}

// mediaMeta — секция audio:/video: в episode.yaml. Характеристики информационные.
type mediaMeta struct {
	File        string `yaml:"file"`
	Duration    int64  `yaml:"duration"`     // секунды
	BitrateKbps int64  `yaml:"bitrate_kbps"` // kbps
	Resolution  string `yaml:"resolution"`
}

// episodeMeta — содержимое обязательного episode.yaml.
type episodeMeta struct {
	Title       string     `yaml:"title"`
	Description string     `yaml:"description"`  // legacy-фолбэк, если нет description.md
	YouTube     string     `yaml:"youtube"`      // ссылка на видеоверсию выпуска
	PublishedAt string     `yaml:"published_at"` // YYYY-MM-DD
	Audio       *mediaMeta `yaml:"audio"`
	Video       *mediaMeta `yaml:"video"`
}

// ScanFolder читает папку подкастов и возвращает желаемое состояние индекса.
// Эпизод — подпапка с числовым именем; нецифровые папки игнорируются. Любое
// нарушение конвенции (нет meta, битый YAML, несуществующий файл, лишний файл,
// некорректный номер) — ошибка синхронизации, а не тихий пропуск.
func ScanFolder(dir string) ([]Episode, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("не открыть папку подкастов %s: %w", dir, err)
	}
	var found []Episode
	for _, entry := range entries {
		if !entry.IsDir() {
			// Плоские файлы (podcast.yaml, обложка, остатки прежнего layout) не эпизоды.
			continue
		}
		name := entry.Name()
		if !episodeFolderRe.MatchString(name) {
			if digitsFolderRe.MatchString(name) {
				return nil, fmt.Errorf("папка %q: номер выпуска должен быть без ведущих нулей и не 0", name)
			}
			continue // нецифровые папки — не эпизоды
		}
		number, err := parseNumber(name)
		if err != nil {
			return nil, err
		}
		ep, err := scanEpisodeFolder(dir, name, number)
		if err != nil {
			return nil, err
		}
		found = append(found, ep)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Number < found[j].Number })
	return found, nil
}

// parseNumber переводит имя папки в номер; имя уже проверено regexp-ом.
func parseNumber(name string) (int64, error) {
	var number int64
	for _, r := range name {
		number = number*10 + int64(r-'0')
		if number < 0 {
			return 0, fmt.Errorf("папка %q: номер выпуска слишком велик", name)
		}
	}
	return number, nil
}

// scanEpisodeFolder разбирает одну папку-эпизод и валидирует её meta и файлы.
func scanEpisodeFolder(root, name string, number int64) (Episode, error) {
	dir := filepath.Join(root, name)
	metaPath := filepath.Join(dir, metaFileName)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Episode{}, fmt.Errorf("папка %q: нет обязательного %s", name, metaFileName)
		}
		return Episode{}, fmt.Errorf("папка %q: %w", name, err)
	}
	var meta episodeMeta
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return Episode{}, fmt.Errorf("не удалось разобрать %s: %w", metaPath, err)
	}
	if strings.TrimSpace(meta.Title) == "" {
		return Episode{}, fmt.Errorf("%s: пустой title", metaPath)
	}
	publishedAt, err := time.Parse("2006-01-02", strings.TrimSpace(meta.PublishedAt))
	if err != nil {
		return Episode{}, fmt.Errorf("%s: published_at должен быть YYYY-MM-DD, получено %q", metaPath, meta.PublishedAt)
	}
	if meta.Audio == nil && meta.Video == nil {
		return Episode{}, fmt.Errorf("%s: нужна хотя бы одна секция audio: или video:", metaPath)
	}
	youtube, err := validateYouTube(meta.YouTube)
	if err != nil {
		return Episode{}, fmt.Errorf("%s: %w", metaPath, err)
	}

	referenced := map[string]bool{metaFileName: true}
	description, err := resolveDescription(dir, name, meta.Description, referenced)
	if err != nil {
		return Episode{}, err
	}

	ep := Episode{
		Number:      number,
		Title:       strings.TrimSpace(meta.Title),
		Description: description,
		YouTube:     youtube,
		PublishedAt: publishedAt,
	}
	for _, section := range []struct {
		kind string
		meta *mediaMeta
	}{
		{KindAudio, meta.Audio},
		{KindVideo, meta.Video},
	} {
		if section.meta == nil {
			continue
		}
		file, err := resolveMedia(dir, name, section.kind, section.meta)
		if err != nil {
			return Episode{}, err
		}
		ep.Files = append(ep.Files, file)
		referenced[filepath.Base(section.meta.File)] = true
	}

	// Любой недот-файл/подпапка, не упомянутые в meta, — сигнал об ошибке:
	// молчаливое игнорирование уже маскировало опечатки. Dot-файлы (scp-временки) — игнор.
	folderEntries, err := os.ReadDir(dir)
	if err != nil {
		return Episode{}, fmt.Errorf("папка %q: %w", name, err)
	}
	for _, entry := range folderEntries {
		if strings.HasPrefix(entry.Name(), ".") || referenced[entry.Name()] {
			continue
		}
		return Episode{}, fmt.Errorf("папка %q: файл %q не упомянут в %s", name, entry.Name(), metaFileName)
	}
	return ep, nil
}

// resolveDescription выбирает источник описания: description.md приоритетнее поля
// description: в meta. Файл помечается известным, чтобы не считаться лишним.
func resolveDescription(dir, folder, metaDescription string, referenced map[string]bool) (string, error) {
	path := filepath.Join(dir, descriptionFileName)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.TrimSpace(metaDescription) != "" {
			log.Printf("папка %q: %s приоритетнее description: в %s", folder, descriptionFileName, metaFileName)
		}
		referenced[descriptionFileName] = true
		return strings.TrimSpace(string(raw)), nil
	case errors.Is(err, os.ErrNotExist):
		return strings.TrimSpace(metaDescription), nil
	default:
		return "", fmt.Errorf("папка %q: не прочитать %s: %w", folder, descriptionFileName, err)
	}
}

// validateYouTube проверяет форму ссылки на видеоверсию выпуска: абсолютный
// http(s) URL с хостом. Существование ролика не проверяется.
func validateYouTube(raw string) (string, error) {
	link := strings.TrimSpace(raw)
	if link == "" {
		return "", nil
	}
	u, err := url.Parse(link)
	if err != nil {
		return "", fmt.Errorf("youtube: некорректный URL %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("youtube: нужен абсолютный http(s) URL, получено %q", raw)
	}
	return link, nil
}

// resolveMedia проверяет файл из media-секции: он существует, лежит прямо в папке
// эпизода и его расширение соответствует виду носителя.
func resolveMedia(dir, folder, kind string, meta *mediaMeta) (EpisodeFile, error) {
	file := strings.TrimSpace(meta.File)
	if file == "" {
		return EpisodeFile{}, fmt.Errorf("папка %q: в секции %s: не указан file", folder, kind)
	}
	if file != filepath.Base(file) || strings.ContainsAny(file, `/\`) {
		return EpisodeFile{}, fmt.Errorf("папка %q: %s.file должен быть именем файла в папке эпизода, получено %q", folder, kind, meta.File)
	}
	ext := strings.ToLower(filepath.Ext(file))
	format, known := mediaByExt[ext]
	if !known {
		return EpisodeFile{}, fmt.Errorf("папка %q: %s.file %q: неподдерживаемое расширение %q", folder, kind, file, ext)
	}
	if format.Kind != kind {
		return EpisodeFile{}, fmt.Errorf("папка %q: %s.file %q — это %s-файл, а не %s", folder, kind, file, format.Kind, kind)
	}
	path := filepath.Join(dir, file)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EpisodeFile{}, fmt.Errorf("папка %q: %s.file %q не существует", folder, kind, file)
		}
		return EpisodeFile{}, fmt.Errorf("папка %q: %s.file %q: %w", folder, kind, file, err)
	}
	if !info.Mode().IsRegular() {
		return EpisodeFile{}, fmt.Errorf("папка %q: %s.file %q — не обычный файл", folder, kind, file)
	}
	return EpisodeFile{
		Kind:            kind,
		FilePath:        path,
		Mime:            format.Mime,
		Size:            info.Size(),
		DurationSeconds: meta.Duration,
		BitrateKbps:     meta.BitrateKbps,
		Resolution:      strings.TrimSpace(meta.Resolution),
		FileMtime:       info.ModTime().Unix(),
	}, nil
}

// SyncPodcastDir сканирует папку и приводит индекс в соответствие с ней.
func SyncPodcastDir(dir string, st *Store) (added, updated, removed int, err error) {
	found, err := ScanFolder(dir)
	if err != nil {
		return 0, 0, 0, err
	}
	return st.Sync(found)
}
