package web

import (
	"context"

	"github.com/androsyz/wasabot/internal/manager"
)

type ClientStatus string

const (
	StatusConnected    ClientStatus = "connected"
	StatusPairing      ClientStatus = "pairing"
	StatusReconnecting ClientStatus = "reconnecting"
	StatusStopped      ClientStatus = "stopped"
	StatusLoggedOut    ClientStatus = "logged_out"
)

func (s ClientStatus) Label() string {
	switch s {
	case StatusConnected:
		return "Connected"
	case StatusPairing:
		return "Pairing"
	case StatusReconnecting:
		return "Reconnecting"
	case StatusStopped:
		return "Stopped"
	default:
		return "Logged out"
	}
}

// Tone picks the status dot color: ok, wait, bad or off.
func (s ClientStatus) Tone() string {
	switch s {
	case StatusConnected:
		return "ok"
	case StatusPairing, StatusReconnecting:
		return "wait"
	case StatusStopped:
		return "bad"
	default:
		return "off"
	}
}

// Runtime is what the dashboard can ask of the running WhatsApp connections; *manager.Manager implements it.
type Runtime interface {
	Status(clientID int64) manager.Status
	Start(clientID int64) error
	Stop(clientID int64)
	Logout(ctx context.Context, clientID int64) error
	Pair(clientID int64) error
	Pairing(clientID int64) (manager.Pairing, bool)
	CancelPair(clientID int64)
}
