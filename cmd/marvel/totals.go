package main

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// totalsOpts carries what a caller adds to the totals line: the label of a
// cluster's line, the count of rows the table is cut from, the as-of stamp of
// held rows, and the starting mark. T1 passes the zero value; the later
// tickets of docs/design/cluster-rollup-header.md fill the rest.
type totalsOpts struct {
	label    string
	of       int
	asOf     time.Time
	starting bool
}

// renderTotals is the totals line of docs/design/cluster-rollup-header.md:
// one sum per shown column, each counting exactly the cells the rows show as
// values. The red commit holds this stub; the tests fail on its empty answer.
func renderTotals(rows []api.Session, shown []sessionColumn, now time.Time, opts totalsOpts) string {
	return ""
}
