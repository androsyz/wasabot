package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/androsyz/wasabot/internal/db/dbtest"
)

func newSession(t *testing.T, st *Store, u User, hash string, ttl time.Duration) Session {
	t.Helper()
	s := Session{IDHash: hash, UserID: u.ID, CSRFToken: "csrf-" + hash, ExpiresAt: time.Now().Add(ttl)}
	if err := st.Sessions.Create(context.Background(), s); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s
}

func TestSessions_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	hash := "abc"
	u, err := st.Users.Create(ctx, User{Email: "a@example.com", Name: "A", PasswordHash: &hash, Role: RoleSuperAdmin})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	created := newSession(t, st, u, "hash1", time.Hour)

	gotSession, gotUser, err := st.Sessions.Get(ctx, "hash1", time.Now())
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if gotSession.CSRFToken != "csrf-hash1" || gotSession.UserID != u.ID || gotSession.ExpiresAt.Unix() != created.ExpiresAt.Unix() {
		t.Errorf("session = %+v", gotSession)
	}
	if gotUser.ID != u.ID || gotUser.Email != "a@example.com" || gotUser.Role != RoleSuperAdmin || gotUser.PasswordHash == nil {
		t.Errorf("user = %+v", gotUser)
	}
}

func TestSessions_Get_NotFoundOrExpired(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	u := mustUser(t, st, "a@example.com")
	newSession(t, st, u, "live", time.Hour)
	newSession(t, st, u, "dead", -time.Minute)

	for _, hash := range []string{"unknown", "dead"} {
		if _, _, err := st.Sessions.Get(ctx, hash, time.Now()); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", hash, err)
		}
	}
	if _, _, err := st.Sessions.Get(ctx, "live", time.Now()); err != nil {
		t.Errorf("a live session must be found: %v", err)
	}
	if _, _, err := st.Sessions.Get(ctx, "live", time.Now().Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("the same session two hours later = %v, want ErrNotFound", err)
	}
}

func TestSessions_Delete(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	u := mustUser(t, st, "a@example.com")
	newSession(t, st, u, "one", time.Hour)
	newSession(t, st, u, "two", time.Hour)

	if err := st.Sessions.Delete(ctx, "one"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, _, err := st.Sessions.Get(ctx, "one", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Error("the deleted session must be gone")
	}
	if _, _, err := st.Sessions.Get(ctx, "two", time.Now()); err != nil {
		t.Error("other sessions stay")
	}
	if err := st.Sessions.Delete(ctx, "never-existed"); err != nil {
		t.Errorf("deleting nothing is not an error: %v", err)
	}
}

func TestSessions_DeleteByUser(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	a := mustUser(t, st, "a@example.com")
	b := mustUser(t, st, "b@example.com")
	newSession(t, st, a, "a1", time.Hour)
	newSession(t, st, a, "a2", time.Hour)
	newSession(t, st, b, "b1", time.Hour)

	if err := st.Sessions.DeleteByUser(ctx, a.ID); err != nil {
		t.Fatalf("delete by user: %v", err)
	}

	for _, hash := range []string{"a1", "a2"} {
		if _, _, err := st.Sessions.Get(ctx, hash, time.Now()); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s should be gone", hash)
		}
	}
	if _, _, err := st.Sessions.Get(ctx, "b1", time.Now()); err != nil {
		t.Error("another user's session must stay")
	}
}

func TestSessions_DeleteExpired(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	u := mustUser(t, st, "a@example.com")
	newSession(t, st, u, "live", time.Hour)
	newSession(t, st, u, "dead1", -time.Hour)
	newSession(t, st, u, "dead2", -time.Minute)

	n, err := st.Sessions.DeleteExpired(ctx, time.Now())

	if err != nil || n != 2 {
		t.Fatalf("deleted %d, %v; want 2", n, err)
	}
	if _, _, err := st.Sessions.Get(ctx, "live", time.Now()); err != nil {
		t.Error("the live session must survive")
	}
}

func TestSessions_CascadeWhenTheUserIsDeleted(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	st := New(sqlDB)
	u := mustUser(t, st, "a@example.com")
	newSession(t, st, u, "s1", time.Hour)

	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, _, err := st.Sessions.Get(ctx, "s1", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want the session gone with its user", err)
	}
}

func TestSessions_ReportsADisabledUser(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	st := New(sqlDB)
	u := mustUser(t, st, "a@example.com")
	newSession(t, st, u, "s1", time.Hour)
	if _, err := sqlDB.ExecContext(ctx, `UPDATE users SET disabled = 1 WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, got, err := st.Sessions.Get(ctx, "s1", time.Now())

	if err != nil || !got.Disabled {
		t.Fatalf("got %+v, %v; the caller needs to see that the user is disabled", got, err)
	}
}

func TestSessions_ReportsMustChangePassword(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	hash := "h"
	u, err := st.Users.Create(ctx, User{Email: "admin", Name: "Admin", PasswordHash: &hash, Role: RoleSuperAdmin, MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	newSession(t, st, u, "s1", time.Hour)

	_, got, err := st.Sessions.Get(ctx, "s1", time.Now())

	if err != nil || !got.MustChangePassword {
		t.Fatalf("got %+v, %v: the web layer needs the flag on every request", got, err)
	}
}
