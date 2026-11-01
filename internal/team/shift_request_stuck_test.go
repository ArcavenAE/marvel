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

const (
	stuckDefer = 45 * time.Minute
	// pastMaxDefer is a point past stuckDefer and inside a one hour window.
	pastMaxDefer = stuckDefer + 5*time.Minute
)

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

// A request is not stuck while its escalation has not had its chance. The
// escalation comes after the handoff window and after a probe read that began
// at or after the deadline, so on the first tick past a longer window the
// request is unescalated and nothing is wrong yet. These tests run the real
// tick order (case (a) of the review of this change, and its sequel).
func windowFixture(t *testing.T, ws string, withSupervisor bool, window time.Duration) *deliverFixture {
	t.Helper()
	f := newDeliverFixture(t, ws, withSupervisor, withSupervisor,
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
	return f
}

// tick advances the clock and runs one evaluation and its probe read.
func (f *deliverFixture) tick(d time.Duration) api.Team {
	f.t.Helper()
	f.clock.Advance(d)
	got := f.evaluate()
	f.ctrl.handoffProbes.wait()
	return got
}

func TestStuckRequestStaysQuietThroughALongerWindowWhenTheBusDelivers(t *testing.T) {
	const window = time.Hour
	f := windowFixture(t, "test-stuck-window-delivers", true, window)
	f.tick(pastMaxDefer)                        // past max_defer, inside the window
	f.tick(window - pastMaxDefer + time.Minute) // the first tick past the window
	f.tick(time.Second)                         // the read that grounds the escalation
	got := f.tick(time.Second)                  // the escalation and its delivery
	for i := 0; i < 3; i++ {
		got = f.tick(time.Minute)
	}
	if got.ShiftRequests[testShiftRole].DeliveredCount == 0 {
		t.Fatalf("setup: nothing was delivered to the running supervisor: %+v", got.ShiftRequests)
	}
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Errorf("stuck events for a request whose escalation was delivered = %d, want 0: %v",
			n, f.ring.Snapshot(events.Filter{Kind: events.KindShiftRequestStuck}, 0))
	}
}

func TestStuckRequestWithNoRecipientFiresOnceEscalatedPastALongerWindow(t *testing.T) {
	const window = time.Hour
	f := windowFixture(t, "test-stuck-window-nobody", false, window)
	f.tick(pastMaxDefer)
	f.tick(window - pastMaxDefer + time.Minute)
	f.tick(time.Second)
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Fatalf("stuck events before the request escalated = %d, want 0", n)
	}
	got := f.tick(time.Second) // the escalation: nobody to deliver to
	if !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatalf("setup: the request did not escalate: %+v", got.ShiftRequests)
	}
	f.tick(time.Second) // the tick after it, which sees the escalation and no delivery
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftRequestStuck}, 0)
	if len(evs) != 1 {
		t.Fatalf("stuck events after the escalation with no recipient = %d, want 1", len(evs))
	}
	if !strings.Contains(evs[0].Message, "escalated with 0 escalations delivered") {
		t.Errorf("message %q does not say the request escalated with nothing delivered", evs[0].Message)
	}
	f.tick(time.Hour)
	if n := f.count(events.KindShiftRequestStuck); n != 1 {
		t.Errorf("stuck events later = %d, want still 1", n)
	}
}

// A request that never escalates, as when its probe read never finishes, is
// still reported, once max_defer has passed after the escalation was due.
func TestStuckRequestThatNeverEscalatesIsReportedAfterTheWindowPlusMaxDefer(t *testing.T) {
	const window = 10 * time.Minute
	f := windowFixture(t, "test-stuck-never-escalates", false, window)
	got, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	req := got.ShiftRequests[testShiftRole]
	role := got.Roles[0]
	for _, r := range got.Roles {
		if r.Name == testShiftRole {
			role = r
		}
	}
	sess, err := f.store.GetSession(req.Session)
	if err != nil {
		t.Fatal(err)
	}
	// Past max_defer and past the window, not yet escalated: quiet.
	f.ctrl.noteStuckRequest(&got, &role, req, sess, req.RequestedAt.Add(window+stuckDefer-time.Second))
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Fatalf("stuck events before window plus max_defer = %d, want 0", n)
	}
	f.ctrl.noteStuckRequest(&got, &role, req, sess, req.RequestedAt.Add(window+stuckDefer))
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftRequestStuck}, 0)
	if len(evs) != 1 {
		t.Fatalf("stuck events at window plus max_defer = %d, want 1", len(evs))
	}
	if !strings.Contains(evs[0].Message, "has not escalated yet") {
		t.Errorf("message %q does not say the request has not escalated yet", evs[0].Message)
	}
}
