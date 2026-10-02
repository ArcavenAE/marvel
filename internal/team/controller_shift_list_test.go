package team

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Tests for the shift trigger list (marvel#437, docs/design/shift-trigger-list.md
// section 6, items 4 to 12). Each drives evaluateShiftTriggers against a fixed
// clock, so age and quiet are exact.

const testMaxAge = 8 * time.Hour

var listEpoch = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// maxAgeRole is a single-replica claude role with the given conditions and,
// when dir is non-empty, a declared handoff file under dir.
func maxAgeRole(dir string, conds ...api.ShiftCondition) api.Role {
	if len(conds) == 0 {
		conds = []api.ShiftCondition{{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge}}
	}
	p := &api.ShiftPolicy{Any: conds}
	if dir != "" {
		p.Handoff = filepath.Join(dir, "{session}.md")
		p.HandoffMarker = "END HANDOFF"
	}
	return api.Role{
		Name:     testShiftRole,
		Replicas: 1,
		Runtime:  api.Runtime{Name: "claude", Command: "claude"},
		Shift:    p,
	}
}

// listFixture builds a controller on a fixed clock with a recording notifier
// and an event ring, and one team holding role.
type listFixture struct {
	t       *testing.T
	store   *api.Store
	ctrl    *Controller
	clock   *testClock
	ring    *events.Ring
	notices []string
	teamKey string
}

func newListFixture(t *testing.T, ws string, role api.Role) *listFixture {
	t.Helper()
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	f := &listFixture{t: t, store: store, ctrl: ctrl, teamKey: ws + "/squad"}
	f.clock = newTestClock(listEpoch)
	ctrl.now = f.clock.Now
	f.ring = events.NewRing(0)
	ctrl.Events = f.ring
	ctrl.Notify = func(sess api.Session, text string) error {
		f.notices = append(f.notices, sess.Key()+": "+text)
		return nil
	}
	createTeamFixture(t, store, ws, "squad", []api.Role{role})
	return f
}

// seed creates the role's Running current-generation session, aged age, last
// active lastActive ago, with the given context reading.
func (f *listFixture) seed(age, lastActive time.Duration, tokens, limit int) api.Session {
	f.t.Helper()
	ws := strings.TrimSuffix(f.teamKey, "/squad")
	s := api.Session{
		Name:       "squad-" + testShiftRole + "-g1-0",
		Workspace:  ws,
		Team:       "squad",
		Role:       testShiftRole,
		Generation: 1,
		State:      api.SessionRunning,
		CreatedAt:  f.clock.Now().Add(-age),
	}
	seedSession(f.t, f.store, s)
	f.store.UpdateSessionContext(s.Key(), api.SessionContext{
		ContextTokens: tokens,
		ContextLimit:  limit,
		ContextAt:     f.clock.Now().Add(-lastActive),
	})
	return s
}

func (f *listFixture) evaluate() api.Team {
	f.t.Helper()
	f.ctrl.autoShiftsThisTick = 0
	team, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		f.t.Fatal(err)
	}
	f.ctrl.evaluateShiftTriggers(&team)
	got, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *listFixture) count(kind events.Kind) int {
	return len(f.ring.Snapshot(events.Filter{Kind: kind}, 0))
}

