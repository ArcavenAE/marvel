package main

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// feedSeat is a seat with a statusline feed whose tick ring says 80% (360 of
// 450), so a test can tell which source the cell read.
func feedSeat(total int64, beat time.Duration, source api.ContextSourceKind, samples ...api.APISample) api.Session {
	s := ticked(360, 450, source, 2*time.Second)
	s.APIDurationMS = &total
	s.APISamples = samples
	s.APISeenAt = lastActiveNow.Add(-beat)
	return s
}

func apiAtNow(ago time.Duration, ms int64) api.APISample {
	return api.APISample{At: lastActiveNow.Add(-ago), MS: ms}
}

// For a seat with a statusline feed ACTIVE% is provisional: the API-time
// increase over 15 minutes, never the tick share. Seats with no feed keep the
// tick reading.
func TestActivePctFromAPITimeForAFeedSeat(t *testing.T) {
	gap := feedSeat(1_090_000, 15*time.Second, api.ContextSourceAccountant, apiAtNow(16*time.Minute, 1_000_000))
	gap.APIGapAt = lastActiveNow.Add(-5 * time.Minute)
	for _, tc := range []struct {
		name string
		s    api.Session
		want string
	}{
		{"rising", feedSeat(1_090_000, 15*time.Second, api.ContextSourceAccountant, apiAtNow(16*time.Minute, 1_000_000)), "10%"},
		{"busy seat in a long build", feedSeat(1_000_000, 15*time.Second, api.ContextSourceAccountant, apiAtNow(16*time.Minute, 1_000_000)), "0%"},
		{"reset", feedSeat(6000, 15*time.Second, api.ContextSourceAccountant, apiAtNow(2*time.Minute, 5000)), asof.DashNone},
		{"silent", feedSeat(1_090_000, 20*time.Minute, api.ContextSourceAccountant, apiAtNow(40*time.Minute, 1_000_000)), asof.MarkStale},
		{"gap", gap, asof.MarkStale},
		{"statusline mark", feedSeat(1_090_000, 15*time.Second, api.ContextSourceHeartbeat, apiAtNow(16*time.Minute, 1_000_000)), "10%" + sourceMark},
		{"no feed keeps ticks", ticked(360, 450, api.ContextSourceAccountant, 2*time.Second), "80%"},
	} {
		if got := activePctCell(tc.s, lastActiveNow); got != tc.want {
			t.Errorf("%s: cell = %q, want %q", tc.name, got, tc.want)
		}
	}
}
