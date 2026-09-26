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
	queryUserCreate = `
		INSERT INTO users (email, name, password_hash, role)
		VALUES (?, ?, ?, ?)
		RETURNING id, email, name, password_hash, role, disabled, created_at, updated_at`

	queryUserGetByID = `
		SELECT id, email, name, password_hash, role, disabled, created_at, updated_at
		FROM users
		WHERE id = ?`

	queryUserGetByEmail = `
		SELECT id, email, name, password_hash, role, disabled, created_at, updated_at
		FROM users
		WHERE email = ?`

	queryUserList = `
		SELECT id, email, name, password_hash, role, disabled, created_at, updated_at
		FROM users
		ORDER BY email`

	queryUserListByClient = `
		SELECT u.id, u.email, u.name, u.password_hash, u.role, u.disabled, u.created_at, u.updated_at
		FROM users u
		JOIN client_users cu ON cu.user_id = u.id
		WHERE cu.client_id = ?
		ORDER BY u.email`
)

type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleClientUser Role = "client_user"
)

type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash *string
	Role         Role
	Disabled     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Users struct {
	db db.DBTX
}

func NewUsers(conn db.DBTX) *Users {
	return &Users{db: conn}
}

// Create uses Email, Name, PasswordHash and Role from u; the rest is set by the database.
func (r *Users) Create(ctx context.Context, u User) (User, error) {
	created, err := scanUser(r.db.QueryRowContext(ctx, queryUserCreate, u.Email, u.Name, u.PasswordHash, u.Role))
	if db.IsUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (r *Users) GetByID(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx, queryUserGetByID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user %d: %w", id, err)
	}
	return u, nil
}

func (r *Users) GetByEmail(ctx context.Context, email string) (User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx, queryUserGetByEmail, email))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

func (r *Users) List(ctx context.Context) ([]User, error) {
	return r.list(ctx, "list users", queryUserList)
}

func (r *Users) ListByClient(ctx context.Context, clientID int64) ([]User, error) {
	return r.list(ctx, fmt.Sprintf("list users of client %d", clientID), queryUserListByClient, clientID)
}

func (r *Users) list(ctx context.Context, op, query string, args ...any) ([]User, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()

	users, err := db.Collect(rows, scanUser)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return users, nil
}

// timestamps are stored as Unix seconds, disabled as 0/1
func scanUser(s db.Scanner) (User, error) {
	var u User
	var disabled, created, updated int64
	err := s.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &disabled, &created, &updated)
	if err != nil {
		return User{}, err
	}
	u.Disabled = disabled == 1
	u.CreatedAt = time.Unix(created, 0)
	u.UpdatedAt = time.Unix(updated, 0)
	return u, nil
}
