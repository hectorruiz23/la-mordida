// Package migrations carries the numbered SQL files embedded in the binary.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
