package web

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

// Runtime reports what the running process knows about a client's WhatsApp session.
type Runtime interface {
	Status(clientID int64) ClientStatus
}
