package main

import (
	"errors"
	"html/template"
	"log"
	"os"
	"path/filepath"
)

// aboutFileName — необязательный markdown-текст лендинга в корне папки подкастов.
const aboutFileName = "about.md"

// siteContent — то, что перечитывается с диска после рескана: метаданные канала
// и текст лендинга. Читается под RWMutex, чтобы запросы не видели промежуточное состояние.
type siteContent struct {
	ch       Channel
	aboutRaw string
}

// LoadAbout читает about.md; отсутствие файла — не ошибка, а пустой текст.
func LoadAbout(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, aboutFileName))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// channel — текущие метаданные канала.
func (s *Server) channel() Channel {
	s.contentMu.RLock()
	defer s.contentMu.RUnlock()
	return s.content.ch
}

// reloadContent перечитывает podcast.yaml и about.md. Ошибка не заменяет
// последнее валидное состояние: сайт продолжает работать со старым контентом.
func (s *Server) reloadContent() {
	ch, err := LoadChannel(s.cfg.PodcastDir)
	if err != nil {
		log.Printf("контент: %v", err)
		return
	}
	about, err := LoadAbout(s.cfg.PodcastDir)
	if err != nil {
		log.Printf("about.md: %v", err)
		return
	}
	s.contentMu.Lock()
	s.content = siteContent{ch: ch, aboutRaw: about}
	s.contentMu.Unlock()
}

// lockContent — хелпер для тестов и сборки страниц: согласованный снимок контента.
func (s *Server) lockContent() (Channel, template.HTML) {
	s.contentMu.RLock()
	defer s.contentMu.RUnlock()
	return s.content.ch, RenderDescription(s.content.aboutRaw)
}
