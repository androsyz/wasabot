package main

import (
	"sync"

	"github.com/androsyz/wasabot/internal/web"
)

type connector interface {
	Connected() bool
}

// runtime tracks what this process is doing with each client's WhatsApp session,
// for the dashboard. It is safe for concurrent use.
type runtime struct {
	mu      sync.RWMutex
	linked  map[int64]bool
	pairing map[int64]bool
	running map[int64]connector
}

func newRuntime() *runtime {
	return &runtime{
		linked:  make(map[int64]bool),
		pairing: make(map[int64]bool),
		running: make(map[int64]connector),
	}
}

func (r *runtime) setLinked(clientID int64, linked bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.linked[clientID] = linked
}

func (r *runtime) setPairing(clientID int64, pairing bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pairing[clientID] = pairing
}

// setRunning records the live connection of a client; nil means it is no longer running.
func (r *runtime) setRunning(clientID int64, c connector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c == nil {
		delete(r.running, clientID)
		return
	}
	r.running[clientID] = c
}

func (r *runtime) Status(clientID int64) web.ClientStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch c, running := r.running[clientID]; {
	case r.pairing[clientID]:
		return web.StatusPairing
	case running && c.Connected():
		return web.StatusConnected
	case running:
		return web.StatusReconnecting
	case r.linked[clientID]:
		return web.StatusStopped
	default:
		return web.StatusLoggedOut
	}
}
