package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/panestate"
)

// coverageRig is the fixture rig after a passing control: one claude pattern
// for 2.1.283 is loaded and kept.
func coverageRig(t *testing.T) *wdRig {
	t.Helper()
	r := newWDRig(t)
	var calls int
	r.w.sample = fixtureSample(&calls)
	r.w.control()
	return r
}

// A seat on a version no pattern covers reads "uncovered", with the versions
// that are covered, and nothing about the seat's State or HealthState changes.
// Nothing is captured: there is nothing it could match.
func TestWatchdogMarksAnUncoveredVersion(t *testing.T) {
	r := coverageRig(t)
	p := r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	before := r.get("a")

	r.w.Once()

	after := r.get("a")
	hs := after.HarnessState
	if hs == nil || hs.State != api.HarnessStateUncovered {
		t.Fatalf("harness state = %+v, want uncovered", hs)
	}
	if hs.Confidence != "" || len(hs.Evidence) != 0 || hs.PatternID != "" {
		t.Errorf("an uncovered state carries a match: %+v", hs)
	}
	if hs.Harness != "claude" || hs.HarnessVersion != "2.1.291" || strings.Join(hs.Covered, ",") != "2.1.283" {
		t.Errorf("state = %+v, want claude 2.1.291 with covered 2.1.283", hs)
	}
	if after.State != before.State || after.HealthState != before.HealthState {
		t.Errorf("State or HealthState changed: %s/%s -> %s/%s", before.State, before.HealthState, after.State, after.HealthState)
	}
	if p.caps != 0 {
		t.Errorf("an uncovered seat was captured %d times", p.caps)
	}
}

// Every pattern for the seat's version failed its control: "control-failed",
// and the watchdog still looks, though it has no pattern left to match with.
func TestWatchdogMarksAVersionWhoseEveryPatternFailedItsControl(t *testing.T) {
	r := newWDRig(t)
	r.w.sample = func(panestate.Pattern) (string, error) { return "some other screen\n", nil }
	r.w.control()
	p := r.seat("a", "claude", "2.1.283", 11*time.Minute, 0)

	r.w.Once()

	if p.caps != 0 {
		t.Errorf("a control-failed seat was captured %d times: there is nothing it could match", p.caps)
	}
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateControlFailed {
		t.Fatalf("harness state = %+v, want control-failed", hs)
	}
	if len(hs.Covered) != 0 || hs.Confidence != "" || hs.HarnessVersion != "2.1.283" {
		t.Errorf("state = %+v, want no covered versions and no confidence", hs)
	}
}

// A harness the watchdog has never had a pattern for reads nothing: marvel does
// not claim to watch what it has no sample of.
func TestWatchdogReadsNothingForAHarnessWithNoPatterns(t *testing.T) {
	r := newWDRig(t)
	other := r.w.sets[0]
	other.Harness = "elsewhere"
	r.w.sets = []panestate.Pattern{other}
	var calls int
	r.w.sample = fixtureSample(&calls)
	r.w.control()
	p := r.seat("a", "claude", "2.1.283", 11*time.Minute, 0)

	r.w.Once()

	if hs := r.get("a").HarnessState; hs != nil {
		t.Fatalf("a harness with no patterns read %+v", hs)
	}
	if p.caps != 0 {
		t.Errorf("captured %d times", p.caps)
	}
}

// A coverage state is not a match result: a pass that finds nothing to match
// does not clear it, because nothing could match.
func TestWatchdogCoverageStateSurvivesLaterPasses(t *testing.T) {
	r := coverageRig(t)
	r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	r.w.Once()
	for range 3 {
		r.now = r.now.Add(11 * time.Minute)
		r.w.Once()
	}
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateUncovered {
		t.Fatalf("harness state after three passes = %+v, want uncovered still", hs)
	}
}

// The did-work clear leaves coverage states alone. A seat that alternates work
// and quiet would otherwise clear and re-set its state every cycle, which is the
// churn the uncovered event must not repeat (design section 5). The clear stays
// for logged-out, whose fact is the screen the seat was looking at.
func TestWatchdogDidWorkClearsLoggedOutButNotACoverageState(t *testing.T) {
	r := coverageRig(t)
	r.seat("cov", "claude", "2.1.291", 11*time.Minute, 0)
	r.seat("out", "claude", "2.1.283", 11*time.Minute, 0)
	r.w.Once()
	if r.get("cov").HarnessState == nil || r.get("out").HarnessState == nil {
		t.Fatalf("precondition: states = %+v / %+v", r.get("cov").HarnessState, r.get("out").HarnessState)
	}

	r.now = r.now.Add(time.Minute)
	r.setContextAt("ws/cov", r.now)
	r.setContextAt("ws/out", r.now)
	r.w.Once()

	if hs := r.get("cov").HarnessState; hs == nil || hs.State != api.HarnessStateUncovered {
		t.Errorf("the did-work clear removed the coverage state: %+v", hs)
	}
	if hs := r.get("out").HarnessState; hs != nil {
		t.Errorf("the did-work clear left logged-out standing: %+v", hs)
	}
}

