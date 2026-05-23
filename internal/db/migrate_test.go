package db

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestToPgxURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"postgres scheme", "postgres://u:p@h/d", "pgx5://u:p@h/d"},
		{"postgresql scheme", "postgresql://u:p@h/d", "pgx5://u:p@h/d"},
		{"already pgx5 left untouched", "pgx5://u:p@h/d", "pgx5://u:p@h/d"},
		{"unknown scheme passes through", "mysql://u:p@h/d", "mysql://u:p@h/d"},
		{"empty string passes through", "", ""},
		{"shorter than prefix", "post", "post"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toPgxURL(tt.in); got != tt.want {
				t.Fatalf("toPgxURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMigrationsFSEmbedsExpectedFiles(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("ReadDir(migrations): %v", err)
	}
	var upFiles, downFiles int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch {
		case strings.HasSuffix(e.Name(), ".up.sql"):
			upFiles++
		case strings.HasSuffix(e.Name(), ".down.sql"):
			downFiles++
		}
	}
	if upFiles < 3 {
		t.Errorf("expected at least 3 .up.sql migrations, got %d", upFiles)
	}
	if downFiles != upFiles {
		t.Errorf("expected matching up/down counts, got up=%d down=%d", upFiles, downFiles)
	}
}

func TestRunMigrationsRejectsInvalidURL(t *testing.T) {
	// pgx parser accepts most strings; force an error by passing a malformed
	// scheme that toPgxURL leaves alone and pgx5 will refuse.
	err := RunMigrations("not-a-valid-url://")
	if err == nil {
		t.Fatal("expected error for invalid database URL")
	}
}

func TestRunMigrationsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	if err := RunMigrations(dsn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Idempotent re-run should not error and should be a no-op.
	if err := RunMigrations(dsn); err != nil {
		t.Fatalf("RunMigrations (re-run): %v", err)
	}

	// Verify a known table exists.
	pool, err := NewPool(context.Background(), dsn, PoolOptions{MaxConns: 2})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	var exists bool
	err = pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'rates')`,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !exists {
		t.Fatal("expected 'rates' table to exist after migrations")
	}
}

func TestRollbackMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	// Ensure migrations are applied first.
	if err := RunMigrations(dsn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Roll back one step and re-apply so we leave the schema intact.
	if err := RollbackMigration(dsn); err != nil {
		t.Fatalf("RollbackMigration: %v", err)
	}
	if err := RunMigrations(dsn); err != nil {
		t.Fatalf("RunMigrations (after rollback): %v", err)
	}
}
