package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestUsers_Count(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))

	if n, err := st.Users.Count(ctx); err != nil || n != 0 {
		t.Fatalf("empty database: %d, %v", n, err)
	}
	mustUser(t, st, "a@example.com")
	mustUser(t, st, "b@example.com")

	if n, err := st.Users.Count(ctx); err != nil || n != 2 {
		t.Fatalf("got %d, %v; want 2", n, err)
	}
}

func TestUsers_CreateIfNone(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	hash := "hash"

	first, err := st.Users.CreateIfNone(ctx, User{Email: "admin@example.com", Name: "Admin", PasswordHash: &hash, Role: RoleSuperAdmin})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.ID == 0 || first.Role != RoleSuperAdmin || first.PasswordHash == nil || *first.PasswordHash != "hash" {
		t.Fatalf("unexpected user: %+v", first)
	}

	_, err = st.Users.CreateIfNone(ctx, User{Email: "other@example.com", Name: "Other", PasswordHash: &hash, Role: RoleSuperAdmin})

	if !errors.Is(err, ErrUsersExist) {
		t.Fatalf("got %v, want ErrUsersExist", err)
	}
	if n, _ := st.Users.Count(ctx); n != 1 {
		t.Fatalf("want exactly one user, got %d", n)
	}
}

func TestUsers_CreateIfNone_OnlyOneOfManySimultaneousCallsWins(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	hash := "hash"

	const attempts = 20
	results := make(chan error, attempts)
	for i := range attempts {
		go func() {
			_, err := st.Users.CreateIfNone(ctx, User{Email: fmt.Sprintf("u%d@example.com", i), Name: "U", PasswordHash: &hash, Role: RoleSuperAdmin})
			results <- err
		}()
	}

	var created, refused int
	for range attempts {
		switch err := <-results; {
		case err == nil:
			created++
		case errors.Is(err, ErrUsersExist):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if created != 1 || refused != attempts-1 {
		t.Fatalf("%d succeeded and %d were refused, want exactly one winner", created, refused)
	}
	if n, _ := st.Users.Count(ctx); n != 1 {
		t.Fatalf("want exactly one user, got %d", n)
	}
}

func TestUsers_SetPasswordHash(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	u := mustUser(t, st, "a@example.com")

	if err := st.Users.SetPasswordHash(ctx, u.ID, "new-hash"); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := st.Users.GetByID(ctx, u.ID)
	if err != nil || got.PasswordHash == nil || *got.PasswordHash != "new-hash" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestUserColumnLists_StayInStep(t *testing.T) {
	var qualified []string
	for _, c := range strings.Split(userColumns, ", ") {
		qualified = append(qualified, "u."+c)
	}

	if got := strings.Join(qualified, ", "); got != userColumnsU {
		t.Fatalf("userColumnsU is out of step with userColumns:\n got  %s\n want %s", userColumnsU, got)
	}
}

func TestUsers_MustChangePassword(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	hash := "hash"

	first, err := st.Users.CreateIfNone(ctx, User{Email: "admin", Name: "Admin", PasswordHash: &hash, Role: RoleSuperAdmin, MustChangePassword: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	other, err := st.Users.Create(ctx, User{Email: "b@example.com", Name: "B", PasswordHash: &hash, Role: RoleClientUser})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !first.MustChangePassword || other.MustChangePassword {
		t.Fatalf("the flag is stored as given: %v and %v", first.MustChangePassword, other.MustChangePassword)
	}
	byID, _ := st.Users.GetByID(ctx, first.ID)
	byEmail, _ := st.Users.GetByEmail(ctx, "admin")
	all, _ := st.Users.List(ctx)
	if !byID.MustChangePassword || !byEmail.MustChangePassword || len(all) != 2 || !all[0].MustChangePassword {
		t.Fatalf("every read path must report the flag: %+v %+v %+v", byID, byEmail, all)
	}
}

func TestUsers_UpdateCredentials(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	hash := "old"
	u, err := st.Users.Create(ctx, User{Email: "admin", Name: "Admin", PasswordHash: &hash, Role: RoleSuperAdmin, MustChangePassword: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := st.Users.UpdateCredentials(ctx, u.ID, "Rina", "rina@example.com", "new-hash"); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := st.Users.GetByID(ctx, u.ID)
	if got.Name != "Rina" || got.Email != "rina@example.com" || got.PasswordHash == nil || *got.PasswordHash != "new-hash" {
		t.Fatalf("got %+v", got)
	}
	if got.MustChangePassword {
		t.Fatal("choosing your own credentials clears the flag")
	}
	if got.Role != RoleSuperAdmin {
		t.Fatal("the role is untouched")
	}
	if _, err := st.Users.GetByEmail(ctx, "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatal("the old login must be gone")
	}
}

func TestUsers_UpdateCredentials_Errors(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	mustUser(t, st, "taken@example.com")
	u := mustUser(t, st, "me@example.com")

	if err := st.Users.UpdateCredentials(ctx, u.ID, "Me", "TAKEN@example.com", "h"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("got %v, want ErrEmailTaken (emails compare case-insensitively)", err)
	}
	if err := st.Users.UpdateCredentials(ctx, 999, "X", "x@example.com", "h"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
	if got, _ := st.Users.GetByID(ctx, u.ID); got.Email != "me@example.com" {
		t.Error("a failed update must change nothing")
	}
}