// The alternating seat over several cycles holds one coverage state, and
// emits no session.harness-state events: coverage is not a logged-out verdict.
func TestWatchdogAlternatingSeatKeepsOneCoverageState(t *testing.T) {
	r := coverageRig(t)
	r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	r.w.Once()
	first := r.get("a").HarnessState.CapturedAt
	for range 3 {
		r.now = r.now.Add(time.Minute)
		r.setContextAt("ws/a", r.now)
		r.w.Once()
		r.now = r.now.Add(11 * time.Minute)
		r.w.Once()
	}
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateUncovered {
		t.Fatalf("harness state = %+v, want uncovered", hs)
	}
	if !hs.CapturedAt.Equal(first) {
		t.Errorf("the coverage state was set again (captured %s, first %s)", hs.CapturedAt, first)
	}
	if n := len(r.kinds(events.KindSessionHarnessState)) + len(r.kinds(events.KindSessionHarnessStateCleared)); n != 0 {
		t.Errorf("coverage emitted %d session.harness-state events", n)
	}
}

// A coverage state clears when its version becomes covered, at the seat's next
// quiet read, and the seat is then classified like any covered one.
func TestWatchdogCoverageStateClearsWhenTheVersionBecomesCovered(t *testing.T) {
	r := coverageRig(t)
	p := r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	r.w.Once()
	if hs := r.get("a").HarnessState; hs == nil || hs.State != api.HarnessStateUncovered {
		t.Fatalf("precondition: %+v", hs)
	}

	loaded := r.w.sets[0]
	loaded.HarnessVersion = "2.1.291"
	r.w.sets = append(r.w.sets, loaded)
	r.now = r.now.Add(11 * time.Minute)
	p.screen = "not a login screen\n"
	r.w.Once()
	if hs := r.get("a").HarnessState; hs != nil {
		t.Fatalf("a covered version with no match left %+v", hs)
	}

	p.screen = wdSample
	r.now = r.now.Add(11 * time.Minute)
	r.w.Once()
	if hs := r.get("a").HarnessState; hs == nil || hs.State != api.HarnessStateLoggedOut {
		t.Fatalf("a covered seat shown the login screen read %+v, want logged-out", hs)
	}
}

// A seat that is not examined (busy within the window, or the harness not in
// front) gets no coverage state: coverage shows once it next goes quiet.
func TestWatchdogCoverageIsEvaluatedAtTheQuietGateOnly(t *testing.T) {
	r := coverageRig(t)
	r.seat("busy", "claude", "2.1.291", 11*time.Minute, time.Minute)
	r.seat("pager", "claude", "less", 11*time.Minute, 0)
	r.w.Once()
	for _, n := range []string{"busy", "pager"} {
		if hs := r.get(n).HarnessState; hs != nil {
			t.Errorf("%s read %+v without being examined", n, hs)
		}
	}
}

// When every pattern of the harness failed its control, a seat on a version
// none of them was for reads uncovered, with no versions covered: the harness
// is one the watchdog has patterns for, so it does not read as unwatched.
func TestWatchdogHarnessWithOnlyFailedPatternsReadsOtherVersionsUncovered(t *testing.T) {
	r := newWDRig(t)
	r.w.sample = func(panestate.Pattern) (string, error) { return "some other screen\n", nil }
	r.w.control()
	p := r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)

	r.w.Once()

	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateUncovered || len(hs.Covered) != 0 {
		t.Fatalf("harness state = %+v, want uncovered with no covered versions", hs)
	}
	if p.caps != 0 {
		t.Errorf("captured %d times", p.caps)
	}
}

// A coverage state records the session's ContextAt when it was seen, as every
// harness state does, so a reader can tell how stale it is against the seat's
// last work.
func TestWatchdogCoverageStateRecordsTheSessionsContextAt(t *testing.T) {
	r := coverageRig(t)
	r.seat("a", "claude", "2.1.291", 30*time.Minute, 15*time.Minute)

	r.w.Once()

	hs := r.get("a").HarnessState
	if hs == nil || hs.ContextAt.IsZero() || !hs.ContextAt.Equal(r.get("a").ContextAt) {
		t.Fatalf("state = %+v, want ContextAt equal to the session's %s", hs, r.get("a").ContextAt)
	}
}
