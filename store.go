package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Виды носителей эпизода. Ровно по одному на вид (design D5).
const (
	KindAudio = "audio"
	KindVideo = "video"
)

// Store — SQLite-хранилище: производный индекс эпизодов + сырые события метрик.
type Store struct {
	db   *sql.DB
	salt []byte
}

// OpenStore открывает (создаёт) базу, накатывает миграции, готовит соль хеширования.
func OpenStore(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Хобби-нагрузка: одно соединение исключает SQLITE_BUSY и гонки записи.
	db.SetMaxOpenConns(1)

	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		db.Close()
		return nil, err
	}
	goose.SetBaseFS(migrationsFS)
	if err := goose.Up(db, "migrations"); err != nil {
		db.Close()
		return nil, fmt.Errorf("миграции: %w", err)
	}

	s := &Store{db: db}
	if s.salt, err = s.loadOrCreateSalt(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// loadOrCreateSalt — соль для хеширования IP/UA: случайная при первом старте,
// живёт в meta. Хеши стабильны в пределах инстанса и не переносимы между инстансами.
func (s *Store) loadOrCreateSalt() ([]byte, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'hash_salt'`).Scan(&val)
	if err == nil {
		return hex.DecodeString(val)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	val = hex.EncodeToString(raw)
	if _, err := s.db.Exec(`INSERT INTO meta (key, value) VALUES ('hash_salt', ?)`, val); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *Store) hash(value string) []byte {
	h := sha256.Sum256(append(append([]byte{}, s.salt...), value...))
	return h[:]
}

// EpisodeFile — носитель эпизода. kind — audio либо video, по одному на эпизод.
type EpisodeFile struct {
	Number          int64
	Kind            string
	FilePath        string
	Mime            string
	Size            int64
	DurationSeconds int64  // из episode.yaml; 0 = неизвестна (оценивается при показе)
	BitrateKbps     int64  // декларативная характеристика из meta
	Resolution      string // декларативная характеристика из meta
	FileMtime       int64
}

// Episode — запись индекса: номер — единственный идентификатор, остальное из папки.
type Episode struct {
	Number      int64
	Title       string
	Description string   // markdown-исходник описания
	YouTube     string   // ссылка на видеоверсию выпуска; пустая = нет
	Host        string   // ведущий выпуска; пусто = не указан
	Guests      []string // гости в порядке вывода; пусто = не указаны
	PublishedAt time.Time
	Files       []EpisodeFile
}

// episodeSelect — общий список колонок эпизода для SELECT-ов.
const episodeSelect = `number, title, description, youtube, host, guests, published_at`

// scanEpisodeRow — скан строки эпизода (Row или Rows): участники читаются из
// JSON-колонки guests (порядок сохраняется), дата парсится из 'YYYY-MM-DD'.
func scanEpisodeRow(scan func(dest ...any) error) (Episode, error) {
	var e Episode
	var pubDate, guestsRaw string
	if err := scan(&e.Number, &e.Title, &e.Description, &e.YouTube, &e.Host, &guestsRaw, &pubDate); err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(guestsRaw), &e.Guests); err != nil {
		return e, err
	}
	pub, err := time.Parse("2006-01-02", pubDate)
	if err != nil {
		return e, err
	}
	e.PublishedAt = pub
	return e, nil
}

// guestsJSON — гости в формате колонки; nil нормализуется в пустой список.
func guestsJSON(guests []string) (string, error) {
	if guests == nil {
		guests = []string{}
	}
	b, err := json.Marshal(guests)
	return string(b), err
}

// File возвращает носитель заданного вида.
func (e Episode) File(kind string) (EpisodeFile, bool) {
	for _, f := range e.Files {
		if f.Kind == kind {
			return f, true
		}
	}
	return EpisodeFile{}, false
}

// EpisodeByNumber — эпизод из индекса; sql.ErrNoRows, если номера нет.
func (s *Store) EpisodeByNumber(number int64) (Episode, error) {
	e, err := scanEpisodeRow(s.db.QueryRow(`SELECT `+episodeSelect+` FROM episodes WHERE number = ?`, number).Scan)
	if err != nil {
		return e, err
	}
	files, err := s.loadFiles()
	if err != nil {
		return e, err
	}
	e.Files = files[number]
	return e, nil
}

// downloadCounts — агрегаты скачиваний по видам носителей.
type DownloadCounts struct {
	Audio int64
	Video int64
	Total int64
}

// EpisodeView — эпизод с носителями и агрегатами метрик за всё время.
type EpisodeView struct {
	Episode
	Downloads DownloadCounts
	Plays     int64
}

// ListEpisodes — все эпизоды с носителями, новые сверху (по published_at).
func (s *Store) ListEpisodes() ([]Episode, error) {
	rows, err := s.db.Query(`SELECT ` + episodeSelect + `
		FROM episodes ORDER BY published_at DESC, number DESC`)
	if err != nil {
		return nil, err
	}
	var out []Episode
	for rows.Next() {
		e, err := scanEpisodeRow(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	files, err := s.loadFiles()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Files = files[out[i].Number]
	}
	return out, nil
}

// loadFiles читает все носители: номер → [audio?, video?] в стабильном порядке.
func (s *Store) loadFiles() (map[int64][]EpisodeFile, error) {
	rows, err := s.db.Query(`SELECT number, kind, file_path, mime, size, duration_seconds,
		bitrate_kbps, resolution, file_mtime
		FROM episode_files
		ORDER BY number, CASE kind WHEN 'audio' THEN 0 ELSE 1 END`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]EpisodeFile{}
	for rows.Next() {
		var f EpisodeFile
		if err := rows.Scan(&f.Number, &f.Kind, &f.FilePath, &f.Mime, &f.Size,
			&f.DurationSeconds, &f.BitrateKbps, &f.Resolution, &f.FileMtime); err != nil {
			return nil, err
		}
		out[f.Number] = append(out[f.Number], f)
	}
	return out, rows.Err()
}

// ListEpisodesWithCounts — все эпизоды, новые сверху, с суммами скачиваний по видам
// носителей и прослушиваниями.
func (s *Store) ListEpisodesWithCounts() ([]EpisodeView, error) {
	eps, err := s.ListEpisodes()
	if err != nil {
		return nil, err
	}

	downloads := map[int64]map[string]int64{}
	rows, err := s.db.Query(`SELECT number, kind, COUNT(*) FROM download_events GROUP BY number, kind`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var number, count int64
		var kind string
		if err := rows.Scan(&number, &kind, &count); err != nil {
			rows.Close()
			return nil, err
		}
		if downloads[number] == nil {
			downloads[number] = map[string]int64{}
		}
		downloads[number][kind] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	plays := map[int64]int64{}
	rows, err = s.db.Query(`SELECT number, COUNT(*) FROM play_events GROUP BY number`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var number, count int64
		if err := rows.Scan(&number, &count); err != nil {
			rows.Close()
			return nil, err
		}
		plays[number] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]EpisodeView, 0, len(eps))
	for _, e := range eps {
		v := EpisodeView{
			Episode: e,
			Downloads: DownloadCounts{
				Audio: downloads[e.Number][KindAudio],
				Video: downloads[e.Number][KindVideo],
			},
			Plays: plays[e.Number],
		}
		v.Downloads.Total = v.Downloads.Audio + v.Downloads.Video
		out = append(out, v)
	}
	return out, nil
}

// episodeEqual сравнивает запись индекса с найденным в папке состоянием.
func episodeEqual(a, b Episode) bool {
	if a.Number != b.Number || a.Title != b.Title || a.Description != b.Description || a.YouTube != b.YouTube {
		return false
	}
	if a.Host != b.Host || !slices.Equal(a.Guests, b.Guests) {
		return false
	}
	if !a.PublishedAt.Equal(b.PublishedAt) || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		x, y := a.Files[i], b.Files[i]
		if x.Kind != y.Kind || x.FilePath != y.FilePath || x.Mime != y.Mime || x.Size != y.Size ||
			x.DurationSeconds != y.DurationSeconds || x.BitrateKbps != y.BitrateKbps ||
			x.Resolution != y.Resolution || x.FileMtime != y.FileMtime {
			return false
		}
	}
	return true
}

// insertFiles пишет носители эпизода в рамках транзакции.
func insertFiles(tx *sql.Tx, e Episode) error {
	for _, f := range e.Files {
		if _, err := tx.Exec(`INSERT INTO episode_files
			(number, kind, file_path, mime, size, duration_seconds, bitrate_kbps, resolution, file_mtime)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Number, f.Kind, f.FilePath, f.Mime, f.Size, f.DurationSeconds,
			f.BitrateKbps, f.Resolution, f.FileMtime); err != nil {
			return err
		}
	}
	return nil
}

