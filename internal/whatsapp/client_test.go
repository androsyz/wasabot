package whatsapp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
)

func TestClient_showQR(t *testing.T) {
	var qr, logs bytes.Buffer
	c := &Client{
		log:   slog.New(slog.NewTextHandler(&logs, nil)),
		qrOut: &qr,
	}

	items := make(chan whatsmeow.QRChannelItem, 3)
	items <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@secret-pairing-code", Timeout: time.Minute}
	items <- whatsmeow.QRChannelSuccess
	close(items)

	c.showQR(items)

	if qr.Len() == 0 {
		t.Fatal("want QR rendered to qrOut")
	}
	if !strings.Contains(logs.String(), "event=success") {
		t.Fatalf("want success logged, got %q", logs.String())
	}
	if strings.Contains(logs.String(), "secret-pairing-code") {
		t.Fatalf("pairing code must never be logged: %q", logs.String())
	}
}
