package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Tests for handoff-first context pressure (marvel#442, design D8; the
// operator's ruling of 2026-10-04: "no, it should NOT, no handoff is worse than
// autocompaction"). Pressure asks the seat for a handoff on the request path
// max age uses, and replaces the seat only on the marker.

// pressureRole is a single-replica role with one pressure condition and a
// declared handoff file under dir.
func pressureRole(dir string) api.Role {
	return maxAgeRole(dir, api.ShiftCondition{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom})
}

// Test 23: at headroom the seat is asked, under its own cause, notice and
// origin; the shift starts only once the file ends in the marker.
func TestContextPressureRequestsHandoffThenShiftsOnMarker(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-pressure-marker", pressureRole(dir))
	s := f.seed(time.Hour, 10*time.Minute, 900_000, 1_000_000)

	got := f.evaluate()
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: pressure asks, it does not shift", got.Shift.Phase)
	}
	if len(f.notices) != 1 || !strings.Contains(f.notices[0], "shift: context nearly full; write your handoff now to ") {
		t.Fatalf("notices = %q, want one context-nearly-full handoff request", f.notices)
	}
	if !strings.Contains(f.notices[0], `, ending with the line "END HANDOFF"; you have `) {
		t.Fatalf("notice = %q, want the marker and the window named", f.notices[0])
	}
	if len(f.origins) != 1 || f.origins[0] != NoticeContextPressure {
		t.Fatalf("origins = %q, want [%s]", f.origins, NoticeContextPressure)
	}
	req, ok := got.ShiftRequests[testShiftRole]
	if !ok || req.Session != s.Key() || req.Cause != api.ShiftTriggerContextPressure || !req.RequestedAt.Equal(listEpoch) {
		t.Fatalf("ShiftRequests = %+v, want a context-pressure request for %s at %v", got.ShiftRequests, s.Key(), listEpoch)
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffRequested}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "cause=context-pressure") || !strings.Contains(evs[0].Message, "900000/1000000") {
		t.Fatalf("handoff-requested = %+v, want one naming the cause and the occupancy", evs)
	}

	// No re-ask while it stands.
	f.clock.Advance(time.Minute)
	if got = f.evaluate(); len(f.notices) != 1 || got.Shift.Phase != api.ShiftNone {
		t.Fatalf("after a minute: notices=%d phase=%q, want 1 and none", len(f.notices), got.Shift.Phase)
	}

	f.ctrl.handoffProbes.wait()
	writeHandoff(t, dir, s.Name, "notes\nEND HANDOFF\n")
	f.clock.Advance(time.Minute)
	if got = f.evaluate(); got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: the read has not been applied yet", got.Shift.Phase)
	}
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	got = f.evaluate()
	if got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching once the marker is the last line", got.Shift.Phase)
	}
	if req := got.Shift.HandoffRequests[testShiftRole]; req.Session != s.Key() || req.Cause != api.ShiftTriggerContextPressure {
		t.Fatalf("Shift.HandoffRequests = %+v, want the pressure request carried into the shift", got.Shift.HandoffRequests)
	}
	auto := f.ring.Snapshot(events.Filter{Kind: events.KindShiftAutoTriggered}, 0)
	if len(auto) != 1 || !strings.Contains(auto[0].Message, "cause=context-pressure") {
		t.Fatalf("autotriggered = %+v, want one naming cause=context-pressure", auto)
	}
}

