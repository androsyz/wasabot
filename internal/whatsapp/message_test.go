package whatsapp

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func newEvent(text string, mutate func(*events.Message)) *events.Message {
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   types.NewJID("6281234567890", types.DefaultUserServer),
				Sender: types.NewJID("6281234567890", types.DefaultUserServer),
			},
			ID: "MSG1",
		},
		Message: &waE2E.Message{Conversation: &text},
	}
	if mutate != nil {
		mutate(evt)
	}
	return evt
}

func TestIncomingFrom_PlainText(t *testing.T) {
	msg, ok := incomingFrom(newEvent("halo", nil))
	if !ok || msg.Text != "halo" || msg.ID != "MSG1" {
		t.Fatalf("got %+v, %v", msg, ok)
	}
}

func TestIncomingFrom_ExtendedText(t *testing.T) {
	evt := newEvent("", func(e *events.Message) {
		text := "cek https://example.com"
		e.Message = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &text}}
	})

	msg, ok := incomingFrom(evt)
	if !ok || msg.Text != "cek https://example.com" {
		t.Fatalf("got %+v, %v", msg, ok)
	}
}

func TestIncomingFrom_Ignored(t *testing.T) {
	tests := map[string]*events.Message{
		"from me": newEvent("hi", func(e *events.Message) { e.Info.IsFromMe = true }),
		"group":   newEvent("hi", func(e *events.Message) { e.Info.IsGroup = true }),
		"status broadcast": newEvent("hi", func(e *events.Message) {
			e.Info.Chat = types.NewJID("status", types.BroadcastServer)
		}),
		"edit":     newEvent("hi", func(e *events.Message) { e.IsEdit = true }),
		"no text":  newEvent("", nil),
		"no proto": newEvent("hi", func(e *events.Message) { e.Message = nil }),
	}
	for name, evt := range tests {
		t.Run(name, func(t *testing.T) {
			if msg, ok := incomingFrom(evt); ok {
				t.Fatalf("want ignored, got %+v", msg)
			}
		})
	}
}

func TestIncomingMessage_MaskedSender(t *testing.T) {
	msg, ok := incomingFrom(newEvent("halo", nil))
	if !ok {
		t.Fatal("want the message to be accepted")
	}

	got := msg.MaskedSender()
	if strings.Contains(got, "6281234567890") || !strings.Contains(got, "62****890") {
		t.Fatalf("sender must be masked, got %q", got)
	}
}
