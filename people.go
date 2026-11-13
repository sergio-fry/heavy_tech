package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// peopleDirName — каталог участников в корне podcast-dir, рядом с podcast.yaml.
const peopleDirName = "people"

// personFileName — обязательный манифест участника внутри папки.
const personFileName = "person.yaml"

// personDescriptionFileName — необязательное markdown-описание участника.
// Приоритетнее поля description: в person.yaml — как у выпусков.
const personDescriptionFileName = "description.md"

// personSlugRe — slug участника = имя папки: [a-z0-9] слова через дефис.
var personSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// avatarMimeByExt — допустимые расширения аватара и их MIME.
var avatarMimeByExt = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".gif":  "image/gif",
}

// personMeta — содержимое обязательного person.yaml.
type personMeta struct {
	Name        string `yaml:"name"`        // ключ связи с выпусками; обязателен
	Avatar      string `yaml:"avatar"`      // имя файла изображения в той же папке
	Description string `yaml:"description"` // markdown; фолбэк, если нет description.md
}

// validatePersonTypes — строгая типизация полей person.yaml (как у участников
// эпизода): yaml.v3 приводит скаляры к строке молча, а конвенция требует строку.
// Отсутствующее поле (null) равно пустому.
func validatePersonTypes(raw []byte) error {
	var typed struct {
		Name        yaml.Node `yaml:"name"`
		Avatar      yaml.Node `yaml:"avatar"`
		Description yaml.Node `yaml:"description"`
	}
	if err := yaml.Unmarshal(raw, &typed); err != nil {
		return err
	}
	absent := func(n yaml.Node) bool { return n.Kind == 0 || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") }
	str := func(field string, n yaml.Node) error {
		if absent(n) {
			return nil
		}
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return fmt.Errorf("%s: должна быть строкой", field)
		}
		return nil
	}
	if err := str("name", typed.Name); err != nil {
		return err
	}
	if err := str("avatar", typed.Avatar); err != nil {
		return err
	}
	return str("description", typed.Description)
}

// ScanPeople читает каталог участников: по папке на участника, slug = имя папки.
// Папки people нет — участников нет, сервис работает как без фичи. Любое
// нарушение конвенции (нет person.yaml, битый YAML, несуществующий аватар,
// лишний файл, дубликат имени) — ошибка синхронизации, а не тихий пропуск.
func ScanPeople(podcastDir string) ([]Person, error) {
	root := filepath.Join(podcastDir, peopleDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("не открыть папку участников %s: %w", root, err)
	}
	var out []Person
	nameOwner := map[string]string{} // name → slug владельца (для дубликатов)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue // dot-файлы: scp-временки и подобное
		}
		slug := entry.Name()
		if !entry.IsDir() {
			return nil, fmt.Errorf("папка участников: %q должен быть папкой участника", slug)
		}
		if !personSlugRe.MatchString(slug) {
			return nil, fmt.Errorf("папка участника %q: имя папки должно быть slug из [a-z0-9] и дефисов", slug)
		}
		p, err := scanPersonFolder(root, slug)
		if err != nil {
			return nil, err
		}
		if prev, dup := nameOwner[p.Name]; dup {
			return nil, fmt.Errorf("папка участника %q: имя %q уже описано в папке %q — имена участников должны быть уникальными", slug, p.Name, prev)
		}
		nameOwner[p.Name] = slug
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// scanPersonFolder разбирает одну папку участника и валидирует её файлы.
func scanPersonFolder(root, slug string) (Person, error) {
	dir := filepath.Join(root, slug)
	metaPath := filepath.Join(dir, personFileName)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Person{}, fmt.Errorf("папка участника %q: нет обязательного %s", slug, personFileName)
		}
		return Person{}, fmt.Errorf("папка участника %q: %w", slug, err)
	}
	var meta personMeta
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return Person{}, fmt.Errorf("не удалось разобрать %s: %w", metaPath, err)
	}
	if err := validatePersonTypes(raw); err != nil {
		return Person{}, fmt.Errorf("%s: %w", metaPath, err)
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		return Person{}, fmt.Errorf("%s: пустой name", metaPath)
	}

	referenced := map[string]bool{personFileName: true}
	avatarExt, avatarPath, err := resolveAvatar(dir, slug, meta.Avatar, referenced)
	if err != nil {
		return Person{}, err
	}
	description, err := resolvePersonDescription(dir, slug, meta.Description, referenced)
	if err != nil {
		return Person{}, err
	}

	// Недот-файл, не упомянутый в person.yaml, — ошибка: молчаливое игнорирование
	// маскирует опечатки. Dot-файлы игнорируются.
	folderEntries, err := os.ReadDir(dir)
	if err != nil {
		return Person{}, fmt.Errorf("папка участника %q: %w", slug, err)
	}
	for _, entry := range folderEntries {
		if strings.HasPrefix(entry.Name(), ".") || referenced[entry.Name()] {
			continue
		}
		return Person{}, fmt.Errorf("папка участника %q: файл %q не упомянут в %s", slug, entry.Name(), personFileName)
	}
	return Person{
		Slug:        slug,
		Name:        name,
		Description: description,
		AvatarExt:   avatarExt,
		AvatarPath:  avatarPath,
	}, nil
}

// resolveAvatar проверяет avatar: имя файла в папке участника с известным
// изображением-расширением; файл должен существовать.
func resolveAvatar(dir, slug, field string, referenced map[string]bool) (ext, path string, err error) {
	file := strings.TrimSpace(field)
	if file == "" {
		return "", "", nil
	}
	if file != filepath.Base(file) || strings.ContainsAny(file, `/\`) {
		return "", "", fmt.Errorf("папка участника %q: avatar должен быть именем файла в папке участника, получено %q", slug, field)
	}
	ext = strings.ToLower(filepath.Ext(file))
	if _, known := avatarMimeByExt[ext]; !known {
		return "", "", fmt.Errorf("папка участника %q: avatar %q: неподдерживаемое расширение %q", slug, field, ext)
	}
	path = filepath.Join(dir, file)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("папка участника %q: avatar %q не существует", slug, file)
		}
		return "", "", fmt.Errorf("папка участника %q: avatar %q: %w", slug, file, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("папка участника %q: avatar %q — не обычный файл", slug, file)
	}
	referenced[file] = true
	return ext, path, nil
}

// resolvePersonDescription выбирает источник описания: description.md
// приоритетнее поля description: в person.yaml (симметрично выпускам).
func resolvePersonDescription(dir, slug, metaDescription string, referenced map[string]bool) (string, error) {
	path := filepath.Join(dir, personDescriptionFileName)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.TrimSpace(metaDescription) != "" {
			fmt.Fprintf(os.Stderr, "папка участника %q: %s приоритетнее description: в %s\n", slug, personDescriptionFileName, personFileName)
		}
		referenced[personDescriptionFileName] = true
		return strings.TrimSpace(string(raw)), nil
	case errors.Is(err, os.ErrNotExist):
		return strings.TrimSpace(metaDescription), nil
	default:
		return "", fmt.Errorf("папка участника %q: не прочитать %s: %w", slug, personDescriptionFileName, err)
	}
}

// SyncPodcastDir сканирует папку (эпизоды и участников) и приводит индекс
// в соответствие с ней.
func SyncPodcastDir(dir string, st *Store) (added, updated, removed int, err error) {
	found, err := ScanFolder(dir)
	if err != nil {
		return 0, 0, 0, err
	}
	people, err := ScanPeople(dir)
	if err != nil {
		return 0, 0, 0, err
	}
	return st.Sync(found, people)
}
