// Package registry wires provider adapters to their keys.
// It lives in a separate package to avoid an import cycle between
// internal/provider (base types) and internal/provider/adapters (implementations).
package registry

import (
	"github.com/mmayadag/fx-rates/internal/provider"
	"github.com/mmayadag/fx-rates/internal/provider/adapters"
)

// Entry pairs a provider key with its Adapter implementation.
type Entry struct {
	Key     string
	Adapter provider.Adapter
}

// All returns every registered provider adapter.
func All() []Entry {
	return all
}

var all = []Entry{
	{"ECB", &adapters.ECB{}},
}
