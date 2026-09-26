package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestMessages_ListRecent(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	a := mustClient(t, st, "A")
	b := mustClient(t, st, "B")
	const chat = "628@s.whatsapp.net"

	for i, body := range []string{"one", "two", "three", "four"} {
		dir := DirectionIn
		if i%2 == 1 {
			dir = DirectionOut
		}
		mustMessage(t, st, Message{ClientID: a.ID, WAID: "M" + body, Chat: chat, Direction: dir, Body: body})
	}
	mustMessage(t, st, Message{ClientID: a.ID, WAID: "other-chat", Chat: "999@s.whatsapp.net", Direction: DirectionIn, Body: "elsewhere"})
	mustMessage(t, st, Message{ClientID: b.ID, WAID: "Mone", Chat: chat, Direction: DirectionIn, Body: "other client"})

	bodies := func(msgs []Message) string {
		var out []string
		for _, m := range msgs {
			out = append(out, m.Body)
		}
		return strings.Join(out, ",")
	}

	t.Run("ends at the given message and is oldest first", func(t *testing.T) {
		got, err := st.Messages.ListRecent(ctx, a.ID, chat, "Mthree", 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if bodies(got) != "one,two,three" {
			t.Fatalf("got %s, want one,two,three (later messages must be excluded)", bodies(got))
		}
		if got[1].Direction != DirectionOut {
			t.Fatalf("direction not preserved: %+v", got[1])
		}
	})

	t.Run("limit keeps the newest", func(t *testing.T) {
		got, err := st.Messages.ListRecent(ctx, a.ID, chat, "Mfour", 2)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if bodies(got) != "three,four" {
			t.Fatalf("got %s, want three,four", bodies(got))
		}
	})

	t.Run("scoped to the client and chat", func(t *testing.T) {
		got, err := st.Messages.ListRecent(ctx, b.ID, chat, "Mone", 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if bodies(got) != "other client" {
			t.Fatalf("got %s, want only the other client's message", bodies(got))
		}
	})

	t.Run("unknown message ID returns nothing", func(t *testing.T) {
		got, err := st.Messages.ListRecent(ctx, a.ID, chat, "nope", 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %+v, %v; want no messages", got, err)
		}
	})
}

func TestMessages_Create_HonorsAnExplicitCreatedAt(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	c := mustClient(t, st, "Acme")
	sent := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)

	explicit, err := st.Messages.Create(ctx, Message{ClientID: c.ID, WAID: "A1", Chat: "c", Direction: DirectionIn, Body: "x", CreatedAt: sent})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !explicit.CreatedAt.Equal(sent) {
		t.Errorf("created_at = %v, want the given %v", explicit.CreatedAt, sent)
	}

	implicit, err := st.Messages.Create(ctx, Message{ClientID: c.ID, WAID: "A2", Chat: "c", Direction: DirectionIn, Body: "y"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if time.Since(implicit.CreatedAt) > time.Minute {
		t.Errorf("created_at = %v, want about now", implicit.CreatedAt)
	}
}

func TestMessages_ListUnanswered(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	a := mustClient(t, st, "A")
	b := mustClient(t, st, "B")
	now := time.Now()

	add := func(clientID int64, waID, chat string, dir Direction, age time.Duration) {
		t.Helper()
		mustMessage(t, st, Message{ClientID: clientID, WAID: waID, Chat: chat, Direction: dir, Body: waID, CreatedAt: now.Add(-age)})
	}
	add(a.ID, "waiting-1", "waiting", DirectionIn, 2*time.Minute)
	add(a.ID, "waiting-2", "waiting", DirectionIn, time.Minute) // the newest one is the one that counts
	add(a.ID, "answered-1", "answered", DirectionIn, 3*time.Minute)
	add(a.ID, "answered-2", "answered", DirectionOut, 2*time.Minute)
	add(a.ID, "too-old", "old", DirectionIn, time.Hour)
	add(a.ID, "asked-again", "again", DirectionOut, 5*time.Minute)
	add(a.ID, "again-2", "again", DirectionIn, time.Minute)
	add(b.ID, "other-client", "waiting", DirectionIn, time.Minute)

	got, err := st.Messages.ListUnanswered(ctx, a.ID, now.Add(-10*time.Minute))
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var ids []string
	for _, m := range got {
		ids = append(ids, m.WAID)
	}
	if want := "waiting-2,again-2"; strings.Join(ids, ",") != want {
		t.Fatalf("got %v, want %s: the latest incoming message of each chat that has no reply after it, within the window", ids, want)
	}

	all, err := st.Messages.ListUnanswered(ctx, a.ID, time.Time{})
	if err != nil {
		t.Fatalf("list without a window: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("a zero since must not exclude old messages, got %d messages", len(all))
	}
}

func TestMessages_CountSince(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	a := mustClient(t, st, "A")
	b := mustClient(t, st, "B")
	idle := mustClient(t, st, "Idle")
	now := time.Now()

	add := func(c Client, waID string, dir Direction, age time.Duration) {
		t.Helper()
		mustMessage(t, st, Message{ClientID: c.ID, WAID: waID, Chat: "c", Direction: dir, Body: "x", CreatedAt: now.Add(-age)})
	}
	add(a, "a1", DirectionIn, time.Hour)
	add(a, "a2", DirectionOut, 2*time.Hour)
	add(a, "a-old", DirectionIn, 48*time.Hour)
	add(b, "b1", DirectionIn, time.Minute)

	got, err := st.Messages.CountSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if got[a.ID] != 2 || got[b.ID] != 1 {
		t.Fatalf("got %v, want client A: 2 (both directions, the old one excluded), client B: 1", got)
	}
	if _, ok := got[idle.ID]; ok {
		t.Fatalf("a client without messages has no entry, got %v", got)
	}
}
