package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// A claude seat on a version inside the shipped range, here one that was never
// the sampled version, reads logged-out at its login picker.
func TestWatchdogReadsLoggedOutAcrossTheShippedRange(t *testing.T) {
	r, screen := shippedRig(t)
	for i, v := range []string{"2.1.285", "2.1.288", "2.1.293"} {
		name := "s" + string(rune('a'+i))
		r.seat(name, "claude", v, 11*time.Minute, 0).screen = screen
	}
	r.w.Once()
	for i, v := range []string{"2.1.285", "2.1.288", "2.1.293"} {
		name := "s" + string(rune('a'+i))
		hs := r.get(name).HarnessState
		if hs == nil || hs.State != api.HarnessStateLoggedOut {
			t.Errorf("claude %s: harness state = %+v, want logged-out", v, hs)
		}
	}
}

// Outside the range the seat is uncovered and the covered list names the range,
// so an operator reading describe sees what the set does cover.
func TestWatchdogOutsideTheShippedRangeIsUncoveredAndNamesTheRange(t *testing.T) {
	r, screen := shippedRig(t)
	for i, v := range []string{"2.1.284", "2.1.294"} {
		name := "o" + string(rune('a'+i))
		p := r.seat(name, "claude", v, 11*time.Minute, 0)
		p.screen = screen
	}
	r.w.Once()
	for i, v := range []string{"2.1.284", "2.1.294"} {
		name := "o" + string(rune('a'+i))
		hs := r.get(name).HarnessState
		if hs == nil || hs.State != api.HarnessStateUncovered {
			t.Fatalf("claude %s: harness state = %+v, want uncovered", v, hs)
		}
		if strings.Join(hs.Covered, ",") != "2.1.285-2.1.293" {
			t.Errorf("claude %s: covered = %v, want the range 2.1.285-2.1.293", v, hs.Covered)
		}
	}
}