func writeHandoff(t *testing.T, dir, session, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, session+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Item 4: past max age and quiet, the seat is asked; the shift starts only
// once the declared handoff file ends in the marker.
func TestMaxAgeRequestsHandoffThenShiftsOnMarker(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-maxage-marker", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	got := f.evaluate()
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: a request is not a shift", got.Shift.Phase)
	}
	if len(f.notices) != 1 || !strings.Contains(f.notices[0], "write your handoff now") {
		t.Fatalf("notices = %q, want one handoff request", f.notices)
	}
	req, ok := got.ShiftRequests[testShiftRole]
	if !ok || req.Session != s.Key() || !req.RequestedAt.Equal(listEpoch) || req.Cause != api.ShiftTriggerMaxAge {
		t.Fatalf("ShiftRequests = %+v, want a max-age request for %s at %v", got.ShiftRequests, s.Key(), listEpoch)
	}
	if n := f.count(events.KindShiftHandoffRequested); n != 1 {
		t.Fatalf("handoff-requested events = %d, want 1", n)
	}

	// A second tick with no file neither re-asks nor shifts.
	f.clock.Advance(time.Minute)
	got = f.evaluate()
	if len(f.notices) != 1 || got.Shift.Phase != api.ShiftNone {
		t.Fatalf("after one quiet minute: notices=%d phase=%q, want 1 and none", len(f.notices), got.Shift.Phase)
	}

	// A file whose last line is not the marker is not a handoff.
	writeHandoff(t, dir, s.Name, "notes\nEND HANDOFF\nmore notes\n")
	f.clock.Advance(time.Minute)
	if got = f.evaluate(); got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: the marker is not the last line", got.Shift.Phase)
	}

	f.ctrl.handoffProbes.wait()
	writeHandoff(t, dir, s.Name, "notes\nEND HANDOFF\n")
	f.clock.Advance(time.Minute)
	// The read runs off the lock, so the marker is applied one tick after it
	// is written (marvel#444).
	if got = f.evaluate(); got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: the read has not been applied yet", got.Shift.Phase)
	}
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	got = f.evaluate()
	if got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching once the marker is the last line", got.Shift.Phase)
	}
	if _, pending := got.ShiftRequests[testShiftRole]; pending {
		t.Fatal("the request should move into the shift when it starts")
	}
	if req := got.Shift.HandoffRequests[testShiftRole]; req.Session != s.Key() || !req.RequestedAt.Equal(listEpoch) {
		t.Fatalf("Shift.HandoffRequests[%s] = %+v, want %s at %v", testShiftRole, req, s.Key(), listEpoch)
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftAutoTriggered}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "cause=max-age") {
		t.Fatalf("autotriggered = %+v, want one naming cause=max-age", evs)
	}
}

// Item 5: no marker by the end of the window escalates and does not shift;
// the seat keeps running and the escalation is reported once.
func TestMaxAgeEscalatesWithoutMarker(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-maxage-missing", maxAgeRole(dir))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	got := f.evaluate()
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: marvel never shifts unwatched", got.Shift.Phase)
	}
	if n := f.count(events.KindShiftHandoffMissing); n != 1 {
		t.Fatalf("handoff-missing events = %d, want 1", n)
	}
	if !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatal("request should be marked escalated")
	}

	// The supervisor owns it now, but the event ring is in memory and can lose
	// it, so the escalation is repeated once per window while the request
	// stands (marvel#453). Ticks inside a window stay quiet.
	f.clock.Advance(api.DefaultShiftHandoffWindow - time.Second)
	got = f.evaluate()
	if got.Shift.Phase != api.ShiftNone || len(f.notices) != 1 || f.count(events.KindShiftHandoffMissing) != 1 {
		t.Fatalf("inside the window: phase=%q notices=%d missing=%d, want none, 1, 1",
			got.Shift.Phase, len(f.notices), f.count(events.KindShiftHandoffMissing))
	}
	f.clock.Advance(2 * time.Second)
	got = f.evaluate()
	if got.Shift.Phase != api.ShiftNone || len(f.notices) != 1 || f.count(events.KindShiftHandoffMissing) != 2 {
		t.Fatalf("after a window: phase=%q notices=%d missing=%d, want none, 1, 2",
			got.Shift.Phase, len(f.notices), f.count(events.KindShiftHandoffMissing))
	}
	if !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatal("the request should stay escalated")
	}
}

// A daemon restart empties the event ring. The escalated request is durable,
// so the next tick must say so again rather than leave a silent hold
// (marvel#453).
func TestMaxAgeEscalationIsRepeatedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-maxage-restart", maxAgeRole(dir))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	if n := f.count(events.KindShiftHandoffMissing); n != 1 {
		t.Fatalf("handoff-missing events = %d, want 1", n)
	}

	// The restart: a fresh controller and an empty ring over the same store.
	fresh := NewController(f.store, f.ctrl.sessMgr)
	fresh.now = f.clock.Now
	ring := events.NewRing(0)
	fresh.Events = ring
	f.clock.Advance(time.Minute)
	team, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	fresh.evaluateShiftTriggers(&team)
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)); n != 1 {
		t.Fatalf("handoff-missing events after restart = %d, want 1: the hold must not be silent", n)
	}
}

// Item 5, second half: a role that declares no handoff path is still asked,
// and escalates when the window expires, because marvel cannot observe it.
func TestMaxAgeWithoutHandoffPathEscalates(t *testing.T) {
	f := newListFixture(t, "test-maxage-nopath", maxAgeRole(""))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.evaluate()
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1", len(f.notices))
	}
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	got := f.evaluate()
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none", got.Shift.Phase)
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "no handoff path") {
		t.Fatalf("handoff-missing = %+v, want one saying no handoff path is declared", evs)
	}
}

