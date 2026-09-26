-- +goose Up
ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1));

-- +goose Down
ALTER TABLE users DROP COLUMN must_change_password;
