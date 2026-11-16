package main

import (
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// The dollar form of the TOUT and RATE cells, for a seat whose only measure is
// the harness's statusline: Claude Code's running cost total. It is the
// harness's own list-price estimate, not a bill, so every figure carries a ~.
// A seat with a token stream (a headless run, or a metered one) keeps the
// token cells: this form applies only where those have nothing to show, so one
// seat never flips between units.

// costCell is the COST cell: the harness's running dollar total, with a ~
// because it is a list-price estimate and not a bill. "-" when the seat's feed
// never carried one.
func costCell(s api.Session) string {
	if s.CostTotalUSD == nil {
		return asof.DashNone
	}
	return fmt.Sprintf("~$%.2f", *s.CostTotalUSD)
}

// costRate is the RATE cell for a statusline seat: the change in the total per
// minute over the quiet window the seat was judged under, from the sample at
// the window's start. It reads "-" until a window of samples exists, which
// covers the first window after a reset (a lower total starts the ring over),
// "?" once the feed has been silent past the window or while a silence is
// inside it (the spend of a gap is not a rate), and never goes below 0.
func costRate(s api.Session, now time.Time) (string, bool) {
	if s.CostTotalUSD == nil {
		return "", false
	}
	window := s.ActiveTicks.Window
	if window <= 0 {
		window = api.DefaultQuietWindow
	}
	if len(s.CostSamples) == 0 {
		return asof.DashNone, true
	}
	seen := s.CostSeenAt
	if seen.IsZero() {
		seen = s.LastHeartbeat
	}
	if now.Sub(seen) > window {
		return asof.MarkStale, true
	}
	start := now.Add(-window)
	if s.CostGapAt.After(start) {
		return asof.MarkStale, true
	}
	if s.CostSamples[0].At.After(start) {
		return asof.DashNone, true
	}
	anchor := s.CostSamples[0].USD
	for _, c := range s.CostSamples {
		if c.At.After(start) {
			break
		}
		anchor = c.USD
	}
	perMinute := (*s.CostTotalUSD - anchor) / window.Minutes()
	if perMinute < 0 {
		perMinute = 0
	}
	return fmt.Sprintf("~$%.2f/m", perMinute), true
}
