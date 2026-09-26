package whatsapp

import (
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/androsyz/wasabot/internal/logger"
)

type IncomingMessage struct {
	ID     string
	Chat   string
	Sender string
	Text   string
	SentAt time.Time
}

// MaskedSender is safe to log; the raw sender is a phone number.
func (m IncomingMessage) MaskedSender() string {
	return logger.MaskJIDs(m.Sender)
}

// incomingFrom keeps only plain-text 1:1 messages from other people.
func incomingFrom(evt *events.Message) (IncomingMessage, bool) {
	info := evt.Info
	if info.IsFromMe || info.IsGroup || info.Chat.Server == types.BroadcastServer || evt.IsEdit {
		return IncomingMessage{}, false
	}

	text := evt.Message.GetConversation()
	if text == "" {
		text = evt.Message.GetExtendedTextMessage().GetText()
	}
	if text == "" {
		return IncomingMessage{}, false
	}

	return IncomingMessage{
		ID:     info.ID,
		Chat:   info.Chat.String(),
		Sender: info.Sender.String(),
		Text:   text,
		SentAt: info.Timestamp,
	}, true
}
