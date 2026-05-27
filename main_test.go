package main

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveExitCode(t *testing.T) {
	tests := []struct {
		name      string
		signalled bool
		runErr    error
		want      int
	}{
		{"clean run", false, nil, 0},
		{"run error", false, errors.New("boom"), 1},
		{"signal received", true, nil, 130},
		{"signal takes precedence over error", true, errors.New("boom"), 130},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveExitCode(tt.signalled, tt.runErr); got != tt.want {
				t.Fatalf("resolveExitCode(%v, %v) = %d, want %d", tt.signalled, tt.runErr, got, tt.want)
			}
		})
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		gone string // substring that must NOT appear in the output
	}{
		{
			name: "postgres dsn in error",
			in:   `failed to connect: postgres://alice:s3cr3t@db.example.com:5432/fx_rates?sslmode=require`,
			want: "postgres://***:***@db.example.com:5432/fx_rates",
			gone: "s3cr3t",
		},
		{
			name: "pgx5 scheme used by golang-migrate",
			in:   `migrate init: pgx5://bob:hunter2@localhost:5432/db`,
			want: "pgx5://***:***@localhost:5432/db",
			gone: "hunter2",
		},
		{
			name: "postgresql scheme",
			in:   `dial error: postgresql://u:p@host/db`,
			want: "postgresql://***:***@host/db",
			gone: ":p@",
		},
		{
			name: "no dsn left untouched",
			in:   "some unrelated error message",
			want: "some unrelated error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactDSN(tt.in)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("redactDSN(%q) = %q, want substring %q", tt.in, got, tt.want)
			}
			if tt.gone != "" && strings.Contains(got, tt.gone) {
				t.Fatalf("redactDSN(%q) = %q, leaked %q", tt.in, got, tt.gone)
			}
		})
	}
}
