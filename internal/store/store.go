package store

import "github.com/androsyz/wasabot/internal/db"

type Store struct {
	Clients          *Clients
	Users            *Users
	WhatsAppSessions *WhatsAppSessions
	Messages         *Messages
}

// New accepts *sql.DB or, inside db.WithTx, the *sql.Tx, so store.New(tx) is a transaction-scoped Store.
func New(conn db.DBTX) *Store {
	return &Store{
		Clients:          NewClients(conn),
		Users:            NewUsers(conn),
		WhatsAppSessions: NewWhatsAppSessions(conn),
		Messages:         NewMessages(conn),
	}
}
