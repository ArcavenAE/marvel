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

// costToutValue is the dollar total a statusline seat's TOUT cell shows, and
// whether this seat shows dollars at all.
func costToutValue(s api.Session) (float64, bool) {
	if s.SpendOut != nil || s.CostTotalUSD == nil {
		return 0, false
	}
	return *s.CostTotalUSD, true
}

// costTout is the TOUT cell for a statusline seat: the latest total, and
// whether this seat shows dollars at all.
func costTout(s api.Session) (string, bool) {
	v, ok := costToutValue(s)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("~$%.2f", v), true
}

// costRateValue is the reading behind a statusline seat's RATE cell: dollars a
// minute and the state of the cell. asof.None is the cell's "-", asof.Stale its
// "?", and asof.Fresh a value. applies is false for a seat that shows no dollar
// rate at all. The cell reads it, so the two cannot disagree.
func costRateValue(s api.Session, now time.Time) (perMinute float64, state asof.State, applies bool) {
	if s.SpendOut != nil || s.CostTotalUSD == nil || s.OutRate.State(now) != asof.None {
		return 0, asof.None, false
	}
	window := s.ActiveTicks.Window
	if window <= 0 {
		window = api.DefaultQuietWindow
	}
	if len(s.CostSamples) == 0 {
		return 0, asof.None, true
	}
	seen := s.CostSeenAt
	if seen.IsZero() {
		seen = s.LastHeartbeat
	}
	if now.Sub(seen) > window {
		return 0, asof.Stale, true
	}
	start := now.Add(-window)
	if s.CostGapAt.After(start) {
		return 0, asof.Stale, true
	}
	if s.CostSamples[0].At.After(start) {
		return 0, asof.None, true
	}
	anchor := s.CostSamples[0].USD
	for _, c := range s.CostSamples {
		if c.At.After(start) {
			break
		}
		anchor = c.USD
	}
	perMinute = (*s.CostTotalUSD - anchor) / window.Minutes()
	if perMinute < 0 {
		perMinute = 0
	}
	return perMinute, asof.Fresh, true
}

// costRate is the RATE cell for a statusline seat: the change in the total per
// minute over the quiet window the seat was judged under, from the sample at
// the window's start. It reads "-" until a window of samples exists, which
// covers the first window after a reset (a lower total starts the ring over),
// "?" once the feed has been silent past the window or while a silence is
// inside it (the spend of a gap is not a rate), and never goes below 0.
func costRate(s api.Session, now time.Time) (string, bool) {
	v, state, ok := costRateValue(s, now)
	if !ok {
		return "", false
	}
	switch state {
	case asof.None:
		return asof.DashNone, true
	case asof.Stale:
		return asof.MarkStale, true
	}
	return fmt.Sprintf("~$%.2f/m", v), true
}
