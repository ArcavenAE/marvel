package team

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Tests for a manual shift of a role that declares a handoff (marvel#1447,
// docs/design/shift-trigger-list.md D5). The role declares the handoff pair and
// no automatic trigger. A manual shift asks the seat, waits for the marker, and
// never shifts unwatched; an explicit NoHandoff is the operator's decision to
// shift without one. Every wait runs on the fixed clock.

// manualHandoffRole is a single-replica claude role that declares the handoff
// pair under dir and no automatic condition.
func manualHandoffRole(dir string) api.Role {
	return api.Role{
		Name:     testShiftRole,
		Replicas: 1,
		Runtime:  api.Runtime{Name: "claude", Command: "claude"},
		Shift: &api.ShiftPolicy{
			Handoff:       filepath.Join(dir, "{session}.md"),
			HandoffMarker: "END HANDOFF",
		},
	}
}

// manualShift starts a manual role shift with opts and returns the team.
func (f *listFixture) manualShift(opts ShiftOpts) (api.Team, error) {
	f.t.Helper()
	err := f.ctrl.InitiateShiftOpts(f.teamKey, testShiftRole, opts)
	got, gerr := f.store.GetTeam(f.teamKey)
	if gerr != nil {
		f.t.Fatal(gerr)
	}
	return got, err
}

func TestManualShiftRequestsHandoffAndDoesNotShiftYet(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-manual-ask", manualHandoffRole(dir))
	s := f.seed(time.Hour, 10*time.Minute, 0, 0)

	got, err := f.manualShift(ShiftOpts{})
	if err != nil {
		t.Fatalf("manual shift: %v", err)
	}
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: the seat is asked before it is replaced", got.Shift.Phase)
	}
	if len(f.notices) != 1 || !strings.Contains(f.notices[0], "write your handoff now") {
		t.Fatalf("notices = %q, want one handoff request naming the file", f.notices)
	}
	req, ok := got.ShiftRequests[testShiftRole]
	if !ok || req.Session != s.Key() || req.Cause != api.HandoffCauseManual {
		t.Fatalf("ShiftRequests = %+v, want a %q request for %s", got.ShiftRequests, api.HandoffCauseManual, s.Key())
	}
	if n := f.count(events.KindShiftHandoffRequested); n != 1 {
		t.Fatalf("handoff-requested events = %d, want 1", n)
	}
}

func TestManualShiftProceedsWhenTheMarkerArrives(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-manual-marker", manualHandoffRole(dir))
	s := f.seed(time.Hour, 10*time.Minute, 0, 0)
	if _, err := f.manualShift(ShiftOpts{}); err != nil {
		t.Fatalf("manual shift: %v", err)
	}

	f.clock.Advance(time.Minute)
	if got := f.evaluate(); got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: no marker yet", got.Shift.Phase)
	}
	f.ctrl.handoffProbes.wait()
	writeHandoff(t, dir, s.Name, "notes\nEND HANDOFF\n")
	f.clock.Advance(time.Minute)
	f.evaluate() // the read runs off the lock and is applied a tick later
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	got := f.evaluate()
	if got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching once the marker is the last line", got.Shift.Phase)
	}
	if req := got.Shift.HandoffRequests[testShiftRole]; req.Session != s.Key() {
		t.Fatalf("Shift.HandoffRequests[%s] = %+v, want the request for %s moved into the shift", testShiftRole, req, s.Key())
	}
}

func TestManualShiftWithoutMarkerKeepsTheSeatRunning(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-manual-missing", manualHandoffRole(dir))
	f.seed(time.Hour, 10*time.Minute, 0, 0)
	if _, err := f.manualShift(ShiftOpts{}); err != nil {
		t.Fatalf("manual shift: %v", err)
	}

	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate() // the first tick past the deadline starts a read
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	got := f.evaluate()
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: marvel never shifts unwatched", got.Shift.Phase)
	}
	if n := f.count(events.KindShiftHandoffMissing); n != 1 {
		t.Fatalf("handoff-missing events = %d, want 1", n)
	}
	if !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatal("the request should be marked escalated")
	}
}

// The caller's way past a missing handoff: after the seat was asked and the
// window ended without a marker, running the shift again with NoHandoff
// proceeds, and nothing is asked a second time.
func TestManualShiftNoHandoffProceedsAfterAMissingHandoff(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-manual-skip", manualHandoffRole(dir))
	f.seed(time.Hour, 10*time.Minute, 0, 0)
	if _, err := f.manualShift(ShiftOpts{}); err != nil {
		t.Fatalf("manual shift: %v", err)
	}
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	if got := f.evaluate(); !got.ShiftRequests[testShiftRole].Escalated || got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, requests = %+v, want an escalated request and no shift", got.Shift.Phase, got.ShiftRequests)
	}

	got, err := f.manualShift(ShiftOpts{NoHandoff: true})
	if err != nil {
		t.Fatalf("manual shift with NoHandoff: %v", err)
	}
	if got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching: the operator chose to shift without a handoff", got.Shift.Phase)
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1: the skip asks nothing again", len(f.notices))
	}
}

func TestManualShiftOfAnUnscopedTeamNamesTheHandoffRole(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-manual-unscoped", manualHandoffRole(dir))
	f.seed(time.Hour, 10*time.Minute, 0, 0)

	err := f.ctrl.InitiateShiftOpts(f.teamKey, "", ShiftOpts{})
	if err == nil {
		t.Fatal("unscoped shift succeeded, want a refusal that names the role that declares a handoff")
	}
	for _, want := range []string{testShiftRole, "--no-handoff"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
	got, _ := f.store.GetTeam(f.teamKey)
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none after the refusal", got.Shift.Phase)
	}
}

// A role with no handoff keys shifts at once, as it does today. This guard
// passes before the change; it fails if the change makes every manual shift wait.
func TestManualShiftOfARoleWithoutHandoffKeysIsUnchanged(t *testing.T) {
	role := manualHandoffRole(t.TempDir())
	role.Shift = nil
	f := newListFixture(t, "test-manual-nokeys", role)
	f.seed(time.Hour, 10*time.Minute, 0, 0)

	got, err := f.manualShift(ShiftOpts{})
	if err != nil {
		t.Fatalf("manual shift: %v", err)
	}
	if got.Shift.Phase != api.ShiftLaunching || len(f.notices) != 0 {
		t.Fatalf("phase = %q, notices = %q, want launching and no notice", got.Shift.Phase, f.notices)
	}
}