// Test 24: no marker by the end of the window reports shift-handoff-missing
// (cause context-pressure) once per window, and the seat stays running. Nothing
// replaces it: no handoff is worse than autocompaction.
func TestContextPressureWithoutMarkerEscalatesAndNeverShifts(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-pressure-missing", pressureRole(dir))
	s := f.seed(time.Hour, 10*time.Minute, 900_000, 1_000_000)

	assertRunning := func(when string, got api.Team) {
		t.Helper()
		if got.Shift.Phase != api.ShiftNone {
			t.Fatalf("%s: phase = %q, want none: marvel never shifts a seat on pressure unwatched", when, got.Shift.Phase)
		}
		live, err := f.store.GetSession(s.Key())
		if err != nil || live.State != api.SessionRunning {
			t.Fatalf("%s: session = %+v, %v; want it running", when, live.State, err)
		}
		if n := len(f.store.ListSessionsByTeamRoleGeneration("test-pressure-missing", "squad", testShiftRole, 2)); n != 0 {
			t.Fatalf("%s: %d successor sessions exist, want none", when, n)
		}
	}

	assertRunning("asked", f.evaluate())
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	// The first tick past the deadline starts a read; escalation needs a
	// finished read that began after the deadline.
	assertRunning("deadline", f.evaluate())
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	got := f.evaluate()
	assertRunning("escalated", got)
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "cause=context-pressure") {
		t.Fatalf("handoff-missing = %+v, want one naming cause=context-pressure", evs)
	}
	if !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatal("request should be marked escalated")
	}
	f.ctrl.handoffProbes.wait()

	// Inside the next window it stays quiet; after it, it repeats once.
	f.clock.Advance(api.DefaultShiftHandoffWindow - time.Second)
	assertRunning("inside window", f.evaluate())
	if n := f.count(events.KindShiftHandoffMissing); n != 1 {
		t.Fatalf("handoff-missing events = %d inside the window, want 1", n)
	}
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(2 * time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	assertRunning("repeat", f.evaluate())
	if n := f.count(events.KindShiftHandoffMissing); n != 2 {
		t.Fatalf("handoff-missing events = %d after a window, want 2", n)
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1: the seat is asked once", len(f.notices))
	}

	// Q5: a marker written after the escalation does not shift the seat; it is
	// held for the supervisor (marvel#453).
	writeHandoff(t, dir, s.Name, "late\nEND HANDOFF\n")
	f.clock.Advance(time.Minute)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	assertRunning("late marker", f.evaluate())
}

// Q3: a session with no resolved window is not asked. (A stale reading is
// covered by TestAutoShiftIgnoresAStaleReading: the store stamps readings with
// the real clock, which this fixture's fixed clock cannot age.)
func TestContextPressureSkipsUnresolvedWindowAndBelowHeadroom(t *testing.T) {
	for name, tc := range map[string]struct {
		tokens, limit int
		lastActive    time.Duration
	}{
		"no window":      {900_000, 0, 10 * time.Minute},
		"below headroom": {100_000, 1_000_000, 10 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			f := newListFixture(t, "test-pressure-skip-"+strings.ReplaceAll(name, " ", "-"), pressureRole(t.TempDir()))
			f.seed(time.Hour, tc.lastActive, tc.tokens, tc.limit)
			got := f.evaluate()
			if len(f.notices) != 0 || len(got.ShiftRequests) != 0 || got.Shift.Phase != api.ShiftNone {
				t.Fatalf("notices=%d requests=%v phase=%q, want nothing", len(f.notices), got.ShiftRequests, got.Shift.Phase)
			}
		})
	}
}

// A pressure request outlives nothing it no longer applies to: when the
// policy stops declaring context pressure, the request is dropped.
func TestPressureRequestDroppedWhenPolicyNoLongerDeclaresIt(t *testing.T) {
	f := newListFixture(t, "test-pressure-policy-gone", pressureRole(t.TempDir()))
	f.seed(time.Hour, 10*time.Minute, 900_000, 1_000_000)
	if got := f.evaluate(); len(got.ShiftRequests) != 1 {
		t.Fatalf("ShiftRequests = %+v, want one", got.ShiftRequests)
	}
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Roles[0].Shift = &api.ShiftPolicy{Any: []api.ShiftCondition{{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.evaluate(); len(got.ShiftRequests) != 0 {
		t.Fatalf("ShiftRequests = %+v, want the pressure request dropped", got.ShiftRequests)
	}
}
