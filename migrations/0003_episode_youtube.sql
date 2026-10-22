-- +goose Up
-- Ссылка на видеоверсию выпуска (YouTube и т.п.): необязательное поле meta.
-- Пустая строка = ссылки нет; форма URL проверяется сканером, а не схемой.
ALTER TABLE episodes ADD COLUMN youtube TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE episodes DROP COLUMN youtube;
