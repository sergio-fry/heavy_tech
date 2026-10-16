package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

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

// Episode — запись индекса; всё, кроме id и discovered_at, выводится из папки.
type Episode struct {
	ID              int64
	Slug            string
	Title           string
	Description     string
	PublishedAt     time.Time
	FilePath        string
	FileSize        int64
	Mime            string
	DurationSeconds int64 // из sidecar; 0 = неизвестна (оценивается при показе)
	FileMtime       int64
}

const episodeCols = `id, slug, title, description, published_at, file_path, file_size, mime, duration_seconds, file_mtime`

func scanEpisode(row interface{ Scan(...any) error }) (Episode, error) {
	var e Episode
	var pubDate string
	err := row.Scan(&e.ID, &e.Slug, &e.Title, &e.Description, &pubDate, &e.FilePath, &e.FileSize, &e.Mime, &e.DurationSeconds, &e.FileMtime)
	if err != nil {
		return e, err
	}
	e.PublishedAt, err = time.Parse("2006-01-02", pubDate)
	return e, err
}

// EpisodeBySlug — эпизод из индекса; sql.ErrNoRows, если слага нет.
func (s *Store) EpisodeBySlug(slug string) (Episode, error) {
	return scanEpisode(s.db.QueryRow(`SELECT `+episodeCols+` FROM episodes WHERE slug = ?`, slug))
}

// EpisodeView — эпизод с агрегатами метрик за всё время.
type EpisodeView struct {
	Episode
	Downloads int64
	Plays     int64
}

// ListEpisodesWithCounts — все эпизоды, новые сверху, с суммами скачиваний и прослушиваний.
func (s *Store) ListEpisodesWithCounts() ([]EpisodeView, error) {
	rows, err := s.db.Query(`SELECT ` + episodeCols + `,
		(SELECT COUNT(*) FROM download_events d WHERE d.episode_id = e.id),
		(SELECT COUNT(*) FROM play_events p WHERE p.episode_id = e.id)
		FROM episodes e ORDER BY e.published_at DESC, e.slug DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpisodeView
	for rows.Next() {
		var v EpisodeView
		var pubDate string
		if err := rows.Scan(&v.ID, &v.Slug, &v.Title, &v.Description, &pubDate, &v.FilePath, &v.FileSize, &v.Mime, &v.DurationSeconds, &v.FileMtime, &v.Downloads, &v.Plays); err != nil {
			return nil, err
		}
		if v.PublishedAt, err = time.Parse("2006-01-02", pubDate); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Sync приводит индекс в соответствие с найденным в папке: добавляет новые,
// обновляет изменившиеся, удаляет исчезнувшие (CASCADE чистит события).
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

	type existing struct {
		id       int64
		size     int64
		mtime    int64
		title    string
		descr    string
		duration int64
	}
	have := map[string]existing{}
	rows, err := tx.Query(`SELECT id, slug, file_size, file_mtime, title, description, duration_seconds FROM episodes`)
	if err != nil {
		return 0, 0, 0, err
	}
	for rows.Next() {
		var e existing
		var slug string
		if err = rows.Scan(&e.id, &slug, &e.size, &e.mtime, &e.title, &e.descr, &e.duration); err != nil {
			rows.Close()
			return 0, 0, 0, err
		}
		have[slug] = e
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, 0, err
	}
	rows.Close()

	seen := map[string]bool{}
	for _, e := range found {
		seen[e.Slug] = true
		prev, ok := have[e.Slug]
		switch {
		case !ok:
			if _, err = tx.Exec(`INSERT INTO episodes
				(slug, title, description, published_at, file_path, file_size, mime, duration_seconds, file_mtime, discovered_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				e.Slug, e.Title, e.Description, e.PublishedAt.Format("2006-01-02"), e.FilePath, e.FileSize, e.Mime, e.DurationSeconds, e.FileMtime, time.Now().UTC()); err != nil {
				return 0, 0, 0, err
			}
			added++
		case prev.size != e.FileSize || prev.mtime != e.FileMtime || prev.title != e.Title ||
			prev.descr != e.Description || prev.duration != e.DurationSeconds:
			if _, err = tx.Exec(`UPDATE episodes SET title = ?, description = ?, published_at = ?, file_path = ?,
				file_size = ?, mime = ?, duration_seconds = ?, file_mtime = ? WHERE id = ?`,
				e.Title, e.Description, e.PublishedAt.Format("2006-01-02"), e.FilePath, e.FileSize, e.Mime, e.DurationSeconds, e.FileMtime, prev.id); err != nil {
				return 0, 0, 0, err
			}
			updated++
		}
	}
	for slug, prev := range have {
		if seen[slug] {
			continue
		}
		if _, err = tx.Exec(`DELETE FROM episodes WHERE id = ?`, prev.id); err != nil {
			return 0, 0, 0, err
		}
		removed++
	}

	return added, updated, removed, tx.Commit()
}

// RecordDownload фиксирует скачивание: сольтированные хеши IP и UA, сутки UTC.
// Возвращает true, если событие засчитано (первое для ключа за день).
func (s *Store) RecordDownload(episodeID int64, ip, ua string, at time.Time) (bool, error) {
	day := at.UTC().Format("2006-01-02")
	res, err := s.db.Exec(`INSERT INTO download_events (episode_id, ip_hash, ua_hash, day, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (episode_id, ip_hash, ua_hash, day) DO NOTHING`,
		episodeID, s.hash(ip), s.hash(ua), day, at.UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecordPlay фиксирует прослушивание (бикон плеера); progress — опциональный процент.
func (s *Store) RecordPlay(episodeID int64, progress *int, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO play_events (episode_id, progress, created_at) VALUES (?, ?, ?)`,
		episodeID, progress, at.UTC())
	return err
}

// CountPlays — суммарные прослушивания эпизода.
func (s *Store) CountPlays(episodeID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM play_events WHERE episode_id = ?`, episodeID).Scan(&n)
	return n, err
}

// CountDownloads — суммарные скачивания эпизода.
func (s *Store) CountDownloads(episodeID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM download_events WHERE episode_id = ?`, episodeID).Scan(&n)
	return n, err
}

// DumpEvents для тестов приватности: возвращает все события как строки хешей и дней.
func (s *Store) DumpEvents() (downloads [][5]string, plays [][4]string, err error) {
	drows, err := s.db.Query(`SELECT episode_id, HEX(ip_hash), HEX(ua_hash), day, CAST(created_at AS TEXT) FROM download_events`)
	if err != nil {
		return nil, nil, err
	}
	defer drows.Close()
	for drows.Next() {
		var r [5]string
		if err = drows.Scan(&r[0], &r[1], &r[2], &r[3], &r[4]); err != nil {
			return nil, nil, err
		}
		downloads = append(downloads, r)
	}
	if err = drows.Err(); err != nil {
		return nil, nil, err
	}
	prows, err := s.db.Query(`SELECT episode_id, CAST(progress AS TEXT), CAST(created_at AS TEXT), '' FROM play_events`)
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
