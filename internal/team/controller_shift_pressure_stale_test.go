package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A handoff that ends in the marker but was written before a context-pressure
// request describes an earlier moment, so it does not shift the seat
// (marvel#1387). The same check serves every cause; these tests pin it under
// the cause the original incident came from.

func plantStaleHandoff(t *testing.T, dir, session string) {
	t.Helper()
	writeHandoff(t, dir, session, "old notes\nEND HANDOFF\n")
	stale := listEpoch.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, session+".md"), stale, stale); err != nil {
		t.Fatal(err)
	}
}

func TestAStaleHandoffDoesNotShiftASeatUnderContextPressure(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-pressure-stale", pressureRole(dir))
	s := f.seed(time.Hour, 10*time.Minute, 900_000, 1_000_000)
	plantStaleHandoff(t, dir, s.Name)

	got := f.evaluate() // asks
	if req, ok := got.ShiftRequests[testShiftRole]; !ok || req.Cause != api.ShiftTriggerContextPressure {
		t.Fatalf("ShiftRequests = %+v, want a context-pressure request", got.ShiftRequests)
	}
	for range 3 {
		f.ctrl.handoffProbes.wait()
		f.clock.Advance(time.Minute)
		if got = f.evaluate(); got.Shift.Phase != api.ShiftNone {
			t.Fatalf("phase = %q, want none: the marker is older than the request", got.Shift.Phase)
		}
	}
	if n := f.count(events.KindShiftAutoTriggered); n != 0 {
		t.Fatalf("autotriggered events = %d, want none", n)
	}
	live, err := f.store.GetSession(s.Key())
	if err != nil || live.State != api.SessionRunning {
		t.Fatalf("session = %v, %v; want it still running", live.State, err)
	}
}

func TestAStaleHandoffEscalatesAtTheWindowUnderContextPressure(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-pressure-stale-esc", pressureRole(dir))
	s := f.seed(time.Hour, 10*time.Minute, 900_000, 1_000_000)
	plantStaleHandoff(t, dir, s.Name)

	f.evaluate() // asks
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	got := f.evaluate()

	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none", got.Shift.Phase)
	}
	if !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatal("the request should be escalated once the window passed with only a stale handoff")
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "cause=context-pressure") {
		t.Fatalf("handoff-missing = %+v, want one naming cause=context-pressure", evs)
	}
}

func TestAHandoffWrittenAfterTheRequestStillShiftsUnderContextPressure(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-pressure-stale-fresh", pressureRole(dir))
	s := f.seed(time.Hour, 10*time.Minute, 900_000, 1_000_000)
	plantStaleHandoff(t, dir, s.Name)

	f.evaluate() // asks
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	if got := f.evaluate(); got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none while only the stale file exists", got.Shift.Phase)
	}

	// The seat answers the request: a rewrite after it counts.
	f.ctrl.handoffProbes.wait()
	fresh := listEpoch.Add(time.Minute)
	writeHandoff(t, dir, s.Name, "current notes\nEND HANDOFF\n")
	if err := os.Chtimes(filepath.Join(dir, s.Name+".md"), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	if got := f.evaluate(); got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching once the handoff is rewritten after the request", got.Shift.Phase)
	}
}
