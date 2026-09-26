package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/web"
)

func (a *app) serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)

	var ui *web.Server
	defer func() {
		cancel()
		a.manager.Close()
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
	if err := a.manager.Load(ctx); err != nil {
		return err
	}

	a.bot.Start(ctx)
	ui, err := a.startWeb(ctx)
	if err != nil {
		return err
	}
	a.manager.StartAll()

	a.log.Info("wasabot started",
		"version", version,
		"addr", a.cfg.Addr,
		"db_path", a.cfg.DBPath,
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
		Runtime:      a.manager,
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
