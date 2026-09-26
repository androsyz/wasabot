package whatsapp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
)

func TestClient_forwardQR(t *testing.T) {
	var logs bytes.Buffer
	c := &Client{log: slog.New(slog.NewTextHandler(&logs, nil))}
	var got []QREvent
	c.OnQR(func(e QREvent) { got = append(got, e) })

	items := make(chan whatsmeow.QRChannelItem, 4)
	items <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@secret-pairing-code", Timeout: time.Minute}
	items <- whatsmeow.QRChannelSuccess
	items <- whatsmeow.QRChannelTimeout
	close(items)

	c.forwardQR(items)

	if len(got) != 2 || got[0].Code != "2@secret-pairing-code" || got[1].Err == nil {
		t.Fatalf("want the code, then the timeout as an error; got %+v", got)
	}
	if strings.Contains(logs.String(), "secret-pairing-code") {
		t.Fatalf("pairing code must never be logged: %q", logs.String())
	}
}

func TestClient_forwardQRWithoutAHandler(t *testing.T) {
	c := &Client{log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}

	items := make(chan whatsmeow.QRChannelItem, 1)
	items <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@x"}
	close(items)

	c.forwardQR(items) // must not panic
}