// Item 6: a busy seat past max age is not asked until max_age + max_defer.
func TestMaxAgeBusySeatDefersToHardThreshold(t *testing.T) {
	f := newListFixture(t, "test-maxage-busy", maxAgeRole(""))
	s := f.seed(testMaxAge+10*time.Minute, 30*time.Second, 0, 0)

	if f.evaluate(); len(f.notices) != 0 {
		t.Fatalf("notices = %d, want 0: the seat is busy and inside max_defer", len(f.notices))
	}
	// Still busy, now past max_age + max_defer (default 30m).
	f.clock.Advance(21 * time.Minute)
	f.store.UpdateSessionContext(s.Key(), api.SessionContext{ContextAt: f.clock.Now().Add(-30 * time.Second)})
	f.evaluate()
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1 at the hard threshold", len(f.notices))
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffRequested}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "hard") {
		t.Fatalf("handoff-requested = %+v, want one naming the hard threshold", evs)
	}
}

// Max age below its bound does nothing at all.
func TestMaxAgeHoldsBelowBound(t *testing.T) {
	f := newListFixture(t, "test-maxage-young", maxAgeRole(""))
	f.seed(testMaxAge-time.Minute, time.Hour, 0, 0)
	got := f.evaluate()
	if len(f.notices) != 0 || len(got.ShiftRequests) != 0 || got.Shift.Phase != api.ShiftNone {
		t.Fatalf("young seat: notices=%d requests=%v phase=%q, want nothing", len(f.notices), got.ShiftRequests, got.Shift.Phase)
	}
}

// Item 7: a session on an unresolved window is handled by max age, never by
// context pressure.
func TestMaxAgeCoversUnresolvedWindow(t *testing.T) {
	role := maxAgeRole("",
		api.ShiftCondition{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom},
		api.ShiftCondition{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge},
	)
	f := newListFixture(t, "test-maxage-nowin", role)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 900_000, 0)

	got := f.evaluate()
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: context pressure cannot meter an unresolved window", got.Shift.Phase)
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1 from max age", len(f.notices))
	}
}

// Item 8: with both conditions, the first in list order that holds fires, and
// the event names it.
func TestShiftListFirstHoldingConditionFires(t *testing.T) {
	cases := []struct {
		name      string
		conds     []api.ShiftCondition
		age       time.Duration
		tokens    int
		wantPhase api.ShiftPhase
		wantCause string
		wantAsks  int
	}{
		{
			name: "pressure holds, age does not",
			conds: []api.ShiftCondition{
				{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge},
				{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom},
			},
			age: time.Hour, tokens: 900_000,
			wantPhase: api.ShiftLaunching, wantCause: "cause=context-pressure",
		},
		{
			name: "both hold, pressure listed first",
			conds: []api.ShiftCondition{
				{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom},
				{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge},
			},
			age: testMaxAge + time.Hour, tokens: 900_000,
			wantPhase: api.ShiftLaunching, wantCause: "cause=context-pressure",
		},
		{
			name: "both hold, age listed first",
			conds: []api.ShiftCondition{
				{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge},
				{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom},
			},
			age: testMaxAge + time.Hour, tokens: 900_000,
			wantPhase: api.ShiftNone, wantAsks: 1,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newListFixture(t, "test-list-order-"+string(rune('a'+i)), maxAgeRole("", tc.conds...))
			f.seed(tc.age, 10*time.Minute, tc.tokens, 1_000_000)
			got := f.evaluate()
			if got.Shift.Phase != tc.wantPhase {
				t.Fatalf("phase = %q, want %q", got.Shift.Phase, tc.wantPhase)
			}
			if len(f.notices) != tc.wantAsks {
				t.Fatalf("notices = %d, want %d", len(f.notices), tc.wantAsks)
			}
			if tc.wantCause != "" {
				evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftAutoTriggered}, 0)
				if len(evs) != 1 || !strings.Contains(evs[0].Message, tc.wantCause) {
					t.Fatalf("autotriggered = %+v, want one naming %s", evs, tc.wantCause)
				}
			}
		})
	}
}

// Item 9: a pending request lives in the store, so a new controller (a daemon
// restart) resumes it and escalates on the original window.
func TestMaxAgePendingRequestSurvivesControllerRestart(t *testing.T) {
	f := newListFixture(t, "test-maxage-restart", maxAgeRole(""))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate()

	fresh := NewController(f.store, f.ctrl.sessMgr)
	fresh.now = f.clock.Now
	fresh.Events = f.ring
	fresh.Notify = f.ctrl.Notify
	f.ctrl = fresh

	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	got := f.evaluate()
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1: a restart must not re-ask", len(f.notices))
	}
	if f.count(events.KindShiftHandoffMissing) != 1 || got.Shift.Phase != api.ShiftNone {
		t.Fatalf("missing=%d phase=%q, want 1 and none", f.count(events.KindShiftHandoffMissing), got.Shift.Phase)
	}
}

