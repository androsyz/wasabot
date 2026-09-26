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
	queryClientCreate = `
		INSERT INTO clients (name)
		VALUES (?)
		RETURNING id, name, created_at, updated_at`

	queryClientGetByID = `
		SELECT id, name, created_at, updated_at
		FROM clients
		WHERE id = ?`

	queryClientList = `
		SELECT id, name, created_at, updated_at
		FROM clients
		ORDER BY name`

	queryClientAddUser = `
		INSERT INTO client_users (client_id, user_id)
		VALUES (?, ?)
		ON CONFLICT DO NOTHING`

	queryClientRemoveUser = `
		DELETE FROM client_users
		WHERE client_id = ? AND user_id = ?`

	queryClientListByUser = `
		SELECT c.id, c.name, c.created_at, c.updated_at
		FROM clients c
		JOIN client_users cu ON cu.client_id = c.id
		WHERE cu.user_id = ?
		ORDER BY c.name`
)

type Client struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Clients struct {
	db db.DBTX
}

func NewClients(conn db.DBTX) *Clients {
	return &Clients{db: conn}
}

func (r *Clients) Create(ctx context.Context, name string) (Client, error) {
	c, err := scanClient(r.db.QueryRowContext(ctx, queryClientCreate, name))
	if err != nil {
		return Client{}, fmt.Errorf("create client: %w", err)
	}
	return c, nil
}

func (r *Clients) GetByID(ctx context.Context, id int64) (Client, error) {
	c, err := scanClient(r.db.QueryRowContext(ctx, queryClientGetByID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	if err != nil {
		return Client{}, fmt.Errorf("get client %d: %w", id, err)
	}
	return c, nil
}

func (r *Clients) List(ctx context.Context) ([]Client, error) {
	return r.list(ctx, "list clients", queryClientList)
}

func (r *Clients) ListByUser(ctx context.Context, userID int64) ([]Client, error) {
	return r.list(ctx, fmt.Sprintf("list clients of user %d", userID), queryClientListByUser, userID)
}

// AddUser is idempotent: adding an existing member is not an error.
func (r *Clients) AddUser(ctx context.Context, clientID, userID int64) error {
	if _, err := r.db.ExecContext(ctx, queryClientAddUser, clientID, userID); err != nil {
		return fmt.Errorf("add user %d to client %d: %w", userID, clientID, err)
	}
	return nil
}

func (r *Clients) RemoveUser(ctx context.Context, clientID, userID int64) error {
	if _, err := r.db.ExecContext(ctx, queryClientRemoveUser, clientID, userID); err != nil {
		return fmt.Errorf("remove user %d from client %d: %w", userID, clientID, err)
	}
	return nil
}

func (r *Clients) list(ctx context.Context, op, query string, args ...any) ([]Client, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()

	clients, err := db.Collect(rows, scanClient)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return clients, nil
}

// timestamps are stored as Unix seconds
func scanClient(s db.Scanner) (Client, error) {
	var c Client
	var created, updated int64
	if err := s.Scan(&c.ID, &c.Name, &created, &updated); err != nil {
		return Client{}, err
	}
	c.CreatedAt = time.Unix(created, 0)
	c.UpdatedAt = time.Unix(updated, 0)
	return c, nil
}
