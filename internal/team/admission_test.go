package team

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// sleepRole is the deterministic fixture runtime used throughout this file:
// admission arithmetic is store-based, so no model auth and no real agent are
// needed to exercise any of it.
func sleepRole(name string, replicas int) api.Role {
	return api.Role{
		Name:     name,
		Replicas: replicas,
		Runtime:  api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
}

// createBudgetTeamFixture is createTeamFixture with a declared budget.
func createBudgetTeamFixture(t *testing.T, store *api.Store, wsName, teamName string, budget api.Budget, roles []api.Role) {
	t.Helper()
	if err := store.CreateWorkspace(&api.Workspace{Name: wsName, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTeam(&api.Team{
		Name:       teamName,
		Workspace:  wsName,
		Roles:      roles,
		Budget:     budget,
		Generation: 1,
		// Represents a team meant to run (see createTeamFixture); the hold
		// posture is set explicitly by the tests that exercise it.
		ConvergencePosture: api.PostureConverge,
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// setReplicas rewrites a role's replica count out of band, bypassing both
// doors that hold the declaration clause: the manifest parser
// (api.validateManifestBudget) and the scale verb
// (daemon.admitDeclaration). With those two closed, this is the only
// remaining way to reach a declared count above the team ceiling, which is
// precisely the state the reconciler backstop exists to catch.
func setReplicas(t *testing.T, store *api.Store, teamKey, role string, replicas int) {
	t.Helper()
	if err := store.UpdateTeam(teamKey, func(live *api.Team) error {
		for i := range live.Roles {
			if live.Roles[i].Name == role {
				live.Roles[i].Replicas = replicas
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("set replicas: %v", err)
	}
}

func setMaxSessions(t *testing.T, store *api.Store, teamKey string, maxSessions int) {
	t.Helper()
	if err := store.UpdateTeam(teamKey, func(live *api.Team) error {
		live.Budget.MaxSessions = maxSessions
		return nil
	}); err != nil {
		t.Fatalf("set max_sessions: %v", err)
	}
}

func admissionEvents(t *testing.T, ring *events.Ring, kind events.Kind) []events.Event {
	t.Helper()
	return ring.Snapshot(events.Filter{Kind: kind}, 0)
}

// TestNoBudgetNoAdmissionEvents is the default-open regression. A team that
// declares no budget must reconcile exactly as it did before admission
// existed: same replicas, and not one event of any admission kind.
//
// Falsification: any gate that evaluates before checking Budget.Declared
// would either change replica convergence or leave a trace here.
func TestNoBudgetNoAdmissionEvents(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createTeamFixture(t, store, "test-adm-open", "agents", []api.Role{sleepRole("worker", 3)})

	for i := 0; i < 10; i++ {
		ctrl.ReconcileOnce()
	}

	if got := len(store.ListSessionsByTeamRole("test-adm-open", "agents", "worker")); got != 3 {
		t.Fatalf("expected 3 sessions, got %d", got)
	}
	for _, kind := range []events.Kind{events.KindAdmissionRefused, events.KindAdmissionCleared, events.KindAdmissionUnmeasured} {
		if evs := admissionEvents(t, ring, kind); len(evs) != 0 {
			t.Errorf("undeclared budget emitted %d %s event(s): %+v", len(evs), kind, evs)
		}
	}
}

// TestAdmissionRefusesOverBudgetFanOut is the backstop firing on the one
// state a manifest declaration cannot see: a declared count above the team
// ceiling, reached out of band. No new sessions, and one event carrying both
// numbers.
func TestAdmissionRefusesOverBudgetFanOut(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createBudgetTeamFixture(t, store, "test-adm-refuse", "crew", api.Budget{MaxSessions: 3}, []api.Role{sleepRole("crew", 3)})
	teamKey := "test-adm-refuse/crew"

	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole("test-adm-refuse", "crew", "crew")); got != 3 {
		t.Fatalf("expected 3 sessions at the ceiling, got %d", got)
	}

	setReplicas(t, store, teamKey, "crew", 40)
	ctrl.ReconcileOnce()

	if got := len(store.ListSessionsByTeamRole("test-adm-refuse", "crew", "crew")); got != 3 {
		t.Fatalf("refused spawn still created sessions: got %d, want 3", got)
	}
	evs := admissionEvents(t, ring, events.KindAdmissionRefused)
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 admission.refused event, got %d", len(evs))
	}
	if evs[0].Severity != events.SeverityWarning {
		t.Errorf("severity = %q, want warning so refusals land in `marvel events --warnings`", evs[0].Severity)
	}
	for _, want := range []string{"max_sessions=3", "declares 40 sessions", "trigger=reconcile"} {
		if !strings.Contains(evs[0].Message, want) {
			t.Errorf("message = %q, missing %q", evs[0].Message, want)
		}
	}

	team, _ := store.GetTeam(teamKey)
	if !team.Admission.Held || team.Admission.Role != "crew" {
		t.Errorf("Team.Admission = %+v, want held on role crew", team.Admission)
	}
}

// TestAdmissionEmitsOncePerTransition is the event-ring guard.
//
// Falsification: without the verdict-key latch this records ten events for
// ten refused ticks. At the 2s reconcile interval and a 2000-entry ring, one
// event per tick per role flushes the whole ring in about 67 minutes and
// erases every other event class, so a naive refusal is a denial of service
// on the operator's own observability.
func TestAdmissionEmitsOncePerTransition(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createBudgetTeamFixture(t, store, "test-adm-latch", "crew", api.Budget{MaxSessions: 2}, []api.Role{sleepRole("crew", 2)})
	teamKey := "test-adm-latch/crew"
	ctrl.ReconcileOnce()

	setReplicas(t, store, teamKey, "crew", 9)
	for i := 0; i < 10; i++ {
		ctrl.ReconcileOnce()
	}
	if got := len(admissionEvents(t, ring, events.KindAdmissionRefused)); got != 1 {
		t.Fatalf("10 refused ticks emitted %d events, want 1", got)
	}

	// Raising the ceiling ends the condition: one clearing, then the latch
	// is free to fire again on a genuinely new refusal.
	setMaxSessions(t, store, teamKey, 9)
	ctrl.ReconcileOnce()
	if got := len(admissionEvents(t, ring, events.KindAdmissionCleared)); got != 1 {
		t.Fatalf("clearing emitted %d events, want 1", got)
	}

	setMaxSessions(t, store, teamKey, 2)
	setReplicas(t, store, teamKey, "crew", 20)
	for i := 0; i < 5; i++ {
		ctrl.ReconcileOnce()
	}
	if got := len(admissionEvents(t, ring, events.KindAdmissionRefused)); got != 2 {
		t.Fatalf("a new refusal after a clearing emitted %d total events, want 2", got)
	}
}

// TestAdmissionRefusalDoesNotTouchRoleHealth is the highest-value test in
// the slice.
//
// Falsification: routing a refusal through noteCrashAndBackoff would climb
// RestartCount on every tick until MaxRestarts saturation froze BackoffUntil
// in the year 9999 — written through to the bolt role_health bucket and
// rehydrated at daemon start. A budget refusal would silently become an
// unrecoverable role kill that survives a restart.
func TestAdmissionRefusalDoesNotTouchRoleHealth(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	role := sleepRole("crew", 1)
	role.MaxRestarts = 2
	createBudgetTeamFixture(t, store, "test-adm-health", "crew", api.Budget{MaxSessions: 1}, []api.Role{role})
	teamKey := "test-adm-health/crew"
	ctrl.ReconcileOnce()

	setReplicas(t, store, teamKey, "crew", 8)
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
	}

	if rh, ok := ctrl.RoleHealthSnapshot("test-adm-health", "crew", "crew"); ok {
		t.Fatalf("20 refused ticks recorded crash-loop state: %+v", rh)
	}
	if _, held := ctrl.AdmissionHold("test-adm-health", "crew", "crew"); !held {
		t.Error("expected the role to be held by admission")
	}
}

// TestAdmissionDoesNotClearCrashMarkers pins the placement of the gate.
//
// Falsification: a gate placed after ClearCrashedForRole would delete this
// role's crash history on every wholly-refused tick while never spawning,
// because that call mutates store state. The marker is injected rather than
// produced by a real crash on purpose: a crash frees a live slot, which
// leaves headroom, and any tick with headroom legitimately spawns and
// legitimately clears. The case under test is the one with no headroom at
// all, where the gate must return having changed nothing.
func TestAdmissionDoesNotClearCrashMarkers(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createBudgetTeamFixture(t, store, "test-adm-marker", "crew", api.Budget{MaxSessions: 2}, []api.Role{sleepRole("crew", 2)})
	teamKey := "test-adm-marker/crew"
	ctrl.ReconcileOnce()
	if got := api.CountAlive(store.ListSessionsByTeam("test-adm-marker", "crew")); got != 2 {
		t.Fatalf("live sessions = %d, want 2 at the ceiling", got)
	}

	// A crash marker from an earlier generation, past its observability job.
	if err := store.CreateSession(&api.Session{
		Name: "crew-crew-g1-ghost", Workspace: "test-adm-marker", Team: "crew", Role: "crew",
		Generation: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep"},
		State: api.SessionCrashed, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("inject crash marker: %v", err)
	}

	setReplicas(t, store, teamKey, "crew", 9)
	for i := 0; i < 5; i++ {
		ctrl.ReconcileOnce()
	}

	if got := countState(store, "test-adm-marker", "crew", api.SessionCrashed); got != 1 {
		t.Errorf("crash marker count = %d after wholly-refused ticks, want 1", got)
	}
	if got := api.CountAlive(store.ListSessionsByTeam("test-adm-marker", "crew")); got != 2 {
		t.Errorf("live sessions = %d, want 2: a refusal with no headroom must spawn nothing", got)
	}
}

func countState(store *api.Store, workspace, team string, state api.SessionState) int {
	n := 0
	for _, s := range store.ListSessionsByTeam(workspace, team) {
		if s.State == state {
			n++
		}
	}
	return n
}

// TestAdmissionDoesNotBlockRepair is the R1 invariant end to end: a team
// declared exactly at its ceiling still replaces a crashed replica.
//
// Falsification: gating repair on live + want > limit means a crashed
// replica never returns, and a budget becomes an outage rather than a
// ceiling.
func TestAdmissionDoesNotBlockRepair(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	clock := newTestClock(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createBudgetTeamFixture(t, store, "test-adm-repair", "crew", api.Budget{MaxSessions: 2}, []api.Role{sleepRole("crew", 2)})
	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeamRole("test-adm-repair", "crew", "crew")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	killPaneAndWait(t, sessions[0].PaneID)
	ctrl.ReconcileOnce()

	clock.Advance(2 * time.Minute)
	ctrl.ReconcileOnce()

	alive := api.CountAlive(store.ListSessionsByTeam("test-adm-repair", "crew"))
	if alive != 2 {
		t.Errorf("live sessions = %d after repair, want 2: a budget at the declared count blocked a replacement", alive)
	}
	if evs := admissionEvents(t, ring, events.KindAdmissionRefused); len(evs) != 0 {
		t.Errorf("repair produced %d refusal(s): %+v", len(evs), evs)
	}
}

// TestAdmissionBackoffTakesPrecedence pins the ordering by position: a
// cooling role returns before admission is evaluated, so the two conditions
// never race for the operator's attention and no wasted work happens inside
// a backoff window.
func TestAdmissionBackoffTakesPrecedence(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	clock := newTestClock(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createBudgetTeamFixture(t, store, "test-adm-backoff", "crew", api.Budget{MaxSessions: 2}, []api.Role{sleepRole("crew", 2)})
	teamKey := "test-adm-backoff/crew"
	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeamRole("test-adm-backoff", "crew", "crew")
	killPaneAndWait(t, sessions[0].PaneID)
	setReplicas(t, store, teamKey, "crew", 9)
	ctrl.ReconcileOnce() // reaps, records the crash, enters backoff

	rh, ok := ctrl.RoleHealthSnapshot("test-adm-backoff", "crew", "crew")
	if !ok || rh.BackoffUntil.IsZero() {
		t.Fatalf("expected a backoff window after the reap, got %+v", rh)
	}
	for i := 0; i < 5; i++ {
		ctrl.ReconcileOnce()
	}
	if evs := admissionEvents(t, ring, events.KindAdmissionRefused); len(evs) != 0 {
		t.Fatalf("admission spoke during a backoff window: %+v", evs)
	}

	clock.Advance(2 * time.Minute)
	ctrl.ReconcileOnce()
	if got := len(admissionEvents(t, ring, events.KindAdmissionRefused)); got != 1 {
		t.Errorf("after the window elapsed, admission emitted %d event(s), want 1", got)
	}
}

// TestAdmissionSkippedDuringShift covers R5 at the reconciler.
//
// Falsification: without the shift skip, a launching generation's transient
// double count refuses a non-shifting role's legitimate repair, and a budget
// equal to declared replicas cannot rotate at all.
func TestAdmissionSkippedDuringShift(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createBudgetTeamFixture(t, store, "test-adm-shift", "crew", api.Budget{MaxSessions: 2}, []api.Role{sleepRole("crew", 2)})
	teamKey := "test-adm-shift/crew"
	ctrl.ReconcileOnce()

	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift under a budget equal to declared replicas: %v", err)
	}
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		team, _ := store.GetTeam(teamKey)
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}
	team, _ := store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift did not complete under a declared budget, phase: %s", team.Shift.Phase)
	}
	if evs := admissionEvents(t, ring, events.KindAdmissionRefused); len(evs) != 0 {
		t.Errorf("a rolling shift produced %d refusal(s): %+v", len(evs), evs)
	}
	if got := len(store.ListSessionsByTeamRoleGeneration("test-adm-shift", "crew", "crew", 2)); got != 2 {
		t.Errorf("gen-2 sessions = %d, want 2", got)
	}
}

// TestAdmissionPartialGrant is the reconciler's half of the asymmetry:
// convergence takes headroom rather than nothing, and the event names both
// numbers so the operator sees what was and was not granted.
func TestAdmissionPartialGrant(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	// A differently named team and role, so the latch's keying is exercised
	// rather than assumed.
	createBudgetTeamFixture(t, store, "test-adm-partial", "squad", api.Budget{MaxSessions: 3}, []api.Role{sleepRole("worker", 1)})
	teamKey := "test-adm-partial/squad"
	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole("test-adm-partial", "squad", "worker")); got != 1 {
		t.Fatalf("expected 1 session, got %d", got)
	}

	// Deficit 5, headroom 2.
	setReplicas(t, store, teamKey, "worker", 6)
	ctrl.ReconcileOnce()

	if got := api.CountAlive(store.ListSessionsByTeam("test-adm-partial", "squad")); got != 3 {
		t.Fatalf("live sessions = %d, want 3 (headroom taken, ceiling respected)", got)
	}
	evs := admissionEvents(t, ring, events.KindAdmissionRefused)
	if len(evs) != 1 {
		t.Fatalf("expected 1 refusal event, got %d", len(evs))
	}
	for _, want := range []string{"refused 3 of 5", "granted 2"} {
		if !strings.Contains(evs[0].Message, want) {
			t.Errorf("message = %q, missing %q", evs[0].Message, want)
		}
	}
}

// TestAdmissionGrantsInFullWithoutClaimingARefusal covers the reconciler half
// of "the refusal surface must not report refusals that did not happen".
//
// An over-ceiling declaration makes the count clause Exceeded for every role
// in the team, including roles the remaining headroom fully satisfies.
//
// Falsification: with the decision keyed on the clause rather than on what was
// granted, role a's tick logged, emitted admission.refused at warning
// severity, and latched Team.Admission{Held:true, Role:"a"} while spawning all
// three of the sessions it asked for. The event message said so in as many
// words: "refused 0 of 3 spawn(s) ... granted 3".
func TestAdmissionGrantsInFullWithoutClaimingARefusal(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	// Declared 4 against a ceiling of 3: role a fits inside the headroom, role
	// b does not.
	createBudgetTeamFixture(t, store, "test-adm-full", "crew", api.Budget{MaxSessions: 3},
		[]api.Role{sleepRole("a", 3), sleepRole("b", 1)})
	ctrl.ReconcileOnce()

	if got := api.CountAlive(store.ListSessionsByTeamRole("test-adm-full", "crew", "a")); got != 3 {
		t.Errorf("role a live = %d, want 3 (the headroom covered its whole ask)", got)
	}
	evs := admissionEvents(t, ring, events.KindAdmissionRefused)
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 refusal (role b), got %d: %+v", len(evs), evs)
	}
	if evs[0].Role != "b" {
		t.Errorf("refusal named role %q, want b: role a was satisfied in full", evs[0].Role)
	}
	if strings.Contains(evs[0].Message, "refused 0 of") {
		t.Errorf("message = %q, which reports a refusal of nothing", evs[0].Message)
	}
	if _, held := ctrl.AdmissionHold("test-adm-full", "crew", "a"); held {
		t.Error("role a is latched as held after a tick that spawned every session it asked for")
	}
	team, _ := store.GetTeam("test-adm-full/crew")
	if team.Admission.Role != "b" {
		t.Errorf("Team.Admission names role %q, want b", team.Admission.Role)
	}
}

// TestAdmissionNeverSpawnsPastReplicas is the overspawn regression.
//
// Falsification: with the partial grant unclamped, a team-wide headroom larger
// than one role's deficit handed the whole headroom to that role, so the
// reconciler spawned five sessions for a one-replica role and deleted four of
// them on the next tick. Those are real tmux panes and real harness processes,
// launched and killed seconds apart.
func TestAdmissionNeverSpawnsPastReplicas(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	// Declared 6 against a ceiling of 5, and the supervisor role is
	// reconciled first with the whole headroom available to it.
	createBudgetTeamFixture(t, store, "test-adm-clamp", "crew", api.Budget{MaxSessions: 5},
		[]api.Role{sleepRole("sup", 1), sleepRole("worker", 5)})

	// Asserted on the FIRST tick: the excess was deleted on the next one, so a
	// steady-state-only check cannot see the panes that were launched and
	// killed in between.
	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole("test-adm-clamp", "crew", "sup")); got != 1 {
		t.Errorf("sup sessions = %d after one tick, want 1: a 1-replica role took the team's whole headroom", got)
	}

	for i := 0; i < 2; i++ {
		ctrl.ReconcileOnce()
	}
	if got := len(store.ListSessionsByTeamRole("test-adm-clamp", "crew", "sup")); got != 1 {
		t.Errorf("sup sessions = %d at steady state, want 1", got)
	}
	if got := api.CountAlive(store.ListSessionsByTeam("test-adm-clamp", "crew")); got != 5 {
		t.Errorf("live sessions = %d, want 5 (the ceiling)", got)
	}
	for _, ev := range admissionEvents(t, ring, events.KindAdmissionRefused) {
		if strings.Contains(ev.Message, "refused -") {
			t.Errorf("message = %q, want no negative refusal count", ev.Message)
		}
	}
}

// TestAdmissionClearsWhenBudgetRaised is the recovery path: raising the
// ceiling resumes spawning within one tick, with no manual clear command,
// no resume verb, and no daemon restart.
func TestAdmissionClearsWhenBudgetRaised(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	// The manager is the producer of session.created, so this test wires it
	// too: resuming spawns is half of what recovery means.
	sessMgr.Events = ring

	createBudgetTeamFixture(t, store, "test-adm-clear", "crew", api.Budget{MaxSessions: 3}, []api.Role{sleepRole("crew", 3)})
	teamKey := "test-adm-clear/crew"
	ctrl.ReconcileOnce()

	setReplicas(t, store, teamKey, "crew", 7)
	ctrl.ReconcileOnce()
	if got := len(admissionEvents(t, ring, events.KindAdmissionRefused)); got != 1 {
		t.Fatalf("expected the role held, got %d refusal event(s)", got)
	}

	setMaxSessions(t, store, teamKey, 7)
	ctrl.ReconcileOnce()

	if got := api.CountAlive(store.ListSessionsByTeam("test-adm-clear", "crew")); got != 7 {
		t.Errorf("live sessions = %d after raising the ceiling, want 7", got)
	}
	if got := len(admissionEvents(t, ring, events.KindAdmissionCleared)); got != 1 {
		t.Errorf("admission.cleared events = %d, want 1", got)
	}
	if len(ring.Snapshot(events.Filter{Kind: events.KindSessionCreated}, 0)) == 0 {
		t.Error("no session.created event after the ceiling was raised")
	}
	team, _ := store.GetTeam(teamKey)
	if team.Admission.Held {
		t.Errorf("Team.Admission still held: %+v", team.Admission)
	}
	if _, held := ctrl.AdmissionHold("test-adm-clear", "crew", "crew"); held {
		t.Error("the latch outlived its condition")
	}
}

// stubSnapshots supplies a fixed measured state, so a cumulative clause can
// be exercised with no accountant and no model auth.
type stubSnapshots struct{ snap admission.Snapshot }

func (s stubSnapshots) AdmissionSnapshot(api.Team) admission.Snapshot { return s.snap }

// TestInitiateShiftRefusedOnTokens covers the shift gate's placement.
//
// Falsification: gating inside shiftLaunch instead would return early and
// leave the team in phase=launching until abortStuckShift fires, so the
// operator would see team.shift-timed-out instead of a budget warning, ten
// minutes late. Here the refusal is synchronous, the phase never moves, and
// no timeout event ever appears.
func TestInitiateShiftRefusedOnTokens(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	clock := newTestClock(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now
	ctrl.ShiftTimeout = 2 * time.Minute
	ctrl.Snapshots = stubSnapshots{snap: admission.Snapshot{
		LiveSessions:     2,
		DeclaredSessions: 2,
		TokensObserved:   2_118_443,
		TokensMetered:    true,
		TokensSeen:       2,
		Since:            clock.Now().Add(-14 * time.Minute),
	}}

	createBudgetTeamFixture(t, store, "test-adm-shift-tok", "crew",
		api.Budget{MaxSessions: 2, MaxTokens: 2_000_000}, []api.Role{sleepRole("crew", 2)})
	teamKey := "test-adm-shift-tok/crew"
	ctrl.ReconcileOnce()

	err := ctrl.InitiateShift(teamKey, "")
	if err == nil {
		t.Fatal("expected the rotation refused against an exhausted token budget")
	}
	for _, want := range []string{"max_tokens=2000000", "trigger=shift"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, missing %q", err, want)
		}
	}

	team, _ := store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("refused shift still wrote shift state: phase %s", team.Shift.Phase)
	}
	if team.Generation != 1 {
		t.Errorf("refused shift advanced the generation to %d", team.Generation)
	}
	if got := len(admissionEvents(t, ring, events.KindAdmissionRefused)); got != 1 {
		t.Errorf("admission.refused events = %d, want 1", got)
	}

	clock.Advance(5 * time.Minute)
	ctrl.ReconcileOnce()
	if evs := ring.Snapshot(events.Filter{Kind: events.KindShiftTimedOut}, 0); len(evs) != 0 {
		t.Errorf("a refused shift produced a shift timeout: %+v", evs)
	}
}

// TestInitiateShiftAdmittedUnderSessionCeiling is the other side of R5: a
// count ceiling exactly at declared replicas must not forbid a rotation,
// even though the overlap transiently doubles the live count.
func TestInitiateShiftAdmittedUnderSessionCeiling(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ctrl.Snapshots = stubSnapshots{snap: admission.Snapshot{
		LiveSessions: 2, DeclaredSessions: 2, TokensMetered: true, TokensSeen: 2,
		Since: time.Now().UTC(),
	}}

	createBudgetTeamFixture(t, store, "test-adm-shift-ok", "crew", api.Budget{MaxSessions: 2}, []api.Role{sleepRole("crew", 2)})
	ctrl.ReconcileOnce()

	if err := ctrl.InitiateShift("test-adm-shift-ok/crew", ""); err != nil {
		t.Fatalf("a session ceiling at declared replicas forbade a rotation: %v", err)
	}
}

// TestAdmissionHoldSelfHealsAfterRestart: the latch is in-memory because the
// condition is derived from live state, so a durable copy could only outlive
// its cause. A recorded Team.Admission rehydrated from bolt is corrected
// within the first tick rather than left describing a condition that has
// passed.
func TestAdmissionHoldSelfHealsAfterRestart(t *testing.T) {
	skipIfNoTmux(t)
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	store1, ctrl1, cleanup1 := setupWithBolt(t, path)
	createBudgetTeamFixture(t, store1, "test-adm-restart", "crew", api.Budget{MaxSessions: 2}, []api.Role{sleepRole("crew", 2)})
	teamKey := "test-adm-restart/crew"
	ctrl1.ReconcileOnce()
	setReplicas(t, store1, teamKey, "crew", 8)
	ctrl1.ReconcileOnce()

	team, _ := store1.GetTeam(teamKey)
	if !team.Admission.Held {
		t.Fatalf("expected the role held before the restart: %+v", team.Admission)
	}
	cleanup1()

	store2, ctrl2, cleanup2 := setupWithBolt(t, path)
	t.Cleanup(cleanup2)
	if _, held := ctrl2.AdmissionHold("test-adm-restart", "crew", "crew"); held {
		t.Error("an admission hold was rehydrated; it is derived state and must not persist")
	}

	// The condition has passed: the ceiling now covers the declaration.
	setMaxSessions(t, store2, teamKey, 8)
	ctrl2.ReconcileOnce()

	team, _ = store2.GetTeam(teamKey)
	if team.Admission.Held {
		t.Errorf("a rehydrated admission state outlived its cause: %+v", team.Admission)
	}
}