// Item 10: a health restart is a new session, so its age starts over, and a
// request for the old session is dropped rather than applied to the new one.
func TestMaxAgeHealthRestartResetsAgeAndDropsRequest(t *testing.T) {
	f := newListFixture(t, "test-maxage-healthrestart", maxAgeRole(""))
	old := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate()

	if err := f.store.DeleteSession(old.Key()); err != nil {
		t.Fatal(err)
	}
	f.seed(0, 0, 0, 0) // same name, recreated now
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	got := f.evaluate()
	if _, pending := got.ShiftRequests[testShiftRole]; pending {
		t.Fatalf("ShiftRequests = %+v, want the stale request dropped", got.ShiftRequests)
	}
	if f.count(events.KindShiftHandoffMissing) != 0 || len(f.notices) != 1 {
		t.Fatalf("missing=%d notices=%d, want 0 and 1: the new session is young", f.count(events.KindShiftHandoffMissing), len(f.notices))
	}
}

// Item 11: a finished headless session is never evaluated.
func TestMaxAgeIgnoresFinishedHeadlessSession(t *testing.T) {
	role := maxAgeRole("")
	role.Runtime.Mode = api.RuntimeModeHeadless
	f := newListFixture(t, "test-maxage-headless", role)
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	if err := f.store.UpdateSession(s.Key(), func(live *api.Session) error {
		live.State = api.SessionSucceeded
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := f.evaluate()
	if len(f.notices) != 0 || len(got.ShiftRequests) != 0 {
		t.Fatalf("finished session: notices=%d requests=%v, want none", len(f.notices), got.ShiftRequests)
	}
}

// Item 12: a successor carries its predecessor's key and, when a request was
// made, when it was made; a first spawn carries neither.
func TestShiftStampsPredecessorOnSuccessor(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	createTeamFixture(t, store, "test-shift-lineage", "squad", []api.Role{sleepRole("crew", 1)})
	ctrl.ReconcileOnce()

	first := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-shift-lineage", "squad", "crew", 1))
	if len(first) != 1 {
		t.Fatalf("first generation = %d sessions, want 1", len(first))
	}
	if first[0].Predecessor != "" || !first[0].HandoffRequestedAt.IsZero() {
		t.Fatalf("first spawn has lineage (%q, %v), want none", first[0].Predecessor, first[0].HandoffRequestedAt)
	}

	asked := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	if err := store.UpdateTeam("test-shift-lineage/squad", func(live *api.Team) error {
		live.ShiftRequests = map[string]api.ShiftRequest{"crew": {Session: first[0].Key(), Cause: api.ShiftTriggerMaxAge, RequestedAt: asked}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.InitiateShift("test-shift-lineage/squad", "crew"); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()

	next := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-shift-lineage", "squad", "crew", 2))
	if len(next) != 1 {
		t.Fatalf("second generation = %d sessions, want 1", len(next))
	}
	if next[0].Predecessor != first[0].Key() {
		t.Fatalf("Predecessor = %q, want %q", next[0].Predecessor, first[0].Key())
	}
	if !next[0].HandoffRequestedAt.Equal(asked) {
		t.Fatalf("HandoffRequestedAt = %v, want %v", next[0].HandoffRequestedAt, asked)
	}
}

// Design D6: a pending or escalated max-age request does not silence the
// role's other conditions. Context pressure still shifts the seat, and the
// shift carries the request to the successor.
func TestContextPressureStillFiresWhileMaxAgeRequestPending(t *testing.T) {
	role := maxAgeRole("",
		api.ShiftCondition{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge},
		api.ShiftCondition{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom},
	)
	f := newListFixture(t, "test-maxage-then-pressure", role)
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 100_000, 1_000_000)
	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	if got := f.evaluate(); !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatalf("ShiftRequests = %+v, want an escalated request", got.ShiftRequests)
	}

	f.store.UpdateSessionContext(s.Key(), api.SessionContext{ContextTokens: 900_000, ContextLimit: 1_000_000, ContextAt: f.clock.Now()})
	got := f.evaluate()
	if got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching on context pressure", got.Shift.Phase)
	}
	if req := got.Shift.HandoffRequests[testShiftRole]; req.Session != s.Key() {
		t.Fatalf("Shift.HandoffRequests = %+v, want the escalated request carried", got.Shift.HandoffRequests)
	}
}

// A request outlives nothing it no longer applies to: when the role's policy
// stops declaring max age, the request is dropped.
func TestMaxAgeRequestDroppedWhenPolicyNoLongerDeclaresIt(t *testing.T) {
	f := newListFixture(t, "test-maxage-policy-gone", maxAgeRole(""))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate()
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Roles[0].Shift = &api.ShiftPolicy{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := f.evaluate()
	if len(got.ShiftRequests) != 0 {
		t.Fatalf("ShiftRequests = %+v, want the request dropped", got.ShiftRequests)
	}
}

// The handoff path is written by the seat, so marvel must not trust what is
// there. A FIFO would block a plain open forever while the controller holds
// c.mu, wedging every team; a directory or a symlink is not a handoff either.
// handoffComplete must answer false for each, promptly (review 5392253372).
func TestHandoffCompleteRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	sub := filepath.Join(dir, "dir.md")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(target, []byte("END HANDOFF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{"fifo": fifo, "directory": sub, "symlink": link} {
		done := make(chan bool, 1)
		go func() { done <- handoffComplete(path, "END HANDOFF") }()
		select {
		case got := <-done:
			if got {
				t.Errorf("%s: handoffComplete = true, want false", name)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: handoffComplete blocked; a seat can wedge the controller", name)
		}
	}
	if !handoffComplete(target, "END HANDOFF") {
		t.Fatal("positive control: a regular file ending in the marker must count")
	}
}

// A handoff path that cannot be resolved (no home directory for ~/) is not
// observed, never read relative to the daemon's working directory.
func TestHandoffPathRefusesUnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := handoffPath("~/h/{session}.md", api.Session{Name: "s"}); err == nil {
		t.Fatal("handoffPath with no home: want an error, not a relative path")
	}
	got, err := handoffPath("/var/h/{session}.md", api.Session{Name: "s"})
	if err != nil || got != "/var/h/s.md" {
		t.Fatalf("absolute template = (%q, %v), want /var/h/s.md", got, err)
	}
}

// The template is checked for .. at apply, but {session} is filled from the
// session name later. A name that is not one path element must not steer the
// resolved path (marvel#444 item 2).
func TestHandoffPathRefusesNameThatEscapesTheTemplate(t *testing.T) {
	for _, name := range []string{"a/b", "..", ".", "../x", "x/..", `a\b`, ""} {
		if p, err := handoffPath("/var/h/{session}.md", api.Session{Name: name}); err == nil {
			t.Errorf("handoffPath with session name %q = %q, want an error", name, p)
		}
	}
	// A name that merely contains dots is one path element and stays valid.
	if _, err := handoffPath("/var/h/{session}.md", api.Session{Name: "team-role-g1-0"}); err != nil {
		t.Errorf("handoffPath with an ordinary name: %v", err)
	}
	// A template with no {session} never reads the name.
	if _, err := handoffPath("/var/h/fixed.md", api.Session{Name: "a/b"}); err != nil {
		t.Errorf("handoffPath with no placeholder: %v", err)
	}
}

// The marker read must not run under c.mu, where a slow filesystem would stall
// every team (marvel#444 item 1). A tick starts the read and returns; the
// result is applied on the next tick.
func TestHandoffReadRunsOffTheLock(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-maxage-offlock", maxAgeRole(dir))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate() // asks the seat

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	f.ctrl.handoffProbes.check = func(string, string) bool {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return true
	}

	f.clock.Advance(time.Minute)
	done := make(chan struct{})
	go func() {
		f.evaluate()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("the tick blocked on the handoff read")
	}
	<-started
	got, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none: the read has not finished", got.Shift.Phase)
	}

	close(release)
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	if got = f.evaluate(); got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching: the finished read applies on the next tick", got.Shift.Phase)
	}
}

// A role's seats carry the generation of the last shift that covered the role,
// not the team's counter, so max age must look at the role's running seats at
// any generation (marvel#451, the class of #345 and #387).
func TestMaxAgeAsksSeatAtAnOlderGeneration(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-maxage-oldgen", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0) // generation 1
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Generation = 4
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got := f.evaluate()
	req, ok := got.ShiftRequests[testShiftRole]
	if !ok || req.Session != s.Key() {
		t.Fatalf("ShiftRequests = %+v, want a request for %s: the seat is at generation 1 on a team at 4", got.ShiftRequests, s.Key())
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1", len(f.notices))
	}
}
