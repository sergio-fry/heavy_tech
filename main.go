package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	cfg := LoadConfig()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg Config) error {
	// Канал без метаданных не публикуется: podcast.yaml обязателен.
	ch, err := LoadChannel(cfg.PodcastDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return err
	}
	st, err := OpenStore(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Автоскан при старте: папка — источник истины.
	if _, _, _, err := SyncPodcastDir(cfg.PodcastDir, st); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// YouTube-метаданные необязательны: без ключа сервис работает как раньше.
	var syncer *youtubeSyncer
	if cfg.YouTubeAPIKey == "" {
		log.Printf("youtube: ключ не задан — главы и просмотры отключены")
	} else {
		syncer = newYouTubeSyncer(st, cfg.YouTubeAPIKey)
		go syncer.Run(ctx)
	}

	srv, err := NewServer(cfg, ch, st)
	if err != nil {
		return err
	}
	if syncer != nil {
		srv.AfterRescan = syncer.Trigger
	}
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	log.Printf("heavytech: слушаю %s, папка %s, база %s, base-url %s",
		cfg.Addr, cfg.PodcastDir, cfg.DBPath, cfg.BaseURL)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
