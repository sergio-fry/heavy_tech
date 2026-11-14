-- +goose Up
-- Участники канала (change participant-profiles): сущность с именем (ключ связи),
-- описанием и аватаром; связи «выпуск ↔ участник» пересчитываются при каждом
-- рескане. Аддитивная миграция: таблицы новые, данные выпусков не трогаются.
CREATE TABLE people (
    slug        TEXT PRIMARY KEY,           -- имя папки участника
    name        TEXT NOT NULL UNIQUE,       -- ключ связи с host/guests выпусков
    description TEXT NOT NULL DEFAULT '',   -- markdown-исходник
    avatar_ext  TEXT NOT NULL DEFAULT '',   -- '.jpg' и т.п.; '' = аватара нет
    avatar_path TEXT NOT NULL DEFAULT ''    -- путь к файлу аватара на диске
);

CREATE TABLE episode_people (
    number   INTEGER NOT NULL REFERENCES episodes(number) ON DELETE CASCADE,
    slug     TEXT NOT NULL REFERENCES people(slug) ON DELETE CASCADE,
    role     TEXT NOT NULL CHECK (role IN ('host', 'guest')),
    position INTEGER NOT NULL DEFAULT 0,    -- позиция в списке гостей (host = 0)
    PRIMARY KEY (number, slug, role)
);

CREATE INDEX idx_episode_people_slug ON episode_people(slug, number);

-- +goose Down
DROP TABLE IF EXISTS episode_people;
DROP TABLE IF EXISTS people;
