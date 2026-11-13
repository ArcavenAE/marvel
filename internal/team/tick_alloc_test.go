package team

import (
	"fmt"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Allocation guards for the per-tick activity reading (rows 13 of the
// 2026-11 review of the evaluation tick). The tick runs refreshTeamLoad every
// pass, and each running seat's reading is read from its tick ring and judged
// under its role's window. None of that needs a Session built per ring sample
// or a team cloned per seat.

// fullRing returns a ring that has watched the whole TickRingWindow.
func fullRing() (*TickRing, time.Time) {
	r := &TickRing{}
	now := feed(r, 2*time.Second, TickRingWindow+time.Minute)
	return r, now
}

// BenchmarkTickRingCounts reads a full ring the way every seat's reading does
// each tick. Counts builds no Session per sample (the compiler keeps it on the
// stack, so the allocation count is 0 either way); the benchmark is for the
// time, which carries a Session-sized zeroing per sample.
func BenchmarkTickRingCounts(b *testing.B) {
	r, now := fullRing()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Counts(time.Minute, now)
	}
}

// seedRunningSeats adds n running seats of the fixture's role and feeds each a
// ring, so the tick's reading has samples to count.
func seedRunningSeats(t *testing.T, f *listFixture, n int) {
	t.Helper()
	ws := f.teamKey[:len(f.teamKey)-len("/squad")]
	for i := 0; i < n; i++ {
		s := api.Session{
			Name: fmt.Sprintf("squad-%s-g1-%d", testShiftRole, i), Workspace: ws, Team: "squad",
			Role: testShiftRole, Generation: 1, State: api.SessionRunning,
			CreatedAt: f.clock.Now().Add(-time.Hour),
		}
		seedSession(t, f.store, s)
		for k := 0; k < 30; k++ {
			f.ctrl.recordTick(s.Key(), f.clock.Now().Add(-time.Duration(30-k)*2*time.Second), f.clock.Now().Add(-time.Minute))
		}
	}
}

// refreshTeamLoad reads the role for each seat from the teams it already
// listed, so no team is fetched and cloned per seat. What is left per seat is
// the session snapshot the store hands out and the reading itself.
func TestRefreshTeamLoadAllocationsPerSeat(t *testing.T) {
	const seats = 20
	f := newListFixture(t, "test-alloc-load", viewRole())
	seedRunningSeats(t, f, seats)
	now := f.clock.Now()
	f.ctrl.refreshTeamLoad(now) // warm
	allocs := testing.AllocsPerRun(20, func() { f.ctrl.refreshTeamLoad(now) })
	perSeat := allocs / seats
	t.Logf("refreshTeamLoad: %.0f allocations for %d seats, %.1f per seat", allocs, seats, perSeat)
	const maxPerSeat = 6
	if perSeat > maxPerSeat {
		t.Errorf("refreshTeamLoad allocated %.1f times per seat, want at most %d", perSeat, maxPerSeat)
	}
}
