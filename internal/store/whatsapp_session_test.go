package store

import (
	"context"
	"errors"
	"testing"

	"github.com/androsyz/wasabot/internal/db/dbtest"
)

func TestWhatsAppSessions_LinkAndGet(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	c := mustClient(t, st, "Acme")

	linked, err := st.WhatsAppSessions.Link(ctx, c.ID, "6281234567890:7@s.whatsapp.net")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if linked.ClientID != c.ID || linked.JID != "6281234567890:7@s.whatsapp.net" || linked.CreatedAt.IsZero() {
		t.Fatalf("unexpected session: %+v", linked)
	}

	got, err := st.WhatsAppSessions.GetByClientID(ctx, c.ID)
	if err != nil || got.JID != linked.JID {
		t.Fatalf("get: %+v, %v", got, err)
	}
}

func TestWhatsAppSessions_GetByClientID_NotFound(t *testing.T) {
	st := New(dbtest.New(t))

	_, err := st.WhatsAppSessions.GetByClientID(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestWhatsAppSessions_Link_ReplacesClientsSession(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	c := mustClient(t, st, "Acme")

	if _, err := st.WhatsAppSessions.Link(ctx, c.ID, "111111111:1@s.whatsapp.net"); err != nil {
		t.Fatalf("link 1: %v", err)
	}
	if _, err := st.WhatsAppSessions.Link(ctx, c.ID, "222222222:2@s.whatsapp.net"); err != nil {
		t.Fatalf("link 2: %v", err)
	}

	all := listSessions(t, st)
	if len(all) != 1 || all[0].JID != "222222222:2@s.whatsapp.net" {
		t.Fatalf("want one replaced session, got %+v", all)
	}
}

func TestWhatsAppSessions_Link_JIDBelongsToOtherClient(t *testing.T) {
	ctx := context.Background()
	st := New(dbtest.New(t))
	a := mustClient(t, st, "A")
	b := mustClient(t, st, "B")

	if _, err := st.WhatsAppSessions.Link(ctx, a.ID, "111111111:1@s.whatsapp.net"); err != nil {
		t.Fatalf("link a: %v", err)
	}

	_, err := st.WhatsAppSessions.Link(ctx, b.ID, "111111111:1@s.whatsapp.net")
	if !errors.Is(err, ErrJIDLinked) {
		t.Fatalf("got %v, want ErrJIDLinked", err)
	}
}

func TestWhatsAppSessions_Link_UnknownClient(t *testing.T) {
	st := New(dbtest.New(t))

	_, err := st.WhatsAppSessions.Link(context.Background(), 999, "111111111:1@s.whatsapp.net")
	if err == nil || errors.Is(err, ErrJIDLinked) {
		t.Fatalf("want foreign key error, got %v", err)
	}
}

func TestWhatsAppSessions_ListAndCascade(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	st := New(sqlDB)
	a := mustClient(t, st, "A")
	b := mustClient(t, st, "B")
	mustLink(t, st, b.ID, "222222222:1@s.whatsapp.net")
	mustLink(t, st, a.ID, "111111111:1@s.whatsapp.net")

	all, err := st.WhatsAppSessions.List(ctx)
	if err != nil || len(all) != 2 || all[0].ClientID != a.ID {
		t.Fatalf("list: %+v, %v", all, err)
	}

	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, a.ID); err != nil {
		t.Fatalf("delete client: %v", err)
	}
	all = listSessions(t, st)
	if len(all) != 1 || all[0].ClientID != b.ID {
		t.Fatalf("want only B's session after deleting A, got %+v", all)
	}
}
