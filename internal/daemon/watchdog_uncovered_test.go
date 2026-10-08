package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/panestate"
)

// uncoveredEvents returns the watchdog.uncovered events in ring order.
func (r *wdRig) uncoveredEvents() []events.Event {
	return r.kinds(events.KindWatchdogUncovered)
}

func (r *wdRig) endSession(name string) {
	r.t.Helper()
	if err := r.store.UpdateSession("ws/"+name, func(s *api.Session) error {
		s.State = api.SessionCrashed
		return nil
	}); err != nil {
		r.t.Fatal(err)
	}
}

// loadVersion adds a passing pattern for a claude version, as a set loading.
func (r *wdRig) loadVersion(version string) {
	r.t.Helper()
	loaded := r.w.sets[0]
	loaded.HarnessVersion = version
	r.w.sets = append(r.w.sets, loaded)
}

// A point release leaves the fleet unwatched as one fact: three seats on the
// same uncovered version produce one event naming the version and the count
// then, and a fourth seat arriving while the set is non-empty produces none.
func TestWatchdogUncoveredIsOneEventPerVersionNotPerSeat(t *testing.T) {
	r := coverageRig(t)
	for _, n := range []string{"a", "b", "c"} {
		r.seat(n, "claude", "2.1.291", 11*time.Minute, 0)
	}
	r.w.Once()

	got := r.uncoveredEvents()
	if len(got) != 1 {
		t.Fatalf("watchdog.uncovered events = %d, want 1: %+v", len(got), got)
	}
	ev := got[0]
	if ev.Severity != events.SeverityInfo {
		t.Errorf("severity = %s, want info", ev.Severity)
	}
	if !strings.Contains(ev.Message, "claude 2.1.291") || !strings.Contains(ev.Message, "3 sessions") ||
		!strings.Contains(ev.Message, "2.1.283") || strings.Contains(ev.Message, "cleared") {
		t.Errorf("message = %q, want the version, the count 3 and the covered versions", ev.Message)
	}

	r.seat("d", "claude", "2.1.291", 11*time.Minute, 0)
	r.now = r.now.Add(11 * time.Minute)
	r.w.Once()
	if hs := r.get("d").HarnessState; hs == nil || hs.State != api.HarnessStateUncovered {
		t.Fatalf("precondition: the fourth seat read %+v", hs)
	}
	if n := len(r.uncoveredEvents()); n != 1 {
		t.Errorf("watchdog.uncovered events = %d after a fourth seat joined, want still 1", n)
	}
}

// Two uncovered versions are two facts.
func TestWatchdogUncoveredIsPerVersion(t *testing.T) {
	r := coverageRig(t)
	r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	r.seat("b", "claude", "2.1.292", 11*time.Minute, 0)
	r.w.Once()

	got := r.uncoveredEvents()
	if len(got) != 2 {
		t.Fatalf("watchdog.uncovered events = %d, want one per version: %+v", len(got), got)
	}
	var versions []string
	for _, ev := range got {
		for _, v := range []string{"2.1.291", "2.1.292"} {
			if strings.Contains(ev.Message, v) {
				versions = append(versions, v)
			}
		}
	}
	if strings.Join(versions, ",") != "2.1.291,2.1.292" && strings.Join(versions, ",") != "2.1.292,2.1.291" {
		t.Errorf("events name %v, want each version once", versions)
	}
}

