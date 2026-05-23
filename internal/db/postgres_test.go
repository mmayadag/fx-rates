package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewPoolRejectsInvalidURL(t *testing.T) {
	_, err := NewPool(context.Background(), ":", PoolOptions{MaxConns: 10})
	if err == nil {
		t.Fatal("expected error for invalid database URL")
	}
	if !strings.Contains(err.Error(), "failed to parse") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewPoolPingsUnreachableHostAndFails(t *testing.T) {
	// 127.0.0.1:1 is virtually guaranteed to refuse connections; pgx surfaces
	// the failure via Ping rather than ParseConfig.
	dsn := "postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
	_, err := NewPool(context.Background(), dsn, PoolOptions{MaxConns: 1})
	if err == nil {
		t.Fatal("expected ping failure for unreachable host")
	}
}

func TestNewPoolAppliesOptionsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	opts := PoolOptions{
		MaxConns:          7,
		MinConns:          2,
		MaxConnLifetime:   15 * time.Minute,
		MaxConnIdleTime:   3 * time.Minute,
		HealthCheckPeriod: 20 * time.Second,
	}
	pool, err := NewPool(context.Background(), dsn, opts)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	cfg := pool.Config()
	if cfg.MaxConns != opts.MaxConns {
		t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, opts.MaxConns)
	}
	if cfg.MinConns != opts.MinConns {
		t.Errorf("MinConns = %d, want %d", cfg.MinConns, opts.MinConns)
	}
	if cfg.MaxConnLifetime != opts.MaxConnLifetime {
		t.Errorf("MaxConnLifetime = %v, want %v", cfg.MaxConnLifetime, opts.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != opts.MaxConnIdleTime {
		t.Errorf("MaxConnIdleTime = %v, want %v", cfg.MaxConnIdleTime, opts.MaxConnIdleTime)
	}
	if cfg.HealthCheckPeriod != opts.HealthCheckPeriod {
		t.Errorf("HealthCheckPeriod = %v, want %v", cfg.HealthCheckPeriod, opts.HealthCheckPeriod)
	}
}

func TestNewPoolZeroOptionsKeepPgxDefaultsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	pool, err := NewPool(context.Background(), dsn, PoolOptions{})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	cfg := pool.Config()
	// pgx defaults are non-zero; we just confirm they weren't clobbered by our
	// zero-value override path.
	if cfg.MaxConns <= 0 {
		t.Errorf("expected pgx default MaxConns > 0, got %d", cfg.MaxConns)
	}
	if cfg.MaxConnLifetime <= 0 {
		t.Errorf("expected pgx default MaxConnLifetime > 0, got %v", cfg.MaxConnLifetime)
	}
}
