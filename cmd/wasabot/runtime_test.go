package main

import (
	"testing"

	"github.com/androsyz/wasabot/internal/web"
)

type fakeConn struct{ up bool }

func (f *fakeConn) Connected() bool { return f.up }

func TestRuntime_Status(t *testing.T) {
	rt := newRuntime()

	if got := rt.Status(1); got != web.StatusLoggedOut {
		t.Errorf("unknown client = %q, want logged out", got)
	}

	rt.setLinked(1, true)
	if got := rt.Status(1); got != web.StatusStopped {
		t.Errorf("linked but not running = %q, want stopped", got)
	}

	conn := &fakeConn{}
	rt.setRunning(1, conn)
	if got := rt.Status(1); got != web.StatusReconnecting {
		t.Errorf("running but not connected = %q, want reconnecting", got)
	}

	conn.up = true
	if got := rt.Status(1); got != web.StatusConnected {
		t.Errorf("connected = %q, want connected", got)
	}

	rt.setPairing(1, true)
	if got := rt.Status(1); got != web.StatusPairing {
		t.Errorf("pairing wins over everything else, got %q", got)
	}

	rt.setPairing(1, false)
	rt.setRunning(1, nil)
	if got := rt.Status(1); got != web.StatusStopped {
		t.Errorf("after stopping = %q, want stopped", got)
	}
}
