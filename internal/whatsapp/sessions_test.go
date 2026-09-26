package whatsapp

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"github.com/androsyz/wasabot/internal/db/dbtest"
)

func TestSetDeviceName(t *testing.T) {
	prev := store.DeviceProps.GetOs()
	t.Cleanup(func() { store.SetOSInfo(prev, [3]uint32{0, 1, 0}) })

	SetDeviceName("wasabot")

	if got := store.DeviceProps.GetOs(); got != "wasabot" {
		t.Fatalf("got %q, want wasabot", got)
	}
}

func TestNewSessions_SharesDatabaseAndIsRepeatable(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)

	for range 2 {
		if _, err := NewSessions(ctx, sqlDB, slog.Default()); err != nil {
			t.Fatalf("new sessions: %v", err)
		}
	}

	var n int
	err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('whatsmeow_device', 'clients')`).Scan(&n)
	if err != nil {
		t.Fatalf("query schema: %v", err)
	}
	if n != 2 {
		t.Fatalf("want whatsmeow_device and clients in the same db, got %d tables", n)
	}
}

func TestSessions_NewClient_EmptyJIDIsUnpaired(t *testing.T) {
	ctx := context.Background()
	s, err := NewSessions(ctx, dbtest.New(t), slog.Default())
	if err != nil {
		t.Fatalf("new sessions: %v", err)
	}

	c, err := s.NewClient(ctx, "", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if c.Paired() {
		t.Fatal("new device must not be paired")
	}
	if !c.wm.ManualHistorySyncDownload {
		t.Fatal("history sync download must be disabled")
	}
}

func TestSessions_NewClient_ResumesStoredDevice(t *testing.T) {
	ctx := context.Background()
	s, err := NewSessions(ctx, dbtest.New(t), slog.Default())
	if err != nil {
		t.Fatalf("new sessions: %v", err)
	}

	jid := types.JID{User: "6281234567890", Device: 7, Server: types.DefaultUserServer}
	dev := s.container.NewDevice()
	dev.ID = &jid
	// normally filled in during pairing; the columns are NOT NULL with fixed-size signatures
	dev.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte("details"),
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
	if err := s.container.PutDevice(ctx, dev); err != nil {
		t.Fatalf("put device: %v", err)
	}

	c, err := s.NewClient(ctx, jid.String(), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if !c.Paired() {
		t.Fatal("stored device must be paired")
	}
}

func TestSessions_NewClient_UnknownJID(t *testing.T) {
	ctx := context.Background()
	s, err := NewSessions(ctx, dbtest.New(t), slog.Default())
	if err != nil {
		t.Fatalf("new sessions: %v", err)
	}

	_, err = s.NewClient(ctx, "6289999999999:3@s.whatsapp.net", &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error for a JID with no stored device")
	}
	if got := err.Error(); bytes.Contains([]byte(got), []byte("6289999999999")) {
		t.Fatalf("error must not contain the raw phone number: %q", got)
	}
}
