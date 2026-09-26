package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Migrate(ctx context.Context, sqlDB *sql.DB, log *slog.Logger) error {
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, fsys, goose.WithSlog(log))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}

	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}

	return nil
}
