package store

import (
	"context"
	"testing"
)

func mustClient(t *testing.T, st *Store, name string) Client {
	t.Helper()
	c, err := st.Clients.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("create client %q: %v", name, err)
	}
	return c
}

func mustUser(t *testing.T, st *Store, email string) User {
	t.Helper()
	u, err := st.Users.Create(context.Background(), User{Email: email, Name: email, Role: RoleClientUser})
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return u
}

func mustLink(t *testing.T, st *Store, clientID int64, jid string) {
	t.Helper()
	if _, err := st.WhatsAppSessions.Link(context.Background(), clientID, jid); err != nil {
		t.Fatalf("link client %d to %q: %v", clientID, jid, err)
	}
}

func mustMessage(t *testing.T, st *Store, m Message) {
	t.Helper()
	if _, err := st.Messages.Create(context.Background(), m); err != nil {
		t.Fatalf("create message %q: %v", m.WAID, err)
	}
}

func listClients(t *testing.T, st *Store) []Client {
	t.Helper()
	got, err := st.Clients.List(context.Background())
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	return got
}

func listUsers(t *testing.T, st *Store) []User {
	t.Helper()
	got, err := st.Users.List(context.Background())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	return got
}

func listSessions(t *testing.T, st *Store) []WhatsAppSession {
	t.Helper()
	got, err := st.WhatsAppSessions.List(context.Background())
	if err != nil {
		t.Fatalf("list whatsapp sessions: %v", err)
	}
	return got
}
