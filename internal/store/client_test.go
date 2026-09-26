package store

import (
	"context"
	"errors"
	"testing"

	"github.com/androsyz/wasabot/internal/db/dbtest"
)

func TestClients_CreateAndGetByID(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	created, err := st.Clients.Create(ctx, "Acme")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.Name != "Acme" || created.CreatedAt.IsZero() {
		t.Fatalf("unexpected client: %+v", created)
	}

	got, err := st.Clients.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID || got.Name != "Acme" {
		t.Fatalf("got %+v, want %+v", got, created)
	}
}

func TestClients_GetByID_NotFound(t *testing.T) {
	st := New(dbtest.New(t))

	_, err := st.Clients.GetByID(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestClients_List(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	for _, name := range []string{"Beta", "Alpha"} {
		if _, err := st.Clients.Create(ctx, name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	got, err := st.Clients.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Name != "Alpha" || got[1].Name != "Beta" {
		t.Fatalf("want [Alpha Beta], got %+v", got)
	}
}

func TestClients_Membership(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	beta := mustClient(t, st, "Beta")
	alpha := mustClient(t, st, "Alpha")
	u, err := st.Users.Create(ctx, User{Email: "a@example.com", Name: "A", Role: RoleClientUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	for _, c := range []Client{beta, alpha, alpha} {
		if err := st.Clients.AddUser(ctx, c.ID, u.ID); err != nil {
			t.Fatalf("add user to %s: %v", c.Name, err)
		}
	}

	got, err := st.Clients.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(got) != 2 || got[0].Name != "Alpha" || got[1].Name != "Beta" {
		t.Fatalf("want [Alpha Beta], got %+v", got)
	}

	if err := st.Clients.RemoveUser(ctx, alpha.ID, u.ID); err != nil {
		t.Fatalf("remove user: %v", err)
	}
	got, err = st.Clients.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("list by user after remove: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Beta" {
		t.Fatalf("want [Beta], got %+v", got)
	}
}

func TestClients_AddUser_UnknownIDs(t *testing.T) {
	st := New(dbtest.New(t))

	if err := st.Clients.AddUser(context.Background(), 1, 1); err == nil {
		t.Fatal("want foreign key error, got nil")
	}
}

func TestClients_MembershipCascadesOnDelete(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	st := New(sqlDB)

	c := mustClient(t, st, "Acme")
	u := mustUser(t, st, "a@example.com")
	if err := st.Clients.AddUser(ctx, c.ID, u.ID); err != nil {
		t.Fatalf("add user: %v", err)
	}

	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var n int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM client_users`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 memberships after user delete, got %d", n)
	}
}
