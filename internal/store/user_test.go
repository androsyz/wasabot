package store

import (
	"context"
	"errors"
	"testing"

	"github.com/androsyz/wasabot/internal/db/dbtest"
)

func TestUsers_CreateAndGetByID(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	hash := "hash"
	created, err := st.Users.Create(ctx, User{
		Email:        "admin@example.com",
		Name:         "Admin",
		PasswordHash: &hash,
		Role:         RoleSuperAdmin,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.Disabled || created.CreatedAt.IsZero() {
		t.Fatalf("unexpected user: %+v", created)
	}

	got, err := st.Users.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Email != "admin@example.com" || got.Role != RoleSuperAdmin {
		t.Fatalf("got %+v", got)
	}
	if got.PasswordHash == nil || *got.PasswordHash != "hash" {
		t.Fatalf("want password hash %q, got %v", hash, got.PasswordHash)
	}
}

func TestUsers_Create_NullPasswordHash(t *testing.T) {
	st := New(dbtest.New(t))

	created, err := st.Users.Create(context.Background(), User{Email: "oidc@example.com", Name: "OIDC", Role: RoleClientUser})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.PasswordHash != nil {
		t.Fatalf("want nil password hash, got %q", *created.PasswordHash)
	}
}

func TestUsers_Create_EmailTakenIgnoresCase(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	if _, err := st.Users.Create(ctx, User{Email: "a@example.com", Name: "A", Role: RoleClientUser}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := st.Users.Create(ctx, User{Email: "A@EXAMPLE.com", Name: "B", Role: RoleClientUser})
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("got %v, want ErrEmailTaken", err)
	}
}

func TestUsers_Create_InvalidRole(t *testing.T) {
	st := New(dbtest.New(t))

	_, err := st.Users.Create(context.Background(), User{Email: "a@example.com", Name: "A", Role: "root"})
	if err == nil || errors.Is(err, ErrEmailTaken) {
		t.Fatalf("want check constraint error, got %v", err)
	}
}

func TestUsers_GetByEmail(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	created := mustUser(t, st, "a@example.com")

	got, err := st.Users.GetByEmail(ctx, "A@Example.COM")
	if err != nil || got.ID != created.ID {
		t.Fatalf("got %+v, %v", got, err)
	}

	if _, err := st.Users.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestUsers_GetByID_NotFound(t *testing.T) {
	st := New(dbtest.New(t))

	if _, err := st.Users.GetByID(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestUsers_ListAndListByClient(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	b := mustUser(t, st, "b@example.com")
	a := mustUser(t, st, "a@example.com")
	if _, err := st.Users.Create(ctx, User{Email: "c@example.com", Name: "C", Role: RoleClientUser}); err != nil {
		t.Fatalf("create c: %v", err)
	}
	acme := mustClient(t, st, "Acme")
	for _, u := range []User{b, a} {
		if err := st.Clients.AddUser(ctx, acme.ID, u.ID); err != nil {
			t.Fatalf("add user: %v", err)
		}
	}

	all, err := st.Users.List(ctx)
	if err != nil || len(all) != 3 || all[0].Email != "a@example.com" {
		t.Fatalf("list: %+v, %v", all, err)
	}

	members, err := st.Users.ListByClient(ctx, acme.ID)
	if err != nil {
		t.Fatalf("list by client: %v", err)
	}
	if len(members) != 2 || members[0].Email != "a@example.com" || members[1].Email != "b@example.com" {
		t.Fatalf("want [a b], got %+v", members)
	}
}
