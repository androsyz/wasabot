package store

import (
	"context"
	"errors"
	"testing"

	"github.com/androsyz/wasabot/internal/db/dbtest"
)

func TestMessages_Create(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	c := mustClient(t, st, "Acme")

	in, err := st.Messages.Create(ctx, Message{ClientID: c.ID, WAID: "A1", Chat: "628@s.whatsapp.net", Direction: DirectionIn, Body: "halo"})
	if err != nil {
		t.Fatalf("create in: %v", err)
	}
	if in.ID == 0 || in.Body != "halo" || in.Direction != DirectionIn || in.CreatedAt.IsZero() {
		t.Fatalf("unexpected message: %+v", in)
	}

	if _, err := st.Messages.Create(ctx, Message{ClientID: c.ID, WAID: "B1", Chat: "628@s.whatsapp.net", Direction: DirectionOut, Body: "hi"}); err != nil {
		t.Fatalf("create out: %v", err)
	}
}

func TestMessages_Create_DuplicateWAID(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	c := mustClient(t, st, "Acme")
	m := Message{ClientID: c.ID, WAID: "A1", Chat: "628@s.whatsapp.net", Direction: DirectionIn, Body: "halo"}

	if _, err := st.Messages.Create(ctx, m); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := st.Messages.Create(ctx, m)
	if !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("got %v, want ErrDuplicateMessage", err)
	}
}

func TestMessages_Create_SameWAIDForDifferentClients(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	a := mustClient(t, st, "A")
	b := mustClient(t, st, "B")

	for _, c := range []Client{a, b} {
		m := Message{ClientID: c.ID, WAID: "SAME", Chat: "628@s.whatsapp.net", Direction: DirectionIn, Body: "halo"}
		if _, err := st.Messages.Create(ctx, m); err != nil {
			t.Fatalf("client %s: %v", c.Name, err)
		}
	}
}

func TestMessages_Create_InvalidDirectionAndUnknownClient(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	c := mustClient(t, st, "Acme")

	_, err := st.Messages.Create(ctx, Message{ClientID: c.ID, WAID: "A1", Chat: "c", Direction: "sideways", Body: "x"})
	if err == nil || errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("want check constraint error, got %v", err)
	}

	_, err = st.Messages.Create(ctx, Message{ClientID: 999, WAID: "A2", Chat: "c", Direction: DirectionIn, Body: "x"})
	if err == nil || errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("want foreign key error, got %v", err)
	}
}

func TestMessages_CascadeOnClientDelete(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	st := New(sqlDB)
	c := mustClient(t, st, "Acme")
	mustMessage(t, st, Message{ClientID: c.ID, WAID: "A1", Chat: "c", Direction: DirectionIn, Body: "x"})

	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, c.ID); err != nil {
		t.Fatalf("delete client: %v", err)
	}

	var n int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("want 0 messages after client delete, got %d (%v)", n, err)
	}
}
