-- +goose Up
CREATE TABLE clients (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    email         TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    name          TEXT    NOT NULL,
    password_hash TEXT,
    role          TEXT    NOT NULL CHECK (role IN ('super_admin', 'client_user')),
    disabled      INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at    INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE client_users (
    client_id  INTEGER NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (client_id, user_id)
);

CREATE INDEX idx_client_users_user_id ON client_users(user_id);

-- +goose Down
DROP INDEX idx_client_users_user_id;
DROP TABLE client_users;
DROP TABLE users;
DROP TABLE clients;