package state

import (
	"embed"
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrateSchema applies only Factory's versioned schema. Hatchet manages its
// own schema independently in the same PostgreSQL installation.
func migrateSchema(databaseURL string) error {
	source, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("open Factory migrations: %w", err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		_ = source.Close()
		return fmt.Errorf("parse Factory database URL: %w", err)
	}
	query := parsed.Query()
	query.Set("x-migrations-table", "factory_schema_migrations")
	parsed.RawQuery = query.Encode()
	migrator, err := migrate.NewWithSourceInstance("iofs", source, parsed.String())
	if err != nil {
		_ = source.Close()
		return fmt.Errorf("connect Factory migrations: %w", err)
	}
	defer migrator.Close()
	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate Factory database: %w", err)
	}
	return nil
}
