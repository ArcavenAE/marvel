package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// observeLevels feeds an accountant a series of occupancy levels and returns
// how many compactions it counted.
func observeLevels(t *testing.T, a *Accountant, levels ...int) int {
	t.Helper()
	for _, occ := range levels {
		a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{Layout: rtevents.LayoutAdditive, In: occ}))
	}
	got, ok := a.SessionOccupancy(testCoords.AgentID)
	if !ok {
		t.Fatal("no occupancy recorded")
	}
	return got.Compactions
}

// The accountant's own request path honors the shared floor: a drop of exactly
// 2048 tokens is inside the band, one more is a compaction. Pinned here so a
// local comparison or a changed default in the accountant cannot drift from
// the rule the heartbeat path shares (marvel#630).
func TestAccountantCompactionFloorIs2048Tokens(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		to   int
		want int
	}{
		{"exactly the floor", 7_952, 0},
		{"one past the floor", 7_951, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _, _ := newTestAccountant(t, Table{})
			if got := observeLevels(t, a, 10_000, tc.to); got != tc.want {
				t.Fatalf("10000 -> %d: compactions = %d, want %d", tc.to, got, tc.want)
			}
		})
	}
}

// WithCompactionHysteresis resizes the band the accountant applies, in both
// directions, and nothing else.
func TestWithCompactionHysteresisResizesTheBand(t *testing.T) {
	t.Parallel()
	t.Run("wider band ignores a drop the default counts", func(t *testing.T) {
		t.Parallel()
		a, _, _ := newTestAccountant(t, Table{})
		if got := observeLevels(t, a, 10_000, 7_000); got != 1 {
			t.Fatalf("control: default band counted %d, want 1", got)
		}
		wide := New(newRecordSink(), NewResolver(Table{}), WithClock(fixedClock()), WithCompactionHysteresis(5_000, 0))
		if got := observeLevels(t, wide, 10_000, 7_000); got != 0 {
			t.Fatalf("a 5000-token band counted %d for a 3000 drop, want 0", got)
		}
	})
	t.Run("narrower band counts a drop the default ignores", func(t *testing.T) {
		t.Parallel()
		a, _, _ := newTestAccountant(t, Table{})
		if got := observeLevels(t, a, 10_000, 9_400); got != 0 {
			t.Fatalf("control: default band counted %d, want 0", got)
		}
		narrow := New(newRecordSink(), NewResolver(Table{}), WithClock(fixedClock()), WithCompactionHysteresis(500, 0.01))
		if got := observeLevels(t, narrow, 10_000, 9_400); got != 1 {
			t.Fatalf("a 500-token band counted %d for a 600 drop, want 1", got)
		}
	})
}
