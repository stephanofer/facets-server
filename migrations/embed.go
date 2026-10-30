// Package migrations exposes the immutable SQL migration sequence.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed sql/*.sql
var files embed.FS

func Files() (fs.FS, error) { return fs.Sub(files, "sql") }
