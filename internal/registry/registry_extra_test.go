package registry_test

import (
	"testing"

	"github.com/mmayadag/fx-rates/internal/registry"
)

func TestGet_Found(t *testing.T) {
	a := registry.Get("ECB")
	if a == nil {
		t.Fatal("expected non-nil adapter for ECB")
	}
}

func TestGet_NotFound(t *testing.T) {
	a := registry.Get("NONEXISTENT")
	if a != nil {
		t.Fatal("expected nil for unknown key")
	}
}
