package db

import (
	"context"
	"strings"
	"testing"
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
