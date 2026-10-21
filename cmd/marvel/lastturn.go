package main

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// lastTurnCell renders the LAST-TURN cell: the age since the seat's statusline
// cost or API-time total last moved, which the store stamps as
// TotalsChangedAt and leaves alone on a compaction beat (api.recordTotals). It
// reads that stamp and nothing else, so a heartbeat or a context reading never
// makes an old turn look recent.
//
// A seat whose feed never carried totals prints a dash. A record that cannot be
// read prints the same ? a stale cell prints: totals with no stamp, a stamp
// with no totals, or a stamp after now, which LAST-ACTIVE's age words would
// clamp to 0s, an age that reads as just now. The cell is display only; no
// health, restart or ACTIVE% path reads it.
func lastTurnCell(s api.Session, now time.Time) string {
	hasTotals := s.CostTotalUSD != nil || s.APIDurationMS != nil
	stamped := !s.TotalsChangedAt.IsZero()
	switch {
	case !hasTotals && !stamped:
		return asof.DashNone
	case !hasTotals || !stamped || s.TotalsChangedAt.After(now):
		return asof.MarkStale
	}
	return ageWords(now.Sub(s.TotalsChangedAt))
}
