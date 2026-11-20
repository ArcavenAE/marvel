package main

import (
	"path/filepath"

	"github.com/arcavenae/marvel/internal/paths"
)

// accountSeriesDir is where the daemon keeps its day files of quota readings,
// under the home's state directory. An empty home gives no directory, and the
// series stays in memory.
func accountSeriesDir(layout paths.Layout) string {
	if layout.Home == "" {
		return ""
	}
	return filepath.Join(layout.StateDir(), "account-series")
}
