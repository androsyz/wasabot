package main

import (
	"context"
	"testing"

	"github.com/androsyz/wasabot/internal/db/dbtest"
	"github.com/androsyz/wasabot/internal/store"
)

func TestApp_FirstClient_CreatesDefaultOnce(t *testing.T) {
	ctx := context.Background()
	a := &app{store: store.New(dbtest.New(t))}

	first, err := a.firstClient(ctx)
	if err != nil {
		t.Fatalf("first client: %v", err)
	}
	if first.Name != defaultClientName {
		t.Fatalf("got %q, want %q", first.Name, defaultClientName)
	}

	again, err := a.firstClient(ctx)
	if err != nil || again.ID != first.ID {
		t.Fatalf("second call must reuse the client: %+v, %v", again, err)
	}
	all, err := a.store.Clients.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("want exactly one client, got %+v (%v)", all, err)
	}
}

func TestApp_FirstClient_UsesExistingClient(t *testing.T) {
	ctx := context.Background()
	a := &app{store: store.New(dbtest.New(t))}
	existing, err := a.store.Clients.Create(ctx, "Toko Budi")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	got, err := a.firstClient(ctx)
	if err != nil || got.ID != existing.ID {
		t.Fatalf("got %+v, %v; want the existing client", got, err)
	}
}
