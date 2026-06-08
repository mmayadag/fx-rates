package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mmayadag/fx-rates/internal/db"
)

func TestRedactDSN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "postgres url with credentials",
			in:   "dial postgres://alice:s3cret@db:5432/fx failed",
			want: "dial postgres://***:***@db:5432/fx failed",
		},
		{
			name: "pgx5 url with credentials",
			in:   "open pgx5://bob:hunter2@localhost/fx",
			want: "open pgx5://***:***@localhost/fx",
		},
		{
			name: "no credentials left untouched",
			in:   "failed to connect to `user=bob database=fx`",
			want: "failed to connect to `user=bob database=fx`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactDSN(tc.in); got != tc.want {
				t.Errorf("redactDSN(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(redactDSN(tc.in), "s3cret") || strings.Contains(redactDSN(tc.in), "hunter2") {
				t.Errorf("redactDSN leaked a password: %q", redactDSN(tc.in))
			}
		})
	}
}

// TestMigrateUpDownIntegration applies migrations, rolls one back, and
// reapplies — mirroring the operator workflow the binary exists for.
func TestMigrateUpDownIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	if err := db.RunMigrations(dsn); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := db.RollbackMigration(dsn); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := db.RunMigrations(dsn); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
