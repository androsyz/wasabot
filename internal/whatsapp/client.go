package whatsapp

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/androsyz/wasabot/internal/logger"
)

type Client struct {
	wm    *whatsmeow.Client
	log   *slog.Logger
	qrOut io.Writer
}

// NewClient does not connect. An empty jid creates a new device to pair.
// The QR code goes to qrOut, never to the logs: it is a pairing credential.
func (s *Sessions) NewClient(ctx context.Context, jid string, qrOut io.Writer) (*Client, error) {
	dev, err := s.device(ctx, jid)
	if err != nil {
		return nil, err
	}

	wm := whatsmeow.NewClient(dev, logger.WhatsApp(s.log))
	// A bot has no use for the phone's chat history, and syncing it fills the database with
	// contacts and message keys. The sync is still acknowledged.
	wm.ManualHistorySyncDownload = true

	return &Client{wm: wm, log: s.log, qrOut: qrOut}, nil
}

// Connected is true once the connection is up and authenticated.
func (c *Client) Connected() bool {
	return c.wm.IsConnected() && c.wm.IsLoggedIn()
}

func (c *Client) Paired() bool {
	return c.wm.Store.ID != nil
}

// Connect reconnects from the stored device, or starts QR pairing if there is none.
// Pairing continues in the background; ctx cancels it.
func (c *Client) Connect(ctx context.Context) error {
	if !c.Paired() {
		// must be requested before Connect
		items, err := c.wm.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("get qr channel: %w", err)
		}
		go c.showQR(items)
	}

	if err := c.wm.Connect(); err != nil {
		return fmt.Errorf("connect whatsapp: %w", err)
	}
	return nil
}

func (c *Client) OnPaired(h func(jid string)) {
	c.wm.AddEventHandler(func(evt any) {
		if e, ok := evt.(*events.PairSuccess); ok {
			h(e.ID.String())
		}
	})
}

// OnConnected calls h each time the connection is established and authenticated, including reconnects.
func (c *Client) OnConnected(h func()) {
	c.wm.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.Connected); ok {
			h()
		}
	})
}

// OnMessage runs h on whatsmeow's event loop, so h must return quickly.
func (c *Client) OnMessage(h func(IncomingMessage)) {
	c.wm.AddEventHandler(func(evt any) {
		e, ok := evt.(*events.Message)
		if !ok {
			return
		}
		if msg, ok := incomingFrom(e); ok {
			h(msg)
		}
	})
}

// Send returns the WhatsApp ID of the sent message.
func (c *Client) Send(ctx context.Context, chat, text string) (string, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("parse chat jid: %w", err)
	}
	resp, err := c.wm.SendMessage(ctx, to, &waE2E.Message{Conversation: &text})
	if err != nil {
		return "", fmt.Errorf("send whatsapp message: %w", err)
	}
	return resp.ID, nil
}

func (c *Client) Disconnect() {
	c.wm.Disconnect()
}

func (c *Client) showQR(items <-chan whatsmeow.QRChannelItem) {
	for item := range items {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, c.qrOut)
			c.log.Info("scan the QR code in WhatsApp > Linked devices", "expires_in", item.Timeout)
		case whatsmeow.QRChannelEventError:
			c.log.Error("whatsapp pairing failed", "error", item.Error)
		default:
			c.log.Info("whatsapp pairing", "event", item.Event)
		}
	}
}
