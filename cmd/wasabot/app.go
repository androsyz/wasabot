package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/bot"
	"github.com/androsyz/wasabot/internal/config"
	"github.com/androsyz/wasabot/internal/db"
	"github.com/androsyz/wasabot/internal/store"
	"github.com/androsyz/wasabot/internal/whatsapp"
)

const botWorkers = 4

type app struct {
	cfg      config.Config
	log      *slog.Logger
	qrOut    io.Writer
	db       *sql.DB
	store    *store.Store
	sessions *whatsapp.Sessions
	bot      *bot.Bot
	auth     *auth.Service
	runtime  *runtime

	agentLabel string // shown on the dashboard
}

func newApp(ctx context.Context, cfg config.Config, log *slog.Logger) (a *app, err error) {
	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			sqlDB.Close()
		}
	}()

	if err := db.Migrate(ctx, sqlDB, log); err != nil {
		return nil, err
	}

	whatsapp.SetDeviceName("wasabot")
	sessions, err := whatsapp.NewSessions(ctx, sqlDB, log)
	if err != nil {
		return nil, err
	}

	st := store.New(sqlDB)
	authService, err := auth.NewService(st, auth.Options{})
	if err != nil {
		return nil, err
	}
	responder, agentLabel, err := newResponder(cfg, log, st.Messages)
	if err != nil {
		return nil, err
	}
	return &app{
		cfg:        cfg,
		log:        log,
		qrOut:      os.Stdout,
		db:         sqlDB,
		store:      st,
		sessions:   sessions,
		auth:       authService,
		runtime:    newRuntime(),
		agentLabel: agentLabel,
		bot: bot.New(st.Messages, log, responder, bot.Options{
			Workers:   botWorkers,
			Fallback:  cfg.FallbackReply,
			RateLimit: cfg.RateLimit,
			MaxAge:    cfg.MaxMessageAge,
		}),
	}, nil
}

func (a *app) Close() {
	if err := a.db.Close(); err != nil {
		a.log.Error("close database", "error", err)
	}
}
