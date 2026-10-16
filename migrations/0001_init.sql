-- +goose Up
CREATE TABLE episodes (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    slug             TEXT NOT NULL UNIQUE,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    published_at     TEXT NOT NULL, -- 'YYYY-MM-DD'; TEXT вместо DATE, чтобы драйвер не конвертировал значение в time.Time при чтении
    file_path        TEXT NOT NULL,
    file_size        INTEGER NOT NULL,
    mime             TEXT NOT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    file_mtime       INTEGER NOT NULL,
    discovered_at    TIMESTAMP NOT NULL
);

CREATE TABLE download_events (
    episode_id INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    ip_hash    BLOB NOT NULL,
    ua_hash    BLOB NOT NULL,
    day        TEXT NOT NULL, -- 'YYYY-MM-DD' UTC
    created_at TIMESTAMP NOT NULL,
    UNIQUE (episode_id, ip_hash, ua_hash, day)
);

CREATE TABLE play_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    episode_id INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    progress   INTEGER,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS meta;
DROP TABLE IF EXISTS play_events;
DROP TABLE IF EXISTS download_events;
DROP TABLE IF EXISTS episodes;
