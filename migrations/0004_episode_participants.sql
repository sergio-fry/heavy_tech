-- +goose Up
-- Участники выпуска (design D2, change episode-host-guests): ведущий строкой,
-- гости JSON-массивом (порядок — часть данных). Аддитивная миграция.
ALTER TABLE episodes ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN guests TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE episodes DROP COLUMN host;
ALTER TABLE episodes DROP COLUMN guests;
