-- +goose Up
-- Обложка выпуска (change episode-cover): путь к файлу-картинке в папке эпизода.
-- Отдельная колонка, а не строка episode_files: обложка — оформление, не носитель
-- (не попадает в списки скачиваний и media-массив API). Аддитивная миграция.
ALTER TABLE episodes ADD COLUMN cover TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE episodes DROP COLUMN cover;
