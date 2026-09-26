package store

import (
	"context"
	"fmt"
	"time"

	"github.com/androsyz/wasabot/internal/db"
)

const queryMessageCreate = `
	INSERT INTO messages (client_id, wa_id, chat, direction, body)
	VALUES (?, ?, ?, ?, ?)
	RETURNING id, client_id, wa_id, chat, direction, body, created_at`

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
func (r *Messages) Create(ctx context.Context, m Message) (Message, error) {
	created, err := scanMessage(r.db.QueryRowContext(ctx, queryMessageCreate, m.ClientID, m.WAID, m.Chat, m.Direction, m.Body))
	if db.IsUniqueViolation(err) {
		return Message{}, ErrDuplicateMessage
	}
	if err != nil {
		return Message{}, fmt.Errorf("create message: %w", err)
	}
	return created, nil
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
