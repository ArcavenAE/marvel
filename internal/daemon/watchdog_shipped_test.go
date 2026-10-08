package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/panestate"
)

// shippedRig is a watchdog over the pattern sets the binary ships and the
// screen captured under probe P-WD1 (marvel#598), not the synthetic fixtures.
func shippedRig(t *testing.T) (*wdRig, string) {
	t.Helper()
	sets, err := panestate.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../panestate/patterns/claude/2.1.290/logged-out.sample.txt")
	if err != nil {
		t.Fatalf("the captured sample is missing: %v", err)
	}
	r := newWDRig(t)
	r.w.sets = sets
	return r, string(raw)
}

// A quiet claude seat reporting 2.1.290, shown the captured screen, reads
// logged-out at high confidence after one pass, and nothing else about the
// session changes.
func TestWatchdogReadsTheCapturedScreenAsLoggedOut(t *testing.T) {
	r, screen := shippedRig(t)
	p := r.seat("a", "claude", "2.1.290", 30*time.Minute, 0)
	p.screen = screen
	before := r.get("a")
	r.w.Once()
	after := r.get("a")
	hs := after.HarnessState
	if hs == nil || hs.State != api.HarnessStateLoggedOut || hs.Confidence != "high" {
		t.Fatalf("harness state = %+v, want logged-out at high confidence", hs)
	}
	if after.State != before.State || after.HealthState != before.HealthState {
		t.Fatalf("State or HealthState changed: %s/%s -> %s/%s", before.State, before.HealthState, after.State, after.HealthState)
	}
}

// The same screen from a session reporting a version outside the pattern's
// range does not read logged-out: a pattern is for the versions it covers.
func TestWatchdogDoesNotApplyTheCapturedScreenToAnotherVersion(t *testing.T) {
	r, screen := shippedRig(t)
	p := r.seat("a", "claude", "2.1.294", 30*time.Minute, 0)
	p.screen = screen
	r.w.Once()
	hs := r.get("a").HarnessState
	if hs != nil && hs.State == api.HarnessStateLoggedOut {
		t.Fatalf("2.1.294 read logged-out from a sample whose range ends at 2.1.293: %+v", hs)
	}
}
