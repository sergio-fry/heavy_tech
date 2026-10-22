-- +goose Up
-- Снимок метаданных YouTube-версии выпуска: просмотры и главы. Данные внешние
-- и обновляются периодически, поэтому живут отдельно от индекса папки.
CREATE TABLE youtube_meta (
    number     INTEGER PRIMARY KEY REFERENCES episodes(number) ON DELETE CASCADE,
    video_id   TEXT NOT NULL,
    views      INTEGER NOT NULL DEFAULT 0,
    chapters   TEXT NOT NULL DEFAULT '[]', -- JSON [{start,title}]
    fetched_at INTEGER -- unix seconds; NULL = ещё не загружали
);

-- +goose Down
DROP TABLE IF EXISTS youtube_meta;
