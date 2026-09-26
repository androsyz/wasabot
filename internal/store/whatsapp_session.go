package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/androsyz/wasabot/internal/db"
)

const (
	queryWhatsAppSessionLink = `
		INSERT INTO whatsapp_sessions (client_id, jid)
		VALUES (?, ?)
		ON CONFLICT (client_id) DO UPDATE SET jid = excluded.jid, created_at = unixepoch()
		RETURNING client_id, jid, created_at`

	queryWhatsAppSessionUnlink = `DELETE FROM whatsapp_sessions WHERE client_id = ?`

	queryWhatsAppSessionGetByClientID = `
		SELECT client_id, jid, created_at
		FROM whatsapp_sessions
		WHERE client_id = ?`

	queryWhatsAppSessionList = `
		SELECT client_id, jid, created_at
		FROM whatsapp_sessions
		ORDER BY client_id`
)

// WhatsAppSession links a client to the WhatsApp device (JID) paired for it. One per client.
type WhatsAppSession struct {
	ClientID  int64
	JID       string
	CreatedAt time.Time
}

type WhatsAppSessions struct {
	db db.DBTX
}

func NewWhatsAppSessions(conn db.DBTX) *WhatsAppSessions {
	return &WhatsAppSessions{db: conn}
}

// Link replaces the client's existing session, if any.
func (r *WhatsAppSessions) Link(ctx context.Context, clientID int64, jid string) (WhatsAppSession, error) {
	s, err := scanWhatsAppSession(r.db.QueryRowContext(ctx, queryWhatsAppSessionLink, clientID, jid))
	if db.IsUniqueViolation(err) {
		return WhatsAppSession{}, ErrJIDLinked
	}
	if err != nil {
		return WhatsAppSession{}, fmt.Errorf("link whatsapp session for client %d: %w", clientID, err)
	}
	return s, nil
}

// Unlink is not an error when the client has no session.
func (r *WhatsAppSessions) Unlink(ctx context.Context, clientID int64) error {
	if _, err := r.db.ExecContext(ctx, queryWhatsAppSessionUnlink, clientID); err != nil {
		return fmt.Errorf("unlink whatsapp session of client %d: %w", clientID, err)
	}
	return nil
}

func (r *WhatsAppSessions) GetByClientID(ctx context.Context, clientID int64) (WhatsAppSession, error) {
	s, err := scanWhatsAppSession(r.db.QueryRowContext(ctx, queryWhatsAppSessionGetByClientID, clientID))
	if errors.Is(err, sql.ErrNoRows) {
		return WhatsAppSession{}, ErrNotFound
	}
	if err != nil {
		return WhatsAppSession{}, fmt.Errorf("get whatsapp session of client %d: %w", clientID, err)
	}
	return s, nil
}

func (r *WhatsAppSessions) List(ctx context.Context) ([]WhatsAppSession, error) {
	rows, err := r.db.QueryContext(ctx, queryWhatsAppSessionList)
	if err != nil {
		return nil, fmt.Errorf("list whatsapp sessions: %w", err)
	}
	defer rows.Close()

	sessions, err := db.Collect(rows, scanWhatsAppSession)
	if err != nil {
		return nil, fmt.Errorf("list whatsapp sessions: %w", err)
	}
	return sessions, nil
}

func scanWhatsAppSession(s db.Scanner) (WhatsAppSession, error) {
	var ws WhatsAppSession
	var created int64
	if err := s.Scan(&ws.ClientID, &ws.JID, &created); err != nil {
		return WhatsAppSession{}, err
	}
	ws.CreatedAt = time.Unix(created, 0)
	return ws, nil
}
