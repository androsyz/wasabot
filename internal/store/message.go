package store

import (
	"context"
	"fmt"
	"time"

	"github.com/androsyz/wasabot/internal/db"
)

const queryMessageCreate = `
	INSERT INTO messages (client_id, wa_id, chat, direction, body, created_at)
	VALUES (?, ?, ?, ?, ?, COALESCE(?, unixepoch()))
	RETURNING id, client_id, wa_id, chat, direction, body, created_at`

const queryMessageListRecent = `
	SELECT id, client_id, wa_id, chat, direction, body, created_at FROM (
		SELECT id, client_id, wa_id, chat, direction, body, created_at
		FROM messages
		WHERE client_id = ? AND chat = ?
		  AND id <= (SELECT id FROM messages WHERE client_id = ? AND wa_id = ?)
		ORDER BY id DESC
		LIMIT ?
	)
	ORDER BY id`

const queryMessageListUnanswered = `
	SELECT id, client_id, wa_id, chat, direction, body, created_at
	FROM messages m
	WHERE client_id = ? AND direction = 'in' AND created_at >= ?
	  AND id = (SELECT MAX(id) FROM messages WHERE client_id = m.client_id AND chat = m.chat)
	ORDER BY id`

const queryMessageCountSince = `
	SELECT client_id, COUNT(*)
	FROM messages
	WHERE created_at >= ?
	GROUP BY client_id`

type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

type Message struct {
	ID        int64
	ClientID  int64
	WAID      string // WhatsApp's message ID, unlike ID, which is our row ID
	Chat      string
	Direction Direction
	Body      string
	CreatedAt time.Time
}

type Messages struct {
	db db.DBTX
}

func NewMessages(conn db.DBTX) *Messages {
	return &Messages{db: conn}
}

// Create returns ErrDuplicateMessage if the client already has a message with this WAID.
// A zero CreatedAt means now; set it to the send time of a message that arrives late.
func (r *Messages) Create(ctx context.Context, m Message) (Message, error) {
	var createdAt any
	if !m.CreatedAt.IsZero() {
		createdAt = m.CreatedAt.Unix()
	}
	created, err := scanMessage(r.db.QueryRowContext(ctx, queryMessageCreate, m.ClientID, m.WAID, m.Chat, m.Direction, m.Body, createdAt))
	if db.IsUniqueViolation(err) {
		return Message{}, ErrDuplicateMessage
	}
	if err != nil {
		return Message{}, fmt.Errorf("create message: %w", err)
	}
	return created, nil
}

// ListRecent returns up to limit messages of the chat, oldest first, ending at the message
// with WhatsApp ID upToWAID. Later messages are excluded, so answering an earlier message
// never sees one that arrived after it. It returns nothing if upToWAID is unknown.
func (r *Messages) ListRecent(ctx context.Context, clientID int64, chat, upToWAID string, limit int) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, queryMessageListRecent, clientID, chat, clientID, upToWAID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent messages: %w", err)
	}
	defer rows.Close()

	messages, err := db.Collect(rows, scanMessage)
	if err != nil {
		return nil, fmt.Errorf("list recent messages: %w", err)
	}
	return messages, nil
}

// ListUnanswered returns the chats of the client whose latest message is an incoming one created
// at or after since: the customer is still waiting. Chats where we replied last are excluded.
func (r *Messages) ListUnanswered(ctx context.Context, clientID int64, since time.Time) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, queryMessageListUnanswered, clientID, max(since.Unix(), 0))
	if err != nil {
		return nil, fmt.Errorf("list unanswered messages: %w", err)
	}
	defer rows.Close()

	messages, err := db.Collect(rows, scanMessage)
	if err != nil {
		return nil, fmt.Errorf("list unanswered messages: %w", err)
	}
	return messages, nil
}

// CountSince counts messages in both directions per client, for the dashboard.
func (r *Messages) CountSince(ctx context.Context, since time.Time) (map[int64]int64, error) {
	rows, err := r.db.QueryContext(ctx, queryMessageCountSince, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}
	defer rows.Close()

	counts := make(map[int64]int64)
	for rows.Next() {
		var clientID, n int64
		if err := rows.Scan(&clientID, &n); err != nil {
			return nil, fmt.Errorf("count messages: %w", err)
		}
		counts[clientID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}
	return counts, nil
}

func scanMessage(s db.Scanner) (Message, error) {
	var m Message
	var created int64
	if err := s.Scan(&m.ID, &m.ClientID, &m.WAID, &m.Chat, &m.Direction, &m.Body, &created); err != nil {
		return Message{}, err
	}
	m.CreatedAt = time.Unix(created, 0)
	return m, nil
}
