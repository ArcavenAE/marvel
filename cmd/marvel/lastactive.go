package main

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// lastActiveClock is the clock the LAST-ACTIVE cell reads, a seam for tests.
var lastActiveClock = time.Now

// lastActiveAsOf is the cell's reading. Stub: the real one is in the next
// commit.
func lastActiveAsOf(s api.Session, now time.Time) asof.Cell[time.Duration] {
	return asof.Cell[time.Duration]{}
}

// lastActiveCell is the LAST-ACTIVE cell. Stub.
func lastActiveCell(s api.Session, now time.Time) string {
	return "?"
}

var (
	_ = lastActiveAsOf
	_ = lastActiveCell
)
