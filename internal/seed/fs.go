// Package seed exposes an embedded filesystem containing static seed data.
// The db package imports from here so the binary remains fully self-contained
// with no external files at runtime.
package seed

import "embed"

//go:embed data/providers/*.json
var FS embed.FS
