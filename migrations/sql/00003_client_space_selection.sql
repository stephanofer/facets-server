-- +goose Up
ALTER TABLE sessions DROP COLUMN active_space;

-- +goose Down
ALTER TABLE sessions ADD COLUMN active_space text NOT NULL DEFAULT '';
