package main

import (
	"context"
	"fmt"
	"time"

	"github.com/androsyz/wasabot/internal/bot"
	"github.com/androsyz/wasabot/internal/store"
	"github.com/androsyz/wasabot/internal/whatsapp"
)

const (
	defaultClientName = "Default"
	pairTimeout       = 3 * time.Minute
)

func (a *app) serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)

	var clients []*whatsapp.Client
	defer func() {
		for _, c := range clients {
			c.Disconnect()
		}
		cancel()
		a.bot.Wait()
	}()

	linked, err := a.store.WhatsAppSessions.List(ctx)
	if err != nil {
		return err
	}
	if len(linked) == 0 {
		if err := a.pairFirstClient(ctx); err != nil {
			return err
		}
		if linked, err = a.store.WhatsAppSessions.List(ctx); err != nil {
			return err
		}
	}

	a.bot.Start(ctx)
	for _, s := range linked {
		log := a.log.With("client_id", s.ClientID)

		c, err := a.sessions.NewClient(ctx, s.JID, a.qrOut)
		if err != nil {
			log.Error("create whatsapp client", "error", err)
			continue
		}
		clientID := s.ClientID
		c.OnMessage(func(m whatsapp.IncomingMessage) {
			a.bot.Handle(ctx, c, clientID, bot.Message{ID: m.ID, Chat: m.Chat, Sender: m.Sender, Text: m.Text})
		})
		if err := c.Connect(ctx); err != nil {
			log.Error("connect whatsapp", "error", err)
			continue
		}
		clients = append(clients, c)
	}

	a.log.Info("wasabot started",
		"version", version,
		"addr", a.cfg.Addr,
		"db_path", a.cfg.DBPath,
		"whatsapp_sessions", len(clients),
	)
	<-ctx.Done()

	a.log.Info("shutting down")
	return nil
}

// pairFirstClient exists because there is no other way to create a client until the web UI
// (Milestone 3): it pairs the first client, creating "Default" if there is none.
func (a *app) pairFirstClient(ctx context.Context) error {
	client, err := a.firstClient(ctx)
	if err != nil {
		return err
	}
	a.log.Info("no linked WhatsApp number; pairing one", "client_id", client.ID, "client", client.Name)
	return a.pair(ctx, client.ID)
}

func (a *app) firstClient(ctx context.Context) (store.Client, error) {
	clients, err := a.store.Clients.List(ctx)
	if err != nil {
		return store.Client{}, err
	}
	if len(clients) > 0 {
		return clients[0], nil
	}
	return a.store.Clients.Create(ctx, defaultClientName)
}

func (a *app) pair(ctx context.Context, clientID int64) error {
	ctx, cancel := context.WithTimeout(ctx, pairTimeout)
	defer cancel()

	c, err := a.sessions.NewClient(ctx, "", a.qrOut)
	if err != nil {
		return err
	}
	linked := make(chan error, 1)
	c.OnPaired(func(jid string) {
		_, err := a.store.WhatsAppSessions.Link(ctx, clientID, jid)
		linked <- err
	})
	if err := c.Connect(ctx); err != nil {
		return err
	}
	defer c.Disconnect()

	select {
	case err := <-linked:
		if err != nil {
			return fmt.Errorf("link whatsapp session: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("pairing did not finish: %w", ctx.Err())
	}
}
