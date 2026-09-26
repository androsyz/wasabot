package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/bot"
	"github.com/androsyz/wasabot/internal/store"
	"github.com/androsyz/wasabot/internal/web"
	"github.com/androsyz/wasabot/internal/whatsapp"
)

const (
	defaultClientName = "Default"
	pairTimeout       = 3 * time.Minute
)

func (a *app) serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)

	var (
		clients []*whatsapp.Client
		ui      *web.Server
	)
	defer func() {
		for _, c := range clients {
			c.Disconnect()
		}
		cancel()
		a.bot.Wait()
		if ui != nil {
			shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := ui.Shutdown(shutdownCtx); err != nil {
				a.log.Error("stop web ui", "error", err)
			}
		}
	}()

	if err := a.prepareAdmin(ctx); err != nil {
		return err
	}

	// the dashboard comes up first, so it is reachable while a number is being paired
	ui, err := a.startWeb(ctx)
	if err != nil {
		return err
	}

	linked, err := a.store.WhatsAppSessions.List(ctx)
	if err != nil {
		return err
	}
	for _, s := range linked {
		a.runtime.setLinked(s.ClientID, true)
	}
	if len(linked) == 0 {
		if err := a.pairFirstClient(ctx); err != nil {
			return err
		}
		if linked, err = a.store.WhatsAppSessions.List(ctx); err != nil {
			return err
		}
		for _, s := range linked {
			a.runtime.setLinked(s.ClientID, true)
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
			a.bot.Handle(ctx, c, clientID, bot.Message{ID: m.ID, Chat: m.Chat, Sender: m.Sender, Text: m.Text, Timestamp: m.SentAt})
		})
		a.answerWaitingMessages(ctx, c, clientID)
		if err := c.Connect(ctx); err != nil {
			log.Error("connect whatsapp", "error", err)
			continue
		}
		clients = append(clients, c)
		a.runtime.setRunning(clientID, c)
		defer a.runtime.setRunning(clientID, nil)
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

// prepareAdmin makes sure a fresh install can be logged into, in this order of preference: the admin
// from WASABOT_ADMIN_EMAIL and WASABOT_ADMIN_PASSWORD; a setup page guarded by WASABOT_SETUP_CODE;
// otherwise the admin/admin account, which must be replaced at its first login.
func (a *app) prepareAdmin(ctx context.Context) error {
	if err := a.bootstrapAdmin(ctx); err != nil {
		return err
	}
	if a.cfg.SetupCode == "" {
		if _, err := a.auth.EnsureDefaultAdmin(ctx); err != nil {
			return err
		}
	}

	active, err := a.auth.DefaultAdminActive(ctx)
	if err != nil {
		return err
	}
	if active {
		a.log.Warn("the default admin account is active: log in as admin with the password admin and choose your own credentials. " +
			"Anyone who can reach the login page can do this first, so do it now, or set WASABOT_SETUP_CODE, or WASABOT_ADMIN_EMAIL and WASABOT_ADMIN_PASSWORD")
	}
	return nil
}

// bootstrapAdmin creates the admin from WASABOT_ADMIN_EMAIL and WASABOT_ADMIN_PASSWORD when there
// is none yet, for hosts where nobody can open a browser or read the logs first.
func (a *app) bootstrapAdmin(ctx context.Context) error {
	if a.cfg.AdminEmail == "" {
		return nil
	}
	err := a.auth.BootstrapAdmin(ctx, a.cfg.AdminName, a.cfg.AdminEmail, a.cfg.AdminPassword)
	var invalid *auth.ValidationError
	switch {
	case errors.As(err, &invalid):
		return fmt.Errorf("WASABOT_ADMIN_NAME, WASABOT_ADMIN_EMAIL or WASABOT_ADMIN_PASSWORD is not usable: %s", invalid.Message)
	case errors.Is(err, auth.ErrSetupDone):
		return nil // an admin exists already: the settings only matter on the first start
	case err != nil:
		return err
	}
	a.log.Info("admin account created from the environment")
	return nil
}

// startWeb serves the dashboard. While no admin exists (only when WASABOT_SETUP_CODE is set, since
// otherwise the default account was made) the setup page asks for that code.
func (a *app) startWeb(ctx context.Context) (*web.Server, error) {
	needsSetup, err := a.auth.NeedsSetup(ctx)
	if err != nil {
		return nil, err
	}

	srv, err := web.New(web.Options{
		Addr:         a.cfg.Addr,
		Log:          a.log,
		Store:        a.store,
		Auth:         a.auth,
		Runtime:      a.runtime,
		Agent:        a.agentLabel,
		Preview:      a.cfg.UIPreview,
		SetupCode:    a.cfg.SetupCode,
		CookieSecure: a.cfg.CookieSecure,
	})
	if err != nil {
		return nil, err
	}
	if err := srv.Start(); err != nil {
		return nil, err
	}

	a.log.Info("web ui listening", "url", "http://"+srv.Addr())
	if needsSetup {
		a.log.Info("no admin account yet: open the setup page and enter the code from WASABOT_SETUP_CODE", "url", "http://"+srv.Addr()+"/setup")
	}
	return srv, nil
}

// answerWaitingMessages replies to messages that were still unanswered when the process last
// stopped. They are listed now, before connecting, and answered once WhatsApp reports the
// connection is ready.
func (a *app) answerWaitingMessages(ctx context.Context, c *whatsapp.Client, clientID int64) {
	pending, err := a.bot.Unanswered(ctx, clientID)
	if err != nil {
		a.log.Error("list unanswered messages", "client_id", clientID, "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	var once sync.Once
	c.OnConnected(func() {
		once.Do(func() { go a.bot.Answer(ctx, c, clientID, pending) })
	})
}

// pairFirstClient exists because there is no other way to create a client until the web UI
// can add one: it pairs the first client, creating "Default" if there is none.
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

	a.runtime.setPairing(clientID, true)
	defer a.runtime.setPairing(clientID, false)

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
		a.runtime.setLinked(clientID, true)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("pairing did not finish: %w", ctx.Err())
	}
}
