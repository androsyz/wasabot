package dbtest

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/androsyz/wasabot/internal/db"
)

func New(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	if err := db.Migrate(ctx, sqlDB, slog.Default()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return sqlDB
}
