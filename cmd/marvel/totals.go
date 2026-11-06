package main

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// renderTotalsLine is the one-line cluster rollup under the daemon header
// (docs/design/cluster-rollup-header.md). This is the stub the red tests run
// against: it prints nothing yet.
func renderTotalsLine(rows []api.Session, now time.Time, label string, width int) string {
	_, _, _, _ = rows, now, label, width
	return ""
}
