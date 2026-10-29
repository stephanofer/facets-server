-- +goose Up
CREATE TABLE environment_probe (
    id BIGINT PRIMARY KEY,
    label TEXT NOT NULL
);

-- +goose Down
DROP TABLE environment_probe;
