package store

import (
	"context"
	"errors"
	"testing"

	"github.com/androsyz/wasabot/internal/db"
	"github.com/androsyz/wasabot/internal/db/dbtest"
)

func TestNew_TransactionScoped(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	errAbort := errors.New("abort")

	err := db.WithTx(ctx, sqlDB, func(tx db.DBTX) error {
		st := New(tx)
		c, err := st.Clients.Create(ctx, "Rolled back")
		if err != nil {
			return err
		}
		u, err := st.Users.Create(ctx, User{Email: "a@example.com", Name: "A", Role: RoleClientUser})
		if err != nil {
			return err
		}
		if err := st.Clients.AddUser(ctx, c.ID, u.ID); err != nil {
			return err
		}
		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("got %v, want errAbort", err)
	}

	st := New(sqlDB)
	if clients := listClients(t, st); len(clients) != 0 {
		t.Fatalf("want rollback, got clients %+v", clients)
	}
	if users := listUsers(t, st); len(users) != 0 {
		t.Fatalf("want rollback, got users %+v", users)
	}

	err = db.WithTx(ctx, sqlDB, func(tx db.DBTX) error {
		_, err := New(tx).Clients.Create(ctx, "Committed")
		return err
	})
	if err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	if clients := listClients(t, st); len(clients) != 1 {
		t.Fatalf("want 1 committed client, got %+v", clients)
	}
}
