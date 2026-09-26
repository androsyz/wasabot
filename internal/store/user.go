package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/androsyz/wasabot/internal/db"
)

// userColumns is the column order scanUser and userScan expect; userColumnsU is the same list for
// queries that join users as "u". Keep the two in step.
const (
	userColumns  = "id, email, name, password_hash, role, disabled, must_change_password, created_at, updated_at"
	userColumnsU = "u.id, u.email, u.name, u.password_hash, u.role, u.disabled, u.must_change_password, u.created_at, u.updated_at"
)

const (
	queryUserCreate = `
		INSERT INTO users (email, name, password_hash, role, must_change_password)
		VALUES (?, ?, ?, ?, ?)
		RETURNING ` + userColumns

	queryUserCount = `SELECT COUNT(*) FROM users`

	queryUserCreateFirst = `
		INSERT INTO users (email, name, password_hash, role, must_change_password)
		SELECT ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM users)
		RETURNING ` + userColumns

	queryUserSetPasswordHash = `UPDATE users SET password_hash = ?, updated_at = unixepoch() WHERE id = ?`

	queryUserUpdateCredentials = `
		UPDATE users
		SET name = ?, email = ?, password_hash = ?, must_change_password = 0, updated_at = unixepoch()
		WHERE id = ?`

	queryUserGetByID    = `SELECT ` + userColumns + ` FROM users WHERE id = ?`
	queryUserGetByEmail = `SELECT ` + userColumns + ` FROM users WHERE email = ?`
	queryUserList       = `SELECT ` + userColumns + ` FROM users ORDER BY email`

	queryUserListByClient = `
		SELECT ` + userColumnsU + `
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
	// MustChangePassword is set on an account made with well-known credentials: it can do nothing
	// but choose its own email and password until that is done.
	MustChangePassword bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Users struct {
	db db.DBTX
}

func NewUsers(conn db.DBTX) *Users {
	return &Users{db: conn}
}

// Create uses Email, Name, PasswordHash, Role and MustChangePassword from u; the rest is set by the database.
func (r *Users) Create(ctx context.Context, u User) (User, error) {
	created, err := scanUser(r.db.QueryRowContext(ctx, queryUserCreate, u.Email, u.Name, u.PasswordHash, u.Role, u.MustChangePassword))
	if db.IsUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (r *Users) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, queryUserCount).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CreateIfNone creates u only if there is no user yet, in one statement, so two simultaneous
// first-run setups cannot both succeed. It returns ErrUsersExist otherwise.
func (r *Users) CreateIfNone(ctx context.Context, u User) (User, error) {
	created, err := scanUser(r.db.QueryRowContext(ctx, queryUserCreateFirst, u.Email, u.Name, u.PasswordHash, u.Role, u.MustChangePassword))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUsersExist
	}
	if err != nil {
		return User{}, fmt.Errorf("create first user: %w", err)
	}
	return created, nil
}

func (r *Users) SetPasswordHash(ctx context.Context, id int64, hash string) error {
	if _, err := r.db.ExecContext(ctx, queryUserSetPasswordHash, hash, id); err != nil {
		return fmt.Errorf("set password of user %d: %w", id, err)
	}
	return nil
}

// UpdateCredentials sets the name, email and password hash and clears MustChangePassword.
func (r *Users) UpdateCredentials(ctx context.Context, id int64, name, email, passwordHash string) error {
	res, err := r.db.ExecContext(ctx, queryUserUpdateCredentials, name, email, passwordHash, id)
	if db.IsUniqueViolation(err) {
		return ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("update credentials of user %d: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
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

// userScan reads the columns of userColumns; session queries reuse it for their joined user.
type userScan struct {
	u                                      User
	disabled, mustChange, created, updated int64
}

func (us *userScan) dest() []any {
	return []any{&us.u.ID, &us.u.Email, &us.u.Name, &us.u.PasswordHash, &us.u.Role, &us.disabled, &us.mustChange, &us.created, &us.updated}
}

// user converts the stored flags and Unix-second timestamps.
func (us *userScan) user() User {
	u := us.u
	u.Disabled = us.disabled == 1
	u.MustChangePassword = us.mustChange == 1
	u.CreatedAt = time.Unix(us.created, 0)
	u.UpdatedAt = time.Unix(us.updated, 0)
	return u
}

func scanUser(s db.Scanner) (User, error) {
	var us userScan
	if err := s.Scan(us.dest()...); err != nil {
		return User{}, err
	}
	return us.user(), nil
}
