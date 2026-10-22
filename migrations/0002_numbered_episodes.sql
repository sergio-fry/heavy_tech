-- +goose Up
-- Переход на номерную модель: эпизод = папка с номером, носители (audio/video)
-- живут отдельной таблицей. Данные прежней схемы не переносятся: guid меняются,
-- индекс пересобирается сканером из папок (design D5, fresh start).
DROP TABLE IF EXISTS play_events;
DROP TABLE IF EXISTS download_events;
DROP TABLE IF EXISTS episodes;

CREATE TABLE episodes (
    number        INTEGER PRIMARY KEY, -- номер выпуска = имя папки, ^[1-9][0-9]*$
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    published_at  TEXT NOT NULL, -- 'YYYY-MM-DD'; TEXT вместо DATE, чтобы драйвер не конвертировал значение в time.Time при чтении
    discovered_at TIMESTAMP NOT NULL
);

CREATE TABLE episode_files (
    number           INTEGER NOT NULL REFERENCES episodes(number) ON DELETE CASCADE,
    kind             TEXT NOT NULL CHECK (kind IN ('audio', 'video')),
    file_path        TEXT NOT NULL,
    mime             TEXT NOT NULL,
    size             INTEGER NOT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0, -- из episode.yaml; 0 = неизвестна
    bitrate_kbps     INTEGER NOT NULL DEFAULT 0, -- декларативная характеристика из meta
    resolution       TEXT NOT NULL DEFAULT '',   -- декларативная характеристика из meta
    file_mtime       INTEGER NOT NULL,
    UNIQUE (number, kind) -- не более одного носителя каждого вида
);

CREATE TABLE download_events (
    number     INTEGER NOT NULL REFERENCES episodes(number) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('audio', 'video')),
    ip_hash    BLOB NOT NULL,
    ua_hash    BLOB NOT NULL,
    day        TEXT NOT NULL, -- 'YYYY-MM-DD' UTC
    created_at TIMESTAMP NOT NULL,
    UNIQUE (number, kind, ip_hash, ua_hash, day)
);

CREATE TABLE play_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    number     INTEGER NOT NULL REFERENCES episodes(number) ON DELETE CASCADE,
    progress   INTEGER,
    created_at TIMESTAMP NOT NULL
);

-- +goose Down
-- Откат восстанавливает пустую прежнюю (slug-)схему: данные свежей модели
-- в ней не представимы, а индекс всё равно восстанавливается сканером.
DROP TABLE IF EXISTS play_events;
DROP TABLE IF EXISTS download_events;
DROP TABLE IF EXISTS episode_files;
DROP TABLE IF EXISTS episodes;

CREATE TABLE episodes (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    slug             TEXT NOT NULL UNIQUE,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    published_at     TEXT NOT NULL,
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
    day        TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (episode_id, ip_hash, ua_hash, day)
);

CREATE TABLE play_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    episode_id INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    progress   INTEGER,
    created_at TIMESTAMP NOT NULL
);
