package main

import (
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// lastActiveClock is the clock the LAST-ACTIVE cell reads, a seam for tests.
var lastActiveClock = time.Now

// sourceMark trails a value that a statusline or a heartbeat reported, not
// the token stream (docs/design/get-sessions-output.md, section 4.4). The
// mark is part of the value; sourceLegend explains it once on a terminal.
const (
	sourceMark   = "*"
	sourceLegend = sourceMark + " reported by statusline or heartbeat, not token flow"
)

// sourceMarkColumns are the columns whose cells can carry the mark. ACTIVE% too.
var sourceMarkColumns = map[string]bool{"last-active": true, "active": true}

// lastActiveAsOf is the cell's reading: the time since ContextAt, observed at
// ContextAt, from the producer that wrote it. ValidUntil stays zero on
// purpose: the value is an age, so it grows and never expires, and an expiry
// would hide the one number the cell shows.
func lastActiveAsOf(s api.Session, now time.Time) asof.Cell[time.Duration] {
	return asof.Cell[time.Duration]{
		Value:      now.Sub(s.ContextAt),
		ObservedAt: s.ContextAt,
		Source:     string(s.ContextSource),
	}
}

// lastActiveCell renders the LAST-ACTIVE cell: the age since the seat last
// reported, marked when a statusline or heartbeat reported it, and a dash for
// a seat with no reading. It claims activity, not busyness: a seat in a long
// tool call reads quiet.
func lastActiveCell(s api.Session, now time.Time) string {
	c := lastActiveAsOf(s, now)
	out := c.Render(now, ageWords)
	if c.State(now) == asof.Fresh && c.Source == string(api.ContextSourceHeartbeat) {
		out += sourceMark
	}
	return out
}

// sourceMarkOnScreen says whether any cell of the shown columns carries the
// mark, which is what earns the legend line.
func sourceMarkOnScreen(sessions []api.Session, shown []sessionColumn) bool {
	for _, c := range shown {
		if !sourceMarkColumns[c.name] {
			continue
		}
		for _, s := range sessions {
			if strings.HasSuffix(c.pick(newSessionRow(s)), sourceMark) {
				return true
			}
		}
	}
	return false
}
