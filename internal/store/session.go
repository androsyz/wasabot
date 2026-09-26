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
	querySessionCreate = `
		INSERT INTO sessions (id_hash, user_id, csrf_token, expires_at)
		VALUES (?, ?, ?, ?)`

	querySessionGet = `
		SELECT s.id_hash, s.user_id, s.csrf_token, s.created_at, s.expires_at, ` + userColumnsU + `
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id_hash = ? AND s.expires_at > ?`

	querySessionDelete        = `DELETE FROM sessions WHERE id_hash = ?`
	querySessionDeleteByUser  = `DELETE FROM sessions WHERE user_id = ?`
	querySessionDeleteExpired = `DELETE FROM sessions WHERE expires_at <= ?`
)

// Session is a logged-in browser. IDHash is the SHA-256 of the cookie value: the cookie itself is
// never stored, so a leaked database cannot be replayed as cookies.
type Session struct {
	IDHash    string
	UserID    int64
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Sessions struct {
	db db.DBTX
}

func NewSessions(conn db.DBTX) *Sessions {
	return &Sessions{db: conn}
}

func (r *Sessions) Create(ctx context.Context, s Session) error {
	if _, err := r.db.ExecContext(ctx, querySessionCreate, s.IDHash, s.UserID, s.CSRFToken, s.ExpiresAt.Unix()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Get returns the session and its user, or ErrNotFound if it does not exist or has expired at now.
func (r *Sessions) Get(ctx context.Context, idHash string, now time.Time) (Session, User, error) {
	var (
		s                Session
		us               userScan
		created, expires int64
	)
	dest := append([]any{&s.IDHash, &s.UserID, &s.CSRFToken, &created, &expires}, us.dest()...)
	err := r.db.QueryRowContext(ctx, querySessionGet, idHash, now.Unix()).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, fmt.Errorf("get session: %w", err)
	}

	s.CreatedAt = time.Unix(created, 0)
	s.ExpiresAt = time.Unix(expires, 0)
	return s, us.user(), nil
}

func (r *Sessions) Delete(ctx context.Context, idHash string) error {
	if _, err := r.db.ExecContext(ctx, querySessionDelete, idHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteByUser logs a user out everywhere, for example after a password change.
func (r *Sessions) DeleteByUser(ctx context.Context, userID int64) error {
	if _, err := r.db.ExecContext(ctx, querySessionDeleteByUser, userID); err != nil {
		return fmt.Errorf("delete sessions of user %d: %w", userID, err)
	}
	return nil
}

func (r *Sessions) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, querySessionDeleteExpired, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return n, nil
}
