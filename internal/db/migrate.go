package db

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func RunMigrations(databaseURL string) error {
	return runMigrations(databaseURL, func(m *migrate.Migrate) error {
		return m.Up()
	})
}

func RollbackMigration(databaseURL string) error {
	return runMigrations(databaseURL, func(m *migrate.Migrate) error {
		return m.Steps(-1)
	})
}

func runMigrations(databaseURL string, fn func(*migrate.Migrate) error) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate fs: %w", err)
	}
	src, err := iofs.New(sub, ".")
	if err != nil {
		return fmt.Errorf("migrate source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toPgxURL(databaseURL))
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer m.Close()

	if err := fn(m); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// toPgxURL converts postgres:// / postgresql:// to pgx5:// for golang-migrate.
func toPgxURL(u string) string {
	for _, prefix := range []string{"postgresql://", "postgres://"} {
		if len(u) >= len(prefix) && u[:len(prefix)] == prefix {
			return "pgx5://" + u[len(prefix):]
		}
	}
	return u
}
