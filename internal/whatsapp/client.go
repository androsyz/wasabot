package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/androsyz/wasabot/internal/logger"
)

type Client struct {
	wm   *whatsmeow.Client
	log  *slog.Logger
	onQR func(QREvent)
}

// QREvent is one step of pairing: a code to show, or the reason pairing ended without success.
type QREvent struct {
	Code string
	Err  error
}

// NewClient does not connect. An empty jid creates a new device to pair.
func (s *Sessions) NewClient(ctx context.Context, jid string) (*Client, error) {
	dev, err := s.device(ctx, jid)
	if err != nil {
		return nil, err
	}

	wm := whatsmeow.NewClient(dev, logger.WhatsApp(s.log))
	// A bot has no use for the phone's chat history, and syncing it fills the database with
	// contacts and message keys. The sync is still acknowledged.
	wm.ManualHistorySyncDownload = true

	return &Client{wm: wm, log: s.log}, nil
}

// Connected is true once the connection is up and authenticated.
func (c *Client) Connected() bool {
	return c.wm.IsConnected() && c.wm.IsLoggedIn()
}

func (c *Client) Paired() bool {
	return c.wm.Store.ID != nil
}

// OnQR sets who receives the pairing codes. It must be called before Connect, and the codes are
// pairing credentials: pass them to the person pairing, never to the logs.
func (c *Client) OnQR(h func(QREvent)) { c.onQR = h }

// Connect reconnects from the stored device, or starts QR pairing if there is none.
// Pairing continues in the background; ctx cancels it.
func (c *Client) Connect(ctx context.Context) error {
	if !c.Paired() {
		// must be requested before Connect
		items, err := c.wm.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("get qr channel: %w", err)
		}
		go c.forwardQR(items)
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

// OnLoggedOut calls h when the phone or WhatsApp unlinks this device. The local device is gone by then.
func (c *Client) OnLoggedOut(h func()) {
	c.wm.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.LoggedOut); ok {
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

// Logout unlinks the device from the account and deletes it locally. When WhatsApp cannot be
// reached the device is still deleted locally, and the error says it stays listed on the phone.
func (c *Client) Logout(ctx context.Context) error {
	err := c.wm.Logout(ctx)
	if err == nil {
		return nil
	}
	c.wm.Disconnect()
	return errors.Join(fmt.Errorf("unlink on whatsapp: %w", err), c.wm.Store.Delete(ctx))
}

func (c *Client) forwardQR(items <-chan whatsmeow.QRChannelItem) {
	for item := range items {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			c.log.Info("whatsapp pairing code ready", "expires_in", item.Timeout)
			c.emit(QREvent{Code: item.Code})
		case whatsmeow.QRChannelSuccess.Event:
			c.log.Info("whatsapp pairing", "event", item.Event)
		default:
			err := item.Error
			if err == nil {
				err = fmt.Errorf("pairing ended: %s", item.Event)
			}
			c.log.Error("whatsapp pairing failed", "event", item.Event, "error", err)
			c.emit(QREvent{Err: err})
		}
	}
}

func (c *Client) emit(e QREvent) {
	if c.onQR != nil {
		c.onQR(e)
	}
}
