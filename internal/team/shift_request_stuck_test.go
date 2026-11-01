package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// docs/design/host-health-guard.md section 4.4 item 2: an event fires when a
// max-age request passes its MaxDefer with nothing delivered, so a stuck
// request is seen in hours and not in weeks. It reports; it does not deliver,
// retry or shift (#608 and #955 are the delivery fixes).

const stuckDefer = 45 * time.Minute

// stuckFixture drives a max-age request to its escalation. With no supervisor
// the escalation has no recipient and DeliveredCount stays 0.
func stuckFixture(t *testing.T, ws string, withSupervisor bool, conds ...api.ShiftCondition) *deliverFixture {
	t.Helper()
	f := newDeliverFixture(t, ws, withSupervisor, withSupervisor, conds...)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.escalate()
	return f
}

func TestMaxDeferForTheRequestsCause(t *testing.T) {
	t.Parallel()
	role := api.Role{Shift: &api.ShiftPolicy{Any: []api.ShiftCondition{
		{On: api.ShiftTriggerContextPressure, HeadroomTokens: 1000},
		{On: api.ShiftTriggerMaxAge, MaxAge: time.Hour, MaxDefer: 10 * time.Minute},
	}}}
	if d, ok := maxDeferFor(&role, api.ShiftTriggerMaxAge); !ok || d != 10*time.Minute {
		t.Errorf("max-age: got %v, %v; want 10m, true", d, ok)
	}
	if _, ok := maxDeferFor(&role, api.ShiftTriggerContextPressure); ok {
		t.Error("a context-pressure request has no MaxDefer to pass")
	}
	def := api.Role{Shift: &api.ShiftPolicy{Any: []api.ShiftCondition{{On: api.ShiftTriggerMaxAge, MaxAge: time.Hour}}}}
	if d, ok := maxDeferFor(&def, api.ShiftTriggerMaxAge); !ok || d != api.DefaultShiftMaxDefer {
		t.Errorf("unset MaxDefer: got %v, %v; want the default %v", d, ok, api.DefaultShiftMaxDefer)
	}
}

func TestStuckRequestFiresOncePastMaxDeferWithNothingDelivered(t *testing.T) {
	f := stuckFixture(t, "test-stuck-fires", false,
		api.ShiftCondition{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge, MaxDefer: stuckDefer})
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Fatalf("stuck events right after the escalation = %d, want 0", n)
	}
	f.clock.Advance(stuckDefer)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftRequestStuck}, 0)
	if len(evs) != 1 {
		t.Fatalf("stuck events past MaxDefer = %d, want 1", len(evs))
	}
	ev := evs[0]
	if ev.Severity != events.SeverityWarning || ev.Team != "squad" || ev.Role != testShiftRole || ev.Session == "" {
		t.Errorf("event = %+v, want a warning on squad/%s naming the session", ev, testShiftRole)
	}
	for _, want := range []string{"max-age", "0 escalations delivered", "escalated with 0 escalations delivered", stuckDefer.String(), "#608"} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("message %q lacks %q", ev.Message, want)
		}
	}
	// The same request does not fire again on later ticks.
	for i := 0; i < 3; i++ {
		f.clock.Advance(time.Hour)
		f.evaluate()
		f.ctrl.handoffProbes.wait()
	}
	if n := f.count(events.KindShiftRequestStuck); n != 1 {
		t.Errorf("stuck events after more ticks = %d, want still 1", n)
	}
}

func TestStuckRequestStaysQuietBeforeMaxDefer(t *testing.T) {
	f := stuckFixture(t, "test-stuck-early", false,
		api.ShiftCondition{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge, MaxDefer: stuckDefer})
	f.clock.Advance(stuckDefer - 10*time.Minute)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Errorf("stuck events before MaxDefer = %d, want 0", n)
	}
}

func TestStuckRequestStaysQuietOnceAnEscalationIsDelivered(t *testing.T) {
	f := stuckFixture(t, "test-stuck-delivered", true,
		api.ShiftCondition{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge, MaxDefer: stuckDefer})
	got, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.ShiftRequests[testShiftRole].DeliveredCount == 0 {
		t.Fatal("setup: the escalation was not delivered to the running supervisor")
	}
	f.clock.Advance(stuckDefer + time.Hour)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Errorf("stuck events with a delivered escalation = %d, want 0", n)
	}
}

// setHandoffWindow gives the fixture's max-age role a handoff window.
func setHandoffWindow(t *testing.T, f *deliverFixture, window time.Duration) {
	t.Helper()
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		for i := range live.Roles {
			if live.Roles[i].Name == testShiftRole {
				live.Roles[i].Shift.HandoffWindow = window
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A request is not stuck while its seat is still inside its handoff window,
// whatever max_defer says: the escalation, which is what delivery counts, comes
// after the window. So the event fires at the later of max_defer and the
// window (case (a) of the review of this change).
func TestStuckRequestWaitsForALongerHandoffWindow(t *testing.T) {
	const window = time.Hour
	f := newDeliverFixture(t, "test-stuck-window", false, false,
		api.ShiftCondition{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge, MaxDefer: stuckDefer})
	setHandoffWindow(t, f, window)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate() // makes the request
	got, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.ShiftRequests[testShiftRole]; !ok {
		t.Fatal("setup: no request was made")
	}

	// Past max_defer, inside the window: quiet.
	f.clock.Advance(stuckDefer + 5*time.Minute)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Fatalf("stuck events inside the handoff window = %d, want 0", n)
	}

	// Past the window with nothing delivered: one event, before the
	// escalation has been recorded, and it says so.
	f.clock.Advance(window)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftRequestStuck}, 0)
	if len(evs) != 1 {
		t.Fatalf("stuck events past the handoff window = %d, want 1", len(evs))
	}
	if !strings.Contains(evs[0].Message, "has not escalated yet") {
		t.Errorf("message %q does not say the request has not escalated yet", evs[0].Message)
	}
	f.clock.Advance(time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	if n := f.count(events.KindShiftRequestStuck); n != 1 {
		t.Errorf("stuck events after the escalation = %d, want still 1", n)
	}
}