// Sync приводит индекс в соответствие с найденным в папке: добавляет новые,
// обновляет изменившиеся, удаляет исчезнувшие (CASCADE чистит носители и события).
// Идемпотентен: повторный вызов без изменений в папке ничего не меняет.
func (s *Store) Sync(found []Episode) (added, updated, removed int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	have := map[int64]Episode{}
	rows, err := tx.Query(`SELECT ` + episodeSelect + ` FROM episodes`)
	if err != nil {
		return 0, 0, 0, err
	}
	for rows.Next() {
		e, serr := scanEpisodeRow(rows.Scan)
		if serr != nil {
			rows.Close()
			return 0, 0, 0, serr
		}
		have[e.Number] = e
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, 0, err
	}
	rows.Close()

	files, err := func() (map[int64][]EpisodeFile, error) {
		out := map[int64][]EpisodeFile{}
		frows, ferr := tx.Query(`SELECT number, kind, file_path, mime, size, duration_seconds,
			bitrate_kbps, resolution, file_mtime
			FROM episode_files ORDER BY number, CASE kind WHEN 'audio' THEN 0 ELSE 1 END`)
		if ferr != nil {
			return nil, ferr
		}
		defer frows.Close()
		for frows.Next() {
			var f EpisodeFile
			if ferr = frows.Scan(&f.Number, &f.Kind, &f.FilePath, &f.Mime, &f.Size,
				&f.DurationSeconds, &f.BitrateKbps, &f.Resolution, &f.FileMtime); ferr != nil {
				return nil, ferr
			}
			out[f.Number] = append(out[f.Number], f)
		}
		return out, frows.Err()
	}()
	if err != nil {
		return 0, 0, 0, err
	}
	for number, e := range have {
		e.Files = files[number]
		have[number] = e
	}

	seen := map[int64]bool{}
	for _, e := range found {
		seen[e.Number] = true
		pubDate := e.PublishedAt.Format("2006-01-02")
		prev, ok := have[e.Number]
		switch {
		case !ok:
			guests, gerr := guestsJSON(e.Guests)
			if gerr != nil {
				return 0, 0, 0, gerr
			}
			if _, err = tx.Exec(`INSERT INTO episodes (number, title, description, youtube, host, guests, published_at, discovered_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, e.Number, e.Title, e.Description, e.YouTube, e.Host, guests, pubDate, time.Now().UTC()); err != nil {
				return 0, 0, 0, err
			}
			if err = insertFiles(tx, e); err != nil {
				return 0, 0, 0, err
			}
			added++
		case !episodeEqual(prev, e):
			guests, gerr := guestsJSON(e.Guests)
			if gerr != nil {
				return 0, 0, 0, gerr
			}
			if _, err = tx.Exec(`UPDATE episodes SET title = ?, description = ?, youtube = ?, host = ?, guests = ?, published_at = ? WHERE number = ?`,
				e.Title, e.Description, e.YouTube, e.Host, guests, pubDate, e.Number); err != nil {
				return 0, 0, 0, err
			}
			if _, err = tx.Exec(`DELETE FROM episode_files WHERE number = ?`, e.Number); err != nil {
				return 0, 0, 0, err
			}
			if err = insertFiles(tx, e); err != nil {
				return 0, 0, 0, err
			}
			updated++
		}
	}
	for number := range have {
		if seen[number] {
			continue
		}
		if _, err = tx.Exec(`DELETE FROM episodes WHERE number = ?`, number); err != nil {
			return 0, 0, 0, err
		}
		removed++
	}

	return added, updated, removed, tx.Commit()
}

// RecordDownload фиксирует скачивание носителя: сольтированные хеши IP и UA, сутки UTC.
// Возвращает true, если событие засчитано (первое для ключа за день).
func (s *Store) RecordDownload(number int64, kind, ip, ua string, at time.Time) (bool, error) {
	day := at.UTC().Format("2006-01-02")
	res, err := s.db.Exec(`INSERT INTO download_events (number, kind, ip_hash, ua_hash, day, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (number, kind, ip_hash, ua_hash, day) DO NOTHING`,
		number, kind, s.hash(ip), s.hash(ua), day, at.UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecordPlay фиксирует прослушивание (бикон плеера); progress — опциональный процент.
func (s *Store) RecordPlay(number int64, progress *int, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO play_events (number, progress, created_at) VALUES (?, ?, ?)`,
		number, progress, at.UTC())
	return err
}

// CountPlays — суммарные прослушивания эпизода.
func (s *Store) CountPlays(number int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM play_events WHERE number = ?`, number).Scan(&n)
	return n, err
}

// CountDownloads — скачивания эпизода с разбивкой по видам носителей.
func (s *Store) CountDownloads(number int64) (DownloadCounts, error) {
	var c DownloadCounts
	rows, err := s.db.Query(`SELECT kind, COUNT(*) FROM download_events WHERE number = ? GROUP BY kind`, number)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			return c, err
		}
		switch kind {
		case KindAudio:
			c.Audio = n
		case KindVideo:
			c.Video = n
		}
	}
	c.Total = c.Audio + c.Video
	return c, rows.Err()
}

// DumpEvents для тестов приватности: возвращает все события как строки хешей и дней.
func (s *Store) DumpEvents() (downloads [][6]string, plays [][4]string, err error) {
	drows, err := s.db.Query(`SELECT number, kind, HEX(ip_hash), HEX(ua_hash), day, CAST(created_at AS TEXT)
		FROM download_events`)
	if err != nil {
		return nil, nil, err
	}
	defer drows.Close()
	for drows.Next() {
		var r [6]string
		if err = drows.Scan(&r[0], &r[1], &r[2], &r[3], &r[4], &r[5]); err != nil {
			return nil, nil, err
		}
		downloads = append(downloads, r)
	}
	if err = drows.Err(); err != nil {
		return nil, nil, err
	}
	prows, err := s.db.Query(`SELECT number, CAST(progress AS TEXT), CAST(created_at AS TEXT), '' FROM play_events`)
	if err != nil {
		return nil, nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var r [4]string
		if err = prows.Scan(&r[0], &r[1], &r[2], &r[3]); err != nil {
			return nil, nil, err
		}
		plays = append(plays, r)
	}
	return downloads, plays, prows.Err()
}
