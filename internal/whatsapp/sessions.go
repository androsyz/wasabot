package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"

	"github.com/androsyz/wasabot/internal/logger"
)

// SetDeviceName sets the name shown under WhatsApp > Linked devices. It is a whatsmeow global,
// so it is process-wide and only applies to devices paired after the call.
func SetDeviceName(name string) {
	store.SetOSInfo(name, [3]uint32{0, 1, 0})
}

type Sessions struct {
	container *sqlstore.Container
	log       *slog.Logger
}

// NewSessions keeps whatsmeow's own tables (whatsmeow_*) in the same database as ours.
func NewSessions(ctx context.Context, sqlDB *sql.DB, log *slog.Logger) (*Sessions, error) {
	log = log.With("component", "whatsmeow")

	container := sqlstore.NewWithDB(sqlDB, "sqlite3", logger.WhatsApp(log))
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("upgrade whatsmeow store: %w", err)
	}
	return &Sessions{container: container, log: log}, nil
}

// device loads the paired device for jid, or creates a new unpaired one when jid is empty.
func (s *Sessions) device(ctx context.Context, jid string) (*store.Device, error) {
	if jid == "" {
		return s.container.NewDevice(), nil
	}

	parsed, err := types.ParseJID(jid)
	if err != nil {
		return nil, fmt.Errorf("parse jid: %w", err)
	}
	dev, err := s.container.GetDevice(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("get whatsapp device: %w", err)
	}
	if dev == nil {
		return nil, fmt.Errorf("no whatsapp device stored for %s", logger.MaskJIDs(jid))
	}
	return dev, nil
}
