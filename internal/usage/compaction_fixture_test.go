package usage

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// compactionSeries is one real auto-compaction lifted out of the local
// Claude Code transcript corpus by scripts/mine_claude_compactions.py.
// Numbers only: no message content, no paths.
type compactionSeries struct {
	Source        string `json:"source"`
	Harness       string `json:"harness"`
	BoundaryIndex int    `json:"boundary_index"`
	PreTokens     int    `json:"pre_tokens"`
	PostTokens    int    `json:"post_tokens"`
	Dropped       int    `json:"dropped"`
	Occupancy     []int  `json:"occupancy_series"`
}

// TestCompactionDetectorOnRealSeries replays a labelled compaction from
// the operator's own history against the shipped hysteresis.
//
// The constants in accountant.go were reasoned rather than calibrated,
// against a single recovered geometry of roughly 167k down to 96k. This
// fixture is the calibration: 466,571 down to 116,155, one of the 63
// labelled events finding-024 replayed, on which the detector fired 63
// times with no false negative and never came within 4x of its guard.
//
// It also pins the two things a synthetic series cannot: the approach is
// the harness's own, in the increments the harness actually produces, and
// the drop is the real one rather than a round number chosen to clear the
// bound.
func TestCompactionDetectorOnRealSeries(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/compaction_series_claudecode.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx compactionSeries
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if fx.Harness != claudecode.Harness {
		t.Fatalf("fixture harness = %q, want %q", fx.Harness, claudecode.Harness)
	}
	if len(fx.Occupancy) <= fx.BoundaryIndex {
		t.Fatalf("fixture carries %d samples, boundary at %d",
			len(fx.Occupancy), fx.BoundaryIndex)
	}

	a, _, _ := newTestAccountant(t, Table{})
	for _, occ := range fx.Occupancy {
		// The corpus records a level, not its three classes; feeding it
		// through In alone is the same additive occupancy.
		a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
			Layout: rtevents.LayoutAdditive,
			In:     occ,
		}))
	}

	got, _ := a.SessionOccupancy(testCoords.AgentID)
	if got.Compactions != 1 {
		t.Errorf("compactions = %d, want 1 across a %d -> %d boundary",
			got.Compactions, fx.Occupancy[fx.BoundaryIndex-1], fx.Occupancy[fx.BoundaryIndex])
	}
	if want := fx.Occupancy[len(fx.Occupancy)-1]; got.Tokens != want {
		t.Errorf("level = %d, want %d (the last sample)", got.Tokens, want)
	}
	if got.Requests != len(fx.Occupancy) {
		t.Errorf("requests = %d, want %d", got.Requests, len(fx.Occupancy))
	}

	// The margin is the point of the fixture: the reasoned guard is not
	// close to missing a real compaction.
	pre := fx.Occupancy[fx.BoundaryIndex-1]
	drop := pre - fx.Occupancy[fx.BoundaryIndex]
	guard := a.hysteresis(pre)
	if drop < 4*guard {
		t.Errorf("drop %d is under 4x the guard %d; the corpus minimum was 4.03x, so this fixture or the constants moved",
			drop, guard)
	}
}