// A set for the version loads: the cleared form fires once, and a later pass
// repeats nothing.
func TestWatchdogUncoveredClearsWhenASetLoads(t *testing.T) {
	r := coverageRig(t)
	r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	r.seat("b", "claude", "2.1.291", 11*time.Minute, 0)
	r.w.Once()
	if n := len(r.uncoveredEvents()); n != 1 {
		t.Fatalf("precondition: %d events", n)
	}

	r.loadVersion("2.1.291")
	r.w.Once()

	got := r.uncoveredEvents()
	if len(got) != 2 {
		t.Fatalf("watchdog.uncovered events = %d after the set loaded, want the first plus one cleared", len(got))
	}
	if ev := got[1]; ev.Severity != events.SeverityInfo || !strings.Contains(ev.Message, "cleared") || !strings.Contains(ev.Message, "claude 2.1.291") {
		t.Errorf("second event = %+v, want a cleared form naming claude 2.1.291", ev)
	}
	r.now = r.now.Add(11 * time.Minute)
	r.w.Once()
	if n := len(r.uncoveredEvents()); n != 2 {
		t.Errorf("watchdog.uncovered events = %d, want the cleared form once", n)
	}
}

// The cleared form fires when the last session on the version ends, and not
// before: two of three ending leaves the set non-empty.
func TestWatchdogUncoveredClearsWhenTheLastSessionEnds(t *testing.T) {
	r := coverageRig(t)
	for _, n := range []string{"a", "b", "c"} {
		r.seat(n, "claude", "2.1.291", 11*time.Minute, 0)
	}
	r.w.Once()

	r.endSession("a")
	r.endSession("b")
	r.w.Once()
	if n := len(r.uncoveredEvents()); n != 1 {
		t.Fatalf("watchdog.uncovered events = %d with one seat left, want 1", n)
	}

	r.endSession("c")
	r.w.Once()
	got := r.uncoveredEvents()
	if len(got) != 2 || !strings.Contains(got[1].Message, "cleared") {
		t.Fatalf("events = %+v, want a cleared form after the last session ended", got)
	}
}

// After a clear, the set going from empty to non-empty again is a new
// transition and says so.
func TestWatchdogUncoveredFiresAgainAfterAClear(t *testing.T) {
	r := coverageRig(t)
	r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	r.w.Once()
	r.endSession("a")
	r.w.Once()

	r.seat("b", "claude", "2.1.291", 11*time.Minute, 0)
	r.now = r.now.Add(11 * time.Minute)
	r.w.Once()

	got := r.uncoveredEvents()
	if len(got) != 3 {
		t.Fatalf("events = %d, want uncovered, cleared, uncovered", len(got))
	}
	if strings.Contains(got[2].Message, "cleared") || !strings.Contains(got[2].Message, "1 session") {
		t.Errorf("third event = %q, want a fresh uncovered naming 1 session", got[2].Message)
	}
}

// A seat that alternates work and quiet keeps its coverage state and adds no
// event past the first (the alternating seat of the coverage-state ticket).
func TestWatchdogUncoveredAlternatingSeatFiresNoRepeat(t *testing.T) {
	r := coverageRig(t)
	r.seat("a", "claude", "2.1.291", 11*time.Minute, 0)
	r.w.Once()
	for range 3 {
		r.now = r.now.Add(time.Minute)
		r.setContextAt("ws/a", r.now)
		r.w.Once()
		r.now = r.now.Add(11 * time.Minute)
		r.w.Once()
	}
	if n := len(r.uncoveredEvents()); n != 1 {
		t.Errorf("watchdog.uncovered events = %d for an alternating seat, want 1", n)
	}
}

// A version whose every pattern failed its control reads control-failed, which
// has its own warning event at start; this event is for the uncovered state.
func TestWatchdogUncoveredIsNotEmittedForControlFailed(t *testing.T) {
	r := newWDRig(t)
	r.w.sample = func(panestate.Pattern) (string, error) { return "some other screen\n", nil }
	r.w.control()
	r.seat("a", "claude", "2.1.283", 11*time.Minute, 0)
	r.w.Once()

	if hs := r.get("a").HarnessState; hs == nil || hs.State != api.HarnessStateControlFailed {
		t.Fatalf("precondition: %+v", hs)
	}
	if n := len(r.uncoveredEvents()); n != 0 {
		t.Errorf("watchdog.uncovered events = %d for a control-failed seat, want 0", n)
	}
}
