-- +goose Up
CREATE TABLE messages (
    id         INTEGER PRIMARY KEY,
    client_id  INTEGER NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    wa_id      TEXT    NOT NULL,
    chat       TEXT    NOT NULL,
    direction  TEXT    NOT NULL CHECK (direction IN ('in', 'out')),
    body       TEXT    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (client_id, wa_id)
);

CREATE INDEX idx_messages_client_chat ON messages(client_id, chat, id);

-- +goose Down
DROP INDEX idx_messages_client_chat;
DROP TABLE messages;
