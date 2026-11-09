package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// setTeamGeneration puts the team's counter ahead of its seat, as a shift of
// another role does (marvel#924): the seat keeps the generation it started at.
func (f *listFixture) setTeamGeneration(gen int64) {
	f.t.Helper()
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Generation = gen
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *listFixture) only(kind events.Kind) events.Event {
	f.t.Helper()
	evs := f.ring.Snapshot(events.Filter{Kind: kind}, 0)
	if len(evs) != 1 {
		f.t.Fatalf("%s events = %d, want exactly 1", kind, len(evs))
	}
	return evs[0]
}

// escalate drives a standing max-age request past its window to the
// escalation.
func (f *listFixture) escalate() {
	f.t.Helper()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	f.evaluate()
}

// docs/design/per-seat-replace.md D9 part A, test 19: the shift events name the
// condition and the seat's own generation, not the team's counter.
func TestShiftRequestAndMissingEventsCarryCauseAndSeatGeneration(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-seatgen-events", maxAgeRole(dir))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0) // the seat is at generation 1
	f.setTeamGeneration(4)

	f.evaluate()
	req := f.only(events.KindShiftHandoffRequested)
	if req.Generation != 1 || req.Cause != api.ShiftTriggerMaxAge || req.Reason != "" {
		t.Fatalf("handoff-requested = gen %d cause %q reason %q, want gen 1 cause max-age and no reason", req.Generation, req.Cause, req.Reason)
	}

	f.escalate()
	miss := f.only(events.KindShiftHandoffMissing)
	if miss.Generation != 1 || miss.Cause != api.ShiftTriggerMaxAge || miss.Reason != "window-expired" {
		t.Fatalf("handoff-missing = gen %d cause %q reason %q, want gen 1 cause max-age reason window-expired", miss.Generation, miss.Cause, miss.Reason)
	}
}

func TestAutoTriggeredEventCarriesCauseMarkerAndSeatGeneration(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-seatgen-auto", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.setTeamGeneration(4)

	f.evaluate()
	f.ctrl.handoffProbes.wait()
	writeHandoff(t, dir, s.Name, "notes\nEND HANDOFF\n")
	f.clock.Advance(time.Minute)
	f.evaluate() // starts the read
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	f.evaluate()

	auto := f.only(events.KindShiftAutoTriggered)
	if auto.Generation != 1 || auto.Cause != api.ShiftTriggerMaxAge || auto.Reason != "marker" {
		t.Fatalf("auto-triggered = gen %d cause %q reason %q, want gen 1 cause max-age reason marker", auto.Generation, auto.Cause, auto.Reason)
	}
}

// Test 20: the escalation time is stored with the request.
func TestEscalatedAtIsSetAndSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-escalated-at", maxAgeRole(dir))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	if got := f.evaluate().ShiftRequests; len(got) != 0 && !got[testShiftRole].EscalatedAt.IsZero() {
		t.Fatalf("EscalatedAt = %v before any escalation, want zero", got[testShiftRole].EscalatedAt)
	}
	f.escalate()
	req := f.ctrlRequest()
	if !req.Escalated {
		t.Fatal("the request should be escalated")
	}
	want := f.clock.Now()
	if !req.EscalatedAt.Equal(want) {
		t.Fatalf("EscalatedAt = %v, want the escalating tick's time %v", req.EscalatedAt, want)
	}

	// A restart: a fresh controller over the same store, a minute later. The
	// stored time is neither lost nor moved by the repeat.
	fresh := NewController(f.store, f.ctrl.sessMgr)
	fresh.now = f.clock.Now
	fresh.Events = events.NewRing(0)
	f.clock.Advance(time.Minute)
	team, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	fresh.evaluateShiftTriggers(&team)
	after := f.ctrlRequest()
	if !after.Escalated || !after.EscalatedAt.Equal(want) {
		t.Fatalf("after a restart: Escalated %v EscalatedAt %v, want true and %v", after.Escalated, after.EscalatedAt, want)
	}
}

func (f *listFixture) ctrlRequest() api.ShiftRequest {
	f.t.Helper()
	team, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		f.t.Fatal(err)
	}
	return team.ShiftRequests[testShiftRole]
}

// D9 part A: a request that stops holding the role's slot without a shift says
// so, with the reason.
func TestHoldReleasedWhenTheAskedSeatIsGone(t *testing.T) {
	f := newListFixture(t, "test-hold-seatgone", maxAgeRole(""))
	old := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate()
	if n := f.count(events.KindShiftHoldReleased); n != 0 {
		t.Fatalf("shift-hold-released = %d while the request stands, want 0", n)
	}

	if err := f.store.DeleteSession(old.Key()); err != nil {
		t.Fatal(err)
	}
	f.seed(0, 0, 0, 0) // same name, recreated now: a health restart
	f.clock.Advance(time.Minute)
	f.evaluate()
	ev := f.only(events.KindShiftHoldReleased)
	if ev.Reason != "seat-gone" || ev.Cause != api.ShiftTriggerMaxAge || ev.Session != old.Key() || ev.Role != testShiftRole {
		t.Fatalf("shift-hold-released = %+v, want reason seat-gone, cause max-age, session %s", ev, old.Key())
	}
	f.clock.Advance(time.Minute)
	f.evaluate()
	if n := f.count(events.KindShiftHoldReleased); n != 1 {
		t.Fatalf("shift-hold-released = %d after the drop, want 1: a drop with no request emits nothing", n)
	}
}

func TestHoldReleasedWhenThePolicyNoLongerDeclaresTheCondition(t *testing.T) {
	f := newListFixture(t, "test-hold-policygone", maxAgeRole(""))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate()
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Roles[0].Shift = &api.ShiftPolicy{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.evaluate()
	ev := f.only(events.KindShiftHoldReleased)
	if ev.Reason != "policy-removed" || ev.Cause != api.ShiftTriggerMaxAge || ev.Session != s.Key() {
		t.Fatalf("shift-hold-released = %+v, want reason policy-removed, cause max-age, session %s", ev, s.Key())
	}
}

// The stuck event names the seat's own generation, like the events beside it.
func TestStuckEventCarriesTheSeatsGeneration(t *testing.T) {
	f := stuckFixture(t, "test-stuck-seatgen", false,
		api.ShiftCondition{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge, MaxDefer: stuckDefer})
	f.setTeamGeneration(4) // the seat stays at generation 1
	f.clock.Advance(stuckDefer)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	ev := f.only(events.KindShiftRequestStuck)
	if ev.Generation != 1 {
		t.Fatalf("shift-request-stuck generation = %d, want 1: the seat is at generation 1 on a team at 4", ev.Generation)
	}
}

// A hibernate asks each seat for a handoff with the same event a shift does,
// and it names the seat's generation too, not the team's counter.
func TestHibernateHandoffRequestedCarriesTheSeatsGeneration(t *testing.T) {
	f := newRetireFixture(t, "test-hib-seatgen", hibernateRole("worker", realTempDir(t), 2))
	a, b := f.seat("worker", 0), f.seat("worker", 1)
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Generation = 4
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()

	got := map[string]int64{}
	for _, ev := range f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffRequested}, 0) {
		got[ev.Session] = ev.Generation
	}
	if len(got) != 2 {
		t.Fatalf("handoff-requested events by session = %v, want one for each of %s and %s", got, a.Key(), b.Key())
	}
	for _, s := range []api.Session{a, b} {
		if got[s.Key()] != 1 {
			t.Errorf("handoff-requested for %s: generation %d, want 1, the seat's own on a team at 4", s.Key(), got[s.Key()])
		}
	}
}
