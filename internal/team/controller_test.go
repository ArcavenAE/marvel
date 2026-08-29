package team

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/tmux"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func setup(t *testing.T) (*api.Store, *session.Manager, *Controller, func()) {
	t.Helper()
	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	sessMgr := session.NewManager(store, driver)
	ctrl := NewController(store, sessMgr)

	cleanup := func() {
		for _, ws := range store.ListWorkspaces() {
			_ = sessMgr.CleanupWorkspace(ws.Name)
		}
	}

	return store, sessMgr, ctrl, cleanup
}

func createTeamFixture(t *testing.T, store *api.Store, wsName, teamName string, roles []api.Role) {
	t.Helper()
	ws := &api.Workspace{Name: wsName, CreatedAt: time.Now().UTC()}
	if err := store.CreateWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	team := &api.Team{
		Name:       teamName,
		Workspace:  wsName,
		Roles:      roles,
		Generation: 1,
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileScaleUp(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-reconcile", "agents", []api.Role{
		{Name: "worker", Replicas: 3, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeamRole("test-reconcile", "agents", "worker")
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	for _, s := range sessions {
		if s.State != api.SessionRunning {
			t.Fatalf("session %s: expected running, got %s", s.Name, s.State)
		}
		if s.Role != "worker" {
			t.Fatalf("session %s: expected role worker, got %s", s.Name, s.Role)
		}
		if s.Generation != 1 {
			t.Fatalf("session %s: expected generation 1, got %d", s.Name, s.Generation)
		}
		if !strings.Contains(s.Name, "-g1-") {
			t.Fatalf("session %s: expected g1 in name", s.Name)
		}
	}
}

func TestReconcileScaleDown(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-scaledown", "agents", []api.Role{
		{Name: "worker", Replicas: 3, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()
	if len(store.ListSessionsByTeamRole("test-scaledown", "agents", "worker")) != 3 {
		t.Fatal("expected 3 sessions after scale up")
	}

	if err := store.UpdateTeam("test-scaledown/agents", func(live *api.Team) error {
		live.Roles[0].Replicas = 1
		return nil
	}); err != nil {
		t.Fatalf("scale down: %v", err)
	}
	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeamRole("test-scaledown", "agents", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after scale down, got %d", len(sessions))
	}
}

func TestReconcileReplaceDead(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-replace", "agents", []api.Role{
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-replace", "agents", "worker")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	if err := sessMgr.Delete(sessions[0].Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	ctrl.ReconcileOnce()

	sessions = store.ListSessionsByTeamRole("test-replace", "agents", "worker")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions after reconcile, got %d", len(sessions))
	}
}

func TestReconcileMultipleRoles(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-multi", "squad", []api.Role{
		{Name: "supervisor", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "worker", Replicas: 3, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()

	supervisors := store.ListSessionsByTeamRole("test-multi", "squad", "supervisor")
	if len(supervisors) != 1 {
		t.Fatalf("expected 1 supervisor, got %d", len(supervisors))
	}

	workers := store.ListSessionsByTeamRole("test-multi", "squad", "worker")
	if len(workers) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(workers))
	}

	all := store.ListSessionsByTeam("test-multi", "squad")
	if len(all) != 4 {
		t.Fatalf("expected 4 total sessions, got %d", len(all))
	}

	if err := store.UpdateTeam("test-multi/squad", func(live *api.Team) error {
		live.Roles[1].Replicas = 1
		return nil
	}); err != nil {
		t.Fatalf("scale down worker: %v", err)
	}
	ctrl.ReconcileOnce()

	workers = store.ListSessionsByTeamRole("test-multi", "squad", "worker")
	if len(workers) != 1 {
		t.Fatalf("expected 1 worker after scale, got %d", len(workers))
	}

	supervisors = store.ListSessionsByTeamRole("test-multi", "squad", "supervisor")
	if len(supervisors) != 1 {
		t.Fatalf("expected 1 supervisor still, got %d", len(supervisors))
	}
}

// --- Shift tests ---

func TestShiftFullLifecycle(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-shift", "squad", []api.Role{
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	teamKey := "test-shift/squad"

	// Initial reconcile creates gen-1 sessions.
	ctrl.ReconcileOnce()
	gen1 := store.ListSessionsByTeamRoleGeneration("test-shift", "squad", "worker", 1)
	if len(gen1) != 2 {
		t.Fatalf("expected 2 gen-1 sessions, got %d", len(gen1))
	}

	// Initiate shift.
	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	team, _ := store.GetTeam(teamKey)
	if team.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", team.Generation)
	}
	if team.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("expected launching, got %s", team.Shift.Phase)
	}

	// Run reconcile ticks until shift completes.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		team, _ = store.GetTeam(teamKey)
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}
	team, _ = store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift didn't complete after 20 ticks, phase: %s", team.Shift.Phase)
	}

	// Verify gen-1 sessions are gone.
	gen1 = store.ListSessionsByTeamRoleGeneration("test-shift", "squad", "worker", 1)
	if len(gen1) != 0 {
		t.Fatalf("expected 0 gen-1 sessions after shift, got %d", len(gen1))
	}

	// Verify gen-2 sessions exist.
	gen2 := store.ListSessionsByTeamRoleGeneration("test-shift", "squad", "worker", 2)
	if len(gen2) != 2 {
		t.Fatalf("expected 2 gen-2 sessions after shift, got %d", len(gen2))
	}

	// Only gen-2 sessions remain.
	all := store.ListSessionsByTeamRole("test-shift", "squad", "worker")
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions after shift, got %d", len(all))
	}
	for _, s := range all {
		if s.Generation != 2 {
			t.Fatalf("session %s: expected gen 2, got %d", s.Name, s.Generation)
		}
	}
}

func TestShiftMultipleRoles(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-shift-multi", "squad", []api.Role{
		{Name: "supervisor", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	teamKey := "test-shift-multi/squad"

	ctrl.ReconcileOnce()

	// Initiate shift — supervisor should shift last.
	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	team, _ := store.GetTeam(teamKey)
	if team.Shift.Roles[0] != "worker" {
		t.Fatalf("expected worker to shift first, got %s", team.Shift.Roles[0])
	}
	if team.Shift.Roles[1] != "supervisor" {
		t.Fatalf("expected supervisor to shift last, got %s", team.Shift.Roles[1])
	}

	// Run reconcile ticks until shift completes.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		team, _ = store.GetTeam(teamKey)
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}

	team, _ = store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift didn't complete after 20 ticks, phase: %s", team.Shift.Phase)
	}

	// All sessions should be gen 2.
	for _, s := range store.ListSessionsByTeam("test-shift-multi", "squad") {
		if s.Generation != 2 {
			t.Fatalf("session %s: expected gen 2, got %d", s.Name, s.Generation)
		}
	}
}

func TestShiftAlreadyInProgress(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-shift-dup", "squad", []api.Role{
		{Name: "worker", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	ctrl.ReconcileOnce()

	if err := ctrl.InitiateShift("test-shift-dup/squad", ""); err != nil {
		t.Fatalf("first shift: %v", err)
	}

	err := ctrl.InitiateShift("test-shift-dup/squad", "")
	if err == nil {
		t.Fatal("expected error for double shift")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("expected 'already in progress' error, got: %v", err)
	}
}

func TestShiftSingleRole(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-shift-single", "squad", []api.Role{
		{Name: "supervisor", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	teamKey := "test-shift-single/squad"

	ctrl.ReconcileOnce()

	// Shift only workers.
	if err := ctrl.InitiateShift(teamKey, "worker"); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	team, _ := store.GetTeam(teamKey)
	if len(team.Shift.Roles) != 1 {
		t.Fatalf("expected 1 role in shift, got %d", len(team.Shift.Roles))
	}

	// Run ticks until complete.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		team, _ = store.GetTeam(teamKey)
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}

	team, _ = store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift didn't complete")
	}

	// Workers should be gen 2, supervisor should still be gen 1.
	for _, s := range store.ListSessionsByTeamRole("test-shift-single", "squad", "worker") {
		if s.Generation != 2 {
			t.Fatalf("worker %s: expected gen 2, got %d", s.Name, s.Generation)
		}
	}
	for _, s := range store.ListSessionsByTeamRole("test-shift-single", "squad", "supervisor") {
		if s.Generation != 1 {
			t.Fatalf("supervisor %s: expected gen 1 (not shifted), got %d", s.Name, s.Generation)
		}
	}
}

// --- Health tests ---

func TestHealthEvalHeartbeatHealthy(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-health-ok", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartAlways,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 30 * time.Second, FailureThreshold: 3},
		},
	})

	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-health-ok", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	// Simulate a fresh heartbeat (commit through the store so the
	// controller sees the updated value on the next tick).
	sess := sessions[0]
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	ctrl.ReconcileOnce()

	// Session should be healthy.
	sess, _ = store.GetSession(sess.Key())
	if sess.HealthState != api.HealthHealthy {
		t.Fatalf("expected healthy, got %s", sess.HealthState)
	}
	if sess.FailureCount != 0 {
		t.Fatalf("expected 0 failures, got %d", sess.FailureCount)
	}
}

func TestHealthEvalHeartbeatStale(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-health-stale", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartNever,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 2},
		},
	})

	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-health-stale", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	// Set a stale heartbeat (well past timeout).
	sess := sessions[0]
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	// First eval: failure count 1 (below threshold of 2).
	ctrl.ReconcileOnce()
	sess, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatalf("session should still exist (restart_policy=never): %v", err)
	}
	if sess.FailureCount != 1 {
		t.Fatalf("expected 1 failure after first eval, got %d", sess.FailureCount)
	}

	// Second eval: failure count 2 (meets threshold).
	ctrl.ReconcileOnce()
	sess, err = store.GetSession(sess.Key())
	if err != nil {
		t.Fatalf("session should still exist (restart_policy=never): %v", err)
	}
	if sess.State != api.SessionFailed {
		t.Fatalf("expected failed state with restart_policy=never, got %s", sess.State)
	}
}

func TestHealthEvalNoConfig(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-health-noconf", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			// No HealthCheck, no RestartPolicy override
		},
	})

	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-health-noconf", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	// No heartbeat ever sent — should stay unknown, not fail.
	ctrl.ReconcileOnce()
	ctrl.ReconcileOnce()
	ctrl.ReconcileOnce()

	sess, err := store.GetSession(sessions[0].Key())
	if err != nil {
		t.Fatal("session should still exist (no healthcheck)")
	}
	if sess.HealthState != api.HealthUnknown {
		t.Fatalf("expected unknown health, got %s", sess.HealthState)
	}
	if sess.State != api.SessionRunning {
		t.Fatalf("expected running, got %s", sess.State)
	}
}

func TestHealthRestartAlways(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	// Clock injection: lets us fast-forward past the crash-loop backoff
	// window so we can observe recreation in a single test run.
	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-health-restart", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartAlways,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-health-restart", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	origCreatedAt := sessions[0].CreatedAt

	// Set stale heartbeat.
	if err := store.UpdateSession(sessions[0].Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	// Tick 1: unhealthy → first restart is immediate (count goes 0→1),
	// session is deleted, but reconciler holds off on recreating because
	// the role is now in backoff.
	ctrl.ReconcileOnce()
	if got := store.ListSessionsByTeamRole("test-health-restart", "squad", "worker"); len(got) != 0 {
		t.Fatalf("expected 0 sessions immediately after first restart (backoff active), got %d", len(got))
	}
	rh, ok := ctrl.RoleHealthSnapshot("test-health-restart", "squad", "worker")
	if !ok || rh.RestartCount != 1 {
		t.Fatalf("expected RestartCount=1 after first restart, got %+v (ok=%v)", rh, ok)
	}

	// Fast-forward past the backoff window. The reconciler should now
	// see actual < desired and respawn a replacement.
	clock.Advance(2 * time.Minute)
	ctrl.ReconcileOnce()

	sessions = store.ListSessionsByTeamRole("test-health-restart", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after backoff elapsed, got %d", len(sessions))
	}
	if !sessions[0].CreatedAt.After(origCreatedAt) {
		t.Fatal("expected new session with later CreatedAt after restart")
	}
}

// TestHealthRestartBackoffHoldsReplacement: after the first restart
// the reconciler must NOT respawn a replacement for the dead replica
// until the backoff window elapses.
func TestHealthRestartBackoffHoldsReplacement(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-health-hold", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartAlways,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	ctrl.ReconcileOnce()
	staleKey := store.ListSessionsByTeamRole("test-health-hold", "squad", "worker")[0].Key()
	if err := store.UpdateSession(staleKey, func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}
	ctrl.ReconcileOnce() // first restart triggered + session deleted

	// Several reconciler ticks while still inside backoff: actual=0,
	// desired=1, but no respawn because the role is cooling down.
	for i := 0; i < 5; i++ {
		clock.Advance(5 * time.Second)
		ctrl.ReconcileOnce()
		got := store.ListSessionsByTeamRole("test-health-hold", "squad", "worker")
		if len(got) != 0 {
			t.Fatalf("tick %d: backoff violated — found %d sessions", i, len(got))
		}
	}

	// Backoff elapses, respawn happens.
	clock.Advance(90 * time.Second) // definitely past 60s initial backoff
	ctrl.ReconcileOnce()
	got := store.ListSessionsByTeamRole("test-health-hold", "squad", "worker")
	if len(got) != 1 {
		t.Fatalf("expected respawn once backoff elapsed, got %d sessions", len(got))
	}
}

// TestHealthRestartBackoffSiblingMarked: when one replica triggers the
// backoff and a sibling in the same role then fails, the sibling is
// marked SessionCrashLoopBackOff and kept alive — it does NOT get a
// second restart inside the same cooling window.
func TestHealthRestartBackoffSiblingMarked(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-health-sibling", "squad", []api.Role{
		{
			Name: "worker", Replicas: 2,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartAlways,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	ctrl.ReconcileOnce() // both workers created
	workers := store.ListSessionsByTeamRole("test-health-sibling", "squad", "worker")
	if len(workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(workers))
	}

	// Fail worker-0 → triggers first restart for the role.
	if err := store.UpdateSession(workers[0].Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}
	ctrl.ReconcileOnce()

	// Now fail worker-1 while still inside the backoff window.
	// (Only one session survives; find it fresh from the store.)
	workers = store.ListSessionsByTeamRole("test-health-sibling", "squad", "worker")
	if len(workers) != 1 {
		t.Fatalf("expected 1 surviving worker during backoff, got %d", len(workers))
	}
	if err := store.UpdateSession(workers[0].Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}
	ctrl.ReconcileOnce()

	// Sibling must be alive, marked CrashLoopBackOff, and the role
	// restart counter must NOT have ticked past 1.
	workers = store.ListSessionsByTeamRole("test-health-sibling", "squad", "worker")
	if len(workers) != 1 {
		t.Fatalf("sibling should still exist during backoff, got %d", len(workers))
	}
	if workers[0].State != api.SessionCrashLoopBackOff {
		t.Fatalf("sibling expected SessionCrashLoopBackOff, got %q", workers[0].State)
	}
	rh, _ := ctrl.RoleHealthSnapshot("test-health-sibling", "squad", "worker")
	if rh.RestartCount != 1 {
		t.Fatalf("RestartCount must stay at 1 during backoff, got %d", rh.RestartCount)
	}
}

// TestHealthRestartMaxReached: once the role passes MaxRestarts the
// session is marked Failed permanently and not respawned.
func TestHealthRestartMaxReached(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-health-maxrst", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartAlways,
			MaxRestarts:   2,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	// Loop: reconcile, stale the current session, advance clock, reconcile.
	// After 2 successful restarts, a third failure must not be restarted.
	for i := 0; i < 3; i++ {
		ctrl.ReconcileOnce() // creates or recreates session
		got := store.ListSessionsByTeamRole("test-health-maxrst", "squad", "worker")
		if len(got) == 0 {
			t.Fatalf("iteration %d: expected a running session", i)
		}
		if err := store.UpdateSession(got[0].Key(), func(live *api.Session) error {
			live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
			return nil
		}); err != nil {
			t.Fatalf("iteration %d: update heartbeat: %v", i, err)
		}
		ctrl.ReconcileOnce() // fail + (maybe) restart
		clock.Advance(10 * time.Minute)
	}

	rh, _ := ctrl.RoleHealthSnapshot("test-health-maxrst", "squad", "worker")
	if rh.RestartCount != 2 {
		t.Fatalf("expected RestartCount capped at MaxRestarts=2, got %d", rh.RestartCount)
	}
	// After the third failure, the session should be in Failed (not
	// recreated, not CrashLoopBackOff).
	ctrl.ReconcileOnce()
	got := store.ListSessionsByTeamRole("test-health-maxrst", "squad", "worker")
	if len(got) != 1 {
		t.Fatalf("expected failed session still in store, got %d", len(got))
	}
	if got[0].State != api.SessionFailed {
		t.Fatalf("expected state=failed after MaxRestarts exceeded, got %q", got[0].State)
	}
}

// TestReapPathBumpsRestartCount verifies that when a session's pane
// vanishes (clean exit / manual kill) the reap path records the crash
// against the role's RoleHealth — previously this path bypassed the
// restart bookkeeping entirely, producing the zero-spacing respawn loop
// Skippy reported as ArcavenAE/marvel#11.
func TestReapPathBumpsRestartCount(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-reap-bump", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			// No HealthCheck: isolates the reap path from the
			// heartbeat-staleness path in evaluateHealth.
		},
	})

	ctrl.ReconcileOnce()
	sess := store.ListSessionsByTeamRole("test-reap-bump", "squad", "worker")[0]

	// Simulate a clean exit / manual pane kill.
	killPaneAndWait(t, sess.PaneID)

	ctrl.ReconcileOnce() // reap + bookkeeping + backoff gate

	rh, ok := ctrl.RoleHealthSnapshot("test-reap-bump", "squad", "worker")
	if !ok {
		t.Fatal("expected RoleHealth snapshot after reap")
	}
	if rh.RestartCount != 1 {
		t.Fatalf("expected RestartCount=1 after reap, got %d", rh.RestartCount)
	}
	if !rh.BackoffUntil.After(clock.Now()) {
		t.Fatalf("expected BackoffUntil after now, got %s (now=%s)", rh.BackoffUntil, clock.Now())
	}
	// The reap path keeps the session in the store as a Crashed
	// marker so operators see the transient via `marvel get sessions`,
	// but the reconciler must NOT have respawned a live replacement
	// during backoff.
	got := store.ListSessionsByTeamRole("test-reap-bump", "squad", "worker")
	if len(got) != 1 {
		t.Fatalf("expected 1 Crashed marker during reap backoff, got %d", len(got))
	}
	if got[0].State != api.SessionCrashed {
		t.Fatalf("expected Crashed state, got %s", got[0].State)
	}
	if got[0].PaneID != "" {
		t.Fatalf("expected empty PaneID on crashed marker, got %q", got[0].PaneID)
	}
}

// TestReapPathRespawnsAfterBackoff: after the reap-triggered backoff
// window elapses, the reconciler respawns the replica.
func TestReapPathRespawnsAfterBackoff(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-reap-respawn", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		},
	})

	ctrl.ReconcileOnce()
	sess := store.ListSessionsByTeamRole("test-reap-respawn", "squad", "worker")[0]
	origCreatedAt := sess.CreatedAt

	killPaneAndWait(t, sess.PaneID)
	ctrl.ReconcileOnce() // reap + bookkeeping

	// Advance past the initial backoff window (60s for restart #1).
	clock.Advance(90 * time.Second)
	ctrl.ReconcileOnce()

	got := store.ListSessionsByTeamRole("test-reap-respawn", "squad", "worker")
	if len(got) != 1 {
		t.Fatalf("expected 1 session after backoff elapsed (crashed marker cleared at respawn), got %d", len(got))
	}
	if got[0].State != api.SessionRunning {
		t.Fatalf("expected respawn to be Running (crashed marker should be cleared), got %s", got[0].State)
	}
	if !got[0].CreatedAt.After(origCreatedAt) {
		t.Fatal("expected new session with later CreatedAt after reap-triggered restart")
	}
}

// TestReapPathSaturatesMaxRestarts: a role whose only crash path is
// reap-via-clean-exit still honors MaxRestarts. Once the budget is
// exhausted, the reconciler stops respawning replacements (BackoffUntil
// is frozen to the far future by noteCrashAndBackoff on saturation).
func TestReapPathSaturatesMaxRestarts(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-reap-maxrst", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			MaxRestarts: 2,
		},
	})

	// Two reap-and-respawn cycles consume the budget.
	for i := 0; i < 2; i++ {
		ctrl.ReconcileOnce()
		got := store.ListSessionsByTeamRole("test-reap-maxrst", "squad", "worker")
		if len(got) == 0 {
			t.Fatalf("iteration %d: expected a running session", i)
		}
		sess := got[0]
		killPaneAndWait(t, sess.PaneID)
		ctrl.ReconcileOnce() // reap + bump counter
		clock.Advance(10 * time.Minute)
	}

	rh, _ := ctrl.RoleHealthSnapshot("test-reap-maxrst", "squad", "worker")
	if rh.RestartCount != 2 {
		t.Fatalf("expected RestartCount=2 after two reap cycles, got %d", rh.RestartCount)
	}

	// Third cycle: respawn, kill, reap — counter must NOT bump past 2,
	// and the reconciler must refuse to spawn a fourth replacement.
	ctrl.ReconcileOnce()
	got := store.ListSessionsByTeamRole("test-reap-maxrst", "squad", "worker")
	if len(got) == 0 {
		t.Fatal("expected a running session before saturation check")
	}
	sess := got[0]
	killPaneAndWait(t, sess.PaneID)
	ctrl.ReconcileOnce() // reap; saturation freezes BackoffUntil

	rh, _ = ctrl.RoleHealthSnapshot("test-reap-maxrst", "squad", "worker")
	if rh.RestartCount != 2 {
		t.Fatalf("RestartCount must stay at MaxRestarts=2 after saturation, got %d", rh.RestartCount)
	}
	if rh.BackoffUntil != saturationFreezeUntil {
		t.Fatalf("expected BackoffUntil frozen at saturation sentinel, got %s", rh.BackoffUntil)
	}

	// No matter how far we advance the clock, no live replacement
	// spawns — saturation leaves at most one Crashed marker behind
	// (the capping in ReapDead / ClearCrashedForRole keeps ghosts
	// bounded).
	clock.Advance(24 * time.Hour)
	ctrl.ReconcileOnce()
	got = store.ListSessionsByTeamRole("test-reap-maxrst", "squad", "worker")
	alive := 0
	for _, s := range got {
		if s.State.CountsAsAlive() {
			alive++
		}
	}
	if alive != 0 {
		t.Fatalf("expected 0 alive sessions after saturation, got %d (all=%d)", alive, len(got))
	}
	if len(got) > 1 {
		t.Fatalf("expected at most 1 Crashed marker after saturation, got %d", len(got))
	}
}

// TestComputeBackoff locks in the exponential curve shape so future
// tweaks are intentional and reviewable.
// TestClearRoleHealthForTeam verifies that the cascade-delete helper
// removes only the entries under (workspace, team), leaves siblings
// untouched, and handles the prefix edge case where one team name is
// a prefix of another (e.g., "b" vs "bb"). See ArcavenAE/marvel#29.
func TestClearRoleHealthForTeam(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	ctrl := NewController(store, nil)

	// Populate state for: ws1/teamA/{w,r}, ws1/teamAA/w, ws1/teamB/w, ws2/teamA/w
	keys := []string{
		"ws1/teamA/worker",
		"ws1/teamA/reviewer",
		"ws1/teamAA/worker",
		"ws1/teamB/worker",
		"ws2/teamA/worker",
	}
	for _, k := range keys {
		ctrl.roleHealth[k] = &RoleHealth{RestartCount: 3}
	}

	ctrl.ClearRoleHealthForTeam("ws1", "teamA")

	want := map[string]bool{
		"ws1/teamAA/worker": true, // must survive — name has teamA as prefix but isn't teamA
		"ws1/teamB/worker":  true,
		"ws2/teamA/worker":  true,
	}
	for k, rh := range ctrl.roleHealth {
		if !want[k] {
			t.Errorf("ws1/teamA cleared but %q still present (count=%d)", k, rh.RestartCount)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("expected %q to survive, but it was deleted", k)
	}
}

// TestClearRoleHealthForWorkspace verifies workspace-level cascade
// clears every team's roles under that workspace, and that another
// workspace whose name shares a prefix (e.g., "ws1" vs "ws1-alt") is
// not affected. See ArcavenAE/marvel#29.
func TestClearRoleHealthForWorkspace(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	ctrl := NewController(store, nil)

	keys := []string{
		"ws1/teamA/worker",
		"ws1/teamA/reviewer",
		"ws1/teamB/worker",
		"ws1-alt/teamA/worker", // must survive
		"ws2/teamA/worker",     // must survive
	}
	for _, k := range keys {
		ctrl.roleHealth[k] = &RoleHealth{RestartCount: 5}
	}

	ctrl.ClearRoleHealthForWorkspace("ws1")

	want := map[string]bool{
		"ws1-alt/teamA/worker": true,
		"ws2/teamA/worker":     true,
	}
	for k, rh := range ctrl.roleHealth {
		if !want[k] {
			t.Errorf("ws1 cleared but %q still present (count=%d)", k, rh.RestartCount)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("expected %q to survive, but it was deleted", k)
	}
}

// TestClearRoleHealthForTeamEmpty verifies the no-op case: clearing
// a workspace/team that was never recorded leaves the map unchanged
// and does not panic on an empty map.
func TestClearRoleHealthForTeamEmpty(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	ctrl := NewController(store, nil)

	ctrl.ClearRoleHealthForTeam("never", "recorded")
	if len(ctrl.roleHealth) != 0 {
		t.Errorf("expected empty map, got %d entries", len(ctrl.roleHealth))
	}

	ctrl.ClearRoleHealthForWorkspace("never")
	if len(ctrl.roleHealth) != 0 {
		t.Errorf("expected empty map, got %d entries", len(ctrl.roleHealth))
	}
}

func TestComputeBackoff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want time.Duration
	}{
		{0, 30 * time.Second},
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second},
		{4, 240 * time.Second},
		{5, 5 * time.Minute}, // capped
		{20, 5 * time.Minute},
	}
	for _, tc := range cases {
		if got := computeBackoff(tc.n); got != tc.want {
			t.Errorf("computeBackoff(%d) = %s, want %s", tc.n, got, tc.want)
		}
	}
}

// testClock is a simple monotonically-advancing clock used in place of
// time.Now for deterministic crash-loop tests.
type testClock struct {
	t time.Time
}

func newTestClock(start time.Time) *testClock { return &testClock{t: start} }
func (c *testClock) Now() time.Time           { return c.t }
func (c *testClock) Advance(d time.Duration)  { c.t = c.t.Add(d) }

// killPaneAndWait shells out to tmux to kill a pane out-of-band (as an
// external process would) and waits until tmux confirms the pane is gone
// so ReapDead can observe the loss on the next reconcile tick. Mirrors
// the Skippy repro from ArcavenAE/marvel#11: `marvel inject ... "exit"`
// — a clean exit that vacates the pane without going through the health
// path. Uses the same MARVEL_TMUX_SOCKET as the driver so tests stay
// scoped to their per-package tmux server.
func killPaneAndWait(t *testing.T, paneID string) {
	t.Helper()
	if err := tmuxTestCmd("kill-pane", "-t", paneID).Run(); err != nil {
		t.Fatalf("tmux kill-pane %s: %v", paneID, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, err := tmuxTestCmd("list-panes", "-a", "-F", "#{pane_id}").CombinedOutput()
		if err != nil || !strings.Contains(string(out), paneID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux still reports pane %s alive after kill-pane", paneID)
}

// tmuxTestCmd builds a tmux invocation against the per-package test
// server TestMain created, so an out-of-band kill in a test never
// reaches the operator's own tmux.
func tmuxTestCmd(args ...string) *exec.Cmd {
	if socket := os.Getenv(tmux.SocketEnv); socket != "" {
		return exec.Command("tmux", append([]string{"-L", socket}, args...)...)
	}
	return exec.Command("tmux", args...)
}

// killWorkspaceAndWait destroys a workspace's whole tmux session out of
// band: the shape of an external event that takes every replica of a role
// down together, such as a foreign daemon reclaiming the marvel-* prefix
// or a `tmux kill-server`. Waits until tmux confirms the session is gone
// so the next reconcile tick observes the loss.
func killWorkspaceAndWait(t *testing.T, workspace string) {
	t.Helper()
	target := "marvel-" + workspace
	if out, err := tmuxTestCmd("kill-session", "-t", target).CombinedOutput(); err != nil {
		t.Fatalf("tmux kill-session %s: %v (%s)", target, err, out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, err := tmuxTestCmd("list-sessions", "-F", "#{session_name}").CombinedOutput()
		if err != nil || !strings.Contains(string(out), target) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux still reports session %s alive after kill-session", target)
}

// TestReapChargesOneCrashPerRolePerTick: a reconcile tick that finds k
// panes of one role gone records ONE crash against that role, not k.
// RoleHealth is per-role state, so charging it per lost replica scaled a
// single event by the replica count — a three-replica role went from
// restart count 0 to 3 and from a 30s backoff to a 4m one on a single
// external kill. See aae-orc-4bz2.
func TestReapChargesOneCrashPerRolePerTick(t *testing.T) {
	skipIfNoTmux(t)

	runtime := api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}
	cases := []struct {
		name      string
		workspace string
		roles     []api.Role
	}{
		{
			name:      "one replica lost",
			workspace: "test-charge-one",
			roles: []api.Role{
				{Name: "worker", Replicas: 1, RestartPolicy: api.RestartAlways, Runtime: runtime},
			},
		},
		{
			name:      "three replicas of one role lost together",
			workspace: "test-charge-three",
			roles: []api.Role{
				{Name: "worker", Replicas: 3, RestartPolicy: api.RestartAlways, Runtime: runtime},
			},
		},
		{
			name:      "two roles lost together are charged separately",
			workspace: "test-charge-two-roles",
			roles: []api.Role{
				{Name: "worker", Replicas: 3, RestartPolicy: api.RestartAlways, Runtime: runtime},
				{Name: "supervisor", Replicas: 2, RestartPolicy: api.RestartAlways, Runtime: runtime},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _, ctrl, cleanup := setup(t)
			t.Cleanup(cleanup)

			clock := newTestClock(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
			ctrl.now = clock.Now

			createTeamFixture(t, store, tc.workspace, "squad", tc.roles)
			ctrl.ReconcileOnce()
			for _, role := range tc.roles {
				if got := len(store.ListSessionsByTeamRole(tc.workspace, "squad", role.Name)); got != role.Replicas {
					t.Fatalf("role %s: expected %d sessions, got %d", role.Name, role.Replicas, got)
				}
			}

			killWorkspaceAndWait(t, tc.workspace)
			ctrl.ReconcileOnce()

			for _, role := range tc.roles {
				rh, ok := ctrl.RoleHealthSnapshot(tc.workspace, "squad", role.Name)
				if !ok {
					t.Fatalf("role %s: expected crash-loop state after the loss", role.Name)
				}
				if rh.RestartCount != 1 {
					t.Errorf("role %s: expected 1 crash for the tick, got %d", role.Name, rh.RestartCount)
				}
				if want := clock.Now().Add(computeBackoff(2)); !rh.BackoffUntil.Equal(want) {
					t.Errorf("role %s: expected backoff until %s, got %s", role.Name, want, rh.BackoffUntil)
				}
			}
		})
	}
}

// TestExternalLossDoesNotExhaustMaxRestarts: losing every replica at once
// must leave the role's restart budget intact and let the reconciler
// repair the team when the backoff window elapses. Before the per-tick
// charge, a three-replica role with max_restarts=3 spent its whole budget
// on one event, and the next loss froze the role at the saturation
// sentinel where the only recovery is deleting the team.
func TestExternalLossDoesNotExhaustMaxRestarts(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	ws := "test-external-loss"
	createTeamFixture(t, store, ws, "squad", []api.Role{{
		Name: "worker", Replicas: 3, MaxRestarts: 3,
		RestartPolicy: api.RestartAlways,
		Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}})

	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole(ws, "squad", "worker")); got != 3 {
		t.Fatalf("expected 3 sessions, got %d", got)
	}

	killWorkspaceAndWait(t, ws)
	ctrl.ReconcileOnce()

	rh, ok := ctrl.RoleHealthSnapshot(ws, "squad", "worker")
	if !ok {
		t.Fatal("expected crash-loop state after the loss")
	}
	if rh.RestartCount != 1 {
		t.Fatalf("expected 1 of 3 restarts spent, got %d", rh.RestartCount)
	}
	if rh.BackoffUntil.Equal(saturationFreezeUntil) {
		t.Fatal("role frozen at the saturation sentinel after a single loss")
	}
	if alive := aliveCount(store, ws, "squad", "worker"); alive != 0 {
		t.Fatalf("expected the backoff window to hold replacements, got %d alive", alive)
	}

	clock.Advance(2 * time.Minute)
	ctrl.ReconcileOnce()

	if alive := aliveCount(store, ws, "squad", "worker"); alive != 3 {
		t.Fatalf("expected 3 replicas back after the backoff window, got %d", alive)
	}
}

// aliveCount reports how many of a role's sessions count toward its
// replica total, which is what the reconciler repairs against.
func aliveCount(store *api.Store, workspace, team, role string) int {
	n := 0
	for _, sess := range store.ListSessionsByTeamRole(workspace, team, role) {
		if sess.State.CountsAsAlive() {
			n++
		}
	}
	return n
}

func TestShiftSessionNaming(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-shift-names", "squad", []api.Role{
		{Name: "worker", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()

	gen1 := store.ListSessionsByTeamRoleGeneration("test-shift-names", "squad", "worker", 1)
	if len(gen1) != 1 {
		t.Fatalf("expected 1 gen-1 session, got %d", len(gen1))
	}
	if !strings.Contains(gen1[0].Name, "-g1-") {
		t.Fatalf("expected g1 in name, got %s", gen1[0].Name)
	}

	if err := ctrl.InitiateShift("test-shift-names/squad", ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	// Run until complete.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		team, _ := store.GetTeam("test-shift-names/squad")
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}

	gen2 := store.ListSessionsByTeamRoleGeneration("test-shift-names", "squad", "worker", 2)
	if len(gen2) != 1 {
		t.Fatalf("expected 1 gen-2 session, got %d", len(gen2))
	}
	if !strings.Contains(gen2[0].Name, "-g2-") {
		t.Fatalf("expected g2 in name, got %s", gen2[0].Name)
	}
}

// --- Recovery-correctness trio (aae-orc-69i2 / qkfl / 96st) ---

// TestOrphanedSessionsDrainedOnRoleRemoval covers aae-orc-69i2: removing
// a role from a re-applied manifest (which replaces the role set
// wholesale) must tear down that role's sessions, not leave them running
// and uncounted forever. Falsification: without reconcileOrphanedSessions,
// role b's session survives the re-apply and this fails on the drained
// count.
func TestOrphanedSessionsDrainedOnRoleRemoval(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createTeamFixture(t, store, "test-orphan", "squad", []api.Role{
		{Name: "a", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "b", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole("test-orphan", "squad", "a")); got != 1 {
		t.Fatalf("expected 1 session for role a, got %d", got)
	}
	if got := len(store.ListSessionsByTeamRole("test-orphan", "squad", "b")); got != 1 {
		t.Fatalf("expected 1 session for role b, got %d", got)
	}
	// Give role b some crash-loop state so we can assert it is forgotten
	// when the role is removed.
	ctrl.roleHealth["test-orphan/squad/b"] = &RoleHealth{RestartCount: 2}

	// Re-apply with only role a — this is exactly what Manifest.Apply does
	// (live.Roles = roles).
	if err := store.UpdateTeam("test-orphan/squad", func(live *api.Team) error {
		live.Roles = []api.Role{live.Roles[0]}
		return nil
	}); err != nil {
		t.Fatalf("re-apply team: %v", err)
	}

	ctrl.ReconcileOnce()

	if got := len(store.ListSessionsByTeam("test-orphan", "squad")); got != 1 {
		t.Fatalf("expected 1 total session after role b removed, got %d", got)
	}
	if got := len(store.ListSessionsByTeamRole("test-orphan", "squad", "a")); got != 1 {
		t.Fatalf("expected role a still running, got %d", got)
	}
	if got := len(store.ListSessionsByTeamRole("test-orphan", "squad", "b")); got != 0 {
		t.Fatalf("expected role b drained, got %d sessions", got)
	}
	if _, ok := ctrl.roleHealth["test-orphan/squad/b"]; ok {
		t.Fatal("expected role b crash-loop state forgotten after removal")
	}
	removed := ring.Snapshot(events.Filter{Kind: events.KindRoleRemoved, Role: "b"}, 0)
	if len(removed) == 0 {
		t.Fatal("expected a role.removed event for role b")
	}
}

// TestShiftTimeoutAbortsStuckLaunch covers aae-orc-qkfl: a shift whose new
// generation never becomes ready (a heartbeat-checked role that never
// beats) must hit the timeout and abort, rolling back to the old
// generation, rather than hang in phase=launching forever. Falsification:
// without the timeout check in reconcileShift, the shift stays launching
// after the clock advances and this fails on the phase assertion.
func TestShiftTimeoutAbortsStuckLaunch(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now
	ctrl.ShiftTimeout = 2 * time.Minute

	// A generous heartbeat timeout keeps the sessions in HealthUnknown
	// (never marked unhealthy) so the ONLY thing blocking shift completion
	// is allReady's requirement of a first heartbeat — which never arrives.
	createTeamFixture(t, store, "test-shift-stuck", "squad", []api.Role{
		{
			Name: "worker", Replicas: 2,
			Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			HealthCheck: &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Hour, FailureThreshold: 3},
		},
	})
	teamKey := "test-shift-stuck/squad"

	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRoleGeneration("test-shift-stuck", "squad", "worker", 1)); got != 2 {
		t.Fatalf("expected 2 gen-1 sessions, got %d", got)
	}

	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	// One tick launches gen-2 but cannot complete (no heartbeat).
	ctrl.ReconcileOnce()
	team, _ := store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("expected launching before timeout, got %s", team.Shift.Phase)
	}
	if got := len(store.ListSessionsByTeamRoleGeneration("test-shift-stuck", "squad", "worker", 2)); got != 2 {
		t.Fatalf("expected 2 gen-2 sessions launched, got %d", got)
	}

	// Move past the timeout and reconcile: the shift must abort.
	clock.Advance(3 * time.Minute)
	ctrl.ReconcileOnce()

	team, _ = store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("expected shift aborted (phase none) after timeout, got %s", team.Shift.Phase)
	}
	if got := len(store.ListSessionsByTeamRoleGeneration("test-shift-stuck", "squad", "worker", 2)); got != 0 {
		t.Fatalf("expected stuck gen-2 torn down on abort, got %d", got)
	}
	if got := len(store.ListSessionsByTeamRoleGeneration("test-shift-stuck", "squad", "worker", 1)); got != 2 {
		t.Fatalf("expected old gen-1 kept on rollback, got %d", got)
	}
	if len(ring.Snapshot(events.Filter{Kind: events.KindShiftTimedOut}, 0)) == 0 {
		t.Fatal("expected a team.shift-timed-out event")
	}
}

// TestSessionFailedEventOnRestartNever covers aae-orc-96st: a role with
// restart_policy=never whose session fails health must emit
// events.KindSessionFailed. Falsification: without the emit in
// applyRestartPolicy's RestartNever case, no session.failed event is
// recorded and this fails.
func TestSessionFailedEventOnRestartNever(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createTeamFixture(t, store, "test-failed-never", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartNever,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	ctrl.ReconcileOnce()
	sess := store.ListSessionsByTeamRole("test-failed-never", "squad", "worker")[0]
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	ctrl.ReconcileOnce()

	got, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatalf("session should still exist (restart_policy=never): %v", err)
	}
	if got.State != api.SessionFailed {
		t.Fatalf("expected failed state, got %s", got.State)
	}
	failed := ring.Snapshot(events.Filter{Kind: events.KindSessionFailed, Session: sess.Key()}, 0)
	if len(failed) == 0 {
		t.Fatal("expected a session.failed event on restart_policy=never failure")
	}
}

// TestRestartNeverFreezesRole covers aae-orc-pyre / marvel#107: a role
// with restart_policy=never whose session fails health must go terminal.
// Falsification: without the freezeRole call in applyRestartPolicy's
// RestartNever case, SessionFailed drops out of CountsAsAlive and the
// reconciler spawns a replacement on the next tick — the session count
// grows past 1 and this fails.
func TestRestartNeverFreezesRole(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-never-freeze", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartNever,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	ctrl.ReconcileOnce()
	sess := store.ListSessionsByTeamRole("test-never-freeze", "squad", "worker")[0]
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	// The failing tick, then several repair opportunities.
	for i := 0; i < 4; i++ {
		ctrl.ReconcileOnce()
	}

	got := store.ListSessionsByTeamRole("test-never-freeze", "squad", "worker")
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 session after never failure (role terminal), got %d", len(got))
	}
	if got[0].State != api.SessionFailed {
		t.Fatalf("expected failed state, got %s", got[0].State)
	}
	rh, ok := ctrl.RoleHealthSnapshot("test-never-freeze", "squad", "worker")
	if !ok {
		t.Fatal("expected RoleHealth snapshot after never failure")
	}
	if !rh.BackoffUntil.After(time.Now().UTC().Add(100 * 365 * 24 * time.Hour)) {
		t.Fatalf("expected far-future freeze, got BackoffUntil=%s", rh.BackoffUntil)
	}
}

// TestRestartNeverReapFreezesRole covers the reap-path half of
// aae-orc-pyre / marvel#107: a vacated pane under restart_policy=never
// must also go terminal. Falsification: without the RestartNever branch
// in noteReapedCrash, the crash gets ordinary backoff accounting and the
// reconciler replaces the session once the window elapses.
func TestRestartNeverReapFreezesRole(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-never-reap", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartNever,
			// No HealthCheck: isolates the reap path from the
			// heartbeat-staleness path in evaluateHealth.
		},
	})

	ctrl.ReconcileOnce()
	sess := store.ListSessionsByTeamRole("test-never-reap", "squad", "worker")[0]

	killPaneAndWait(t, sess.PaneID)

	ctrl.ReconcileOnce()            // reap tick
	clock.Advance(10 * time.Minute) // far past any ordinary backoff window
	ctrl.ReconcileOnce()            // repair opportunity — must refuse

	got := store.ListSessionsByTeamRole("test-never-reap", "squad", "worker")
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 session after never reap (role terminal), got %d", len(got))
	}
	if got[0].State != api.SessionFailed {
		t.Fatalf("expected failed state after never reap, got %s", got[0].State)
	}
	failed := ring.Snapshot(events.Filter{Kind: events.KindSessionFailed, Session: sess.Key()}, 0)
	if len(failed) == 0 {
		t.Fatal("expected a session.failed event on the never reap path")
	}
}

// TestSessionFailedEventOnSaturation covers aae-orc-96st: a role that
// saturates MaxRestarts must emit events.KindSessionFailed in addition to
// events.KindRoleSaturated. Falsification: without the emit in
// restartSession's saturation branch, only role.saturated is recorded and
// the session.failed assertion fails.
func TestSessionFailedEventOnSaturation(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-failed-sat", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartAlways,
			MaxRestarts:   1,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	// Burn the single restart, then saturate on the next crash.
	for i := 0; i < 2; i++ {
		ctrl.ReconcileOnce()
		got := store.ListSessionsByTeamRole("test-failed-sat", "squad", "worker")
		if len(got) == 0 {
			continue
		}
		if err := store.UpdateSession(got[0].Key(), func(live *api.Session) error {
			live.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)
			return nil
		}); err != nil {
			t.Fatalf("iter %d update heartbeat: %v", i, err)
		}
		ctrl.ReconcileOnce()
		clock.Advance(10 * time.Minute)
	}

	if len(ring.Snapshot(events.Filter{Kind: events.KindRoleSaturated}, 0)) == 0 {
		t.Fatal("expected a role.saturated event on saturation")
	}
	if len(ring.Snapshot(events.Filter{Kind: events.KindSessionFailed}, 0)) == 0 {
		t.Fatal("expected a session.failed event on saturation")
	}
}

// setupWithBolt is setup() against a persistence-backed store, for the
// tests that assert crash-loop state survives a store reopen. The
// caller supplies the bolt path so a second call can reopen the same
// file as a fresh daemon would. See aae-orc-qdew.
func setupWithBolt(t *testing.T, boltPath string) (*api.Store, *Controller, func()) {
	t.Helper()
	store := api.NewStore()
	if err := store.OpenBolt(boltPath); err != nil {
		t.Fatalf("OpenBolt %s: %v", boltPath, err)
	}
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	sessMgr := session.NewManager(store, driver)
	ctrl := NewController(store, sessMgr)
	if err := ctrl.RehydrateRoleHealth(); err != nil {
		t.Fatalf("RehydrateRoleHealth: %v", err)
	}

	cleanup := func() {
		for _, ws := range store.ListWorkspaces() {
			_ = sessMgr.CleanupWorkspace(ws.Name)
		}
		_ = store.CloseBolt()
	}
	return store, ctrl, cleanup
}

// TestRoleHealthRoundTripsThroughStore covers the bucket wiring:
// noteCrashAndBackoff writes through to the store, a fresh controller
// reads the same values back, and the cascade-clear helpers remove the
// durable row as well as the in-memory one. No tmux needed: this is
// the persistence contract, not the reconciler.
func TestRoleHealthRoundTripsThroughStore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	store1 := api.NewStore()
	if err := store1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	ctrl1 := NewController(store1, nil)
	clock := newTestClock(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC))
	ctrl1.now = clock.Now

	if !ctrl1.noteCrashAndBackoff("ws1", "squad", "worker", 0) {
		t.Fatal("first crash should be recorded")
	}
	if !ctrl1.noteCrashAndBackoff("ws1", "squad", "reviewer", 0) {
		t.Fatal("sibling role crash should be recorded")
	}
	want, ok := ctrl1.RoleHealthSnapshot("ws1", "squad", "worker")
	if !ok {
		t.Fatal("expected in-memory role health for worker")
	}
	if err := store1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	store2 := api.NewStore()
	if err := store2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = store2.CloseBolt() })
	ctrl2 := NewController(store2, nil)
	if err := ctrl2.RehydrateRoleHealth(); err != nil {
		t.Fatalf("RehydrateRoleHealth: %v", err)
	}

	got, ok := ctrl2.RoleHealthSnapshot("ws1", "squad", "worker")
	if !ok {
		t.Fatal("worker role health did not survive the reopen")
	}
	if got.RestartCount != want.RestartCount {
		t.Fatalf("RestartCount after reopen: want %d, got %d", want.RestartCount, got.RestartCount)
	}
	if !got.BackoffUntil.Equal(want.BackoffUntil) {
		t.Fatalf("BackoffUntil after reopen: want %s, got %s", want.BackoffUntil, got.BackoffUntil)
	}
	if !got.LastRestartAt.Equal(want.LastRestartAt) {
		t.Fatalf("LastRestartAt after reopen: want %s, got %s", want.LastRestartAt, got.LastRestartAt)
	}
	if _, ok := ctrl2.RoleHealthSnapshot("ws1", "squad", "reviewer"); !ok {
		t.Fatal("reviewer role health did not survive the reopen")
	}

	// Cascade clear must reach the durable row too, or a re-applied
	// manifest inherits the prior generation's backoff. See marvel#29.
	ctrl2.ClearRoleHealthForTeam("ws1", "squad")
	recs, err := store2.ListRoleHealth()
	if err != nil {
		t.Fatalf("ListRoleHealth: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected no durable role-health rows after cascade clear, got %+v", recs)
	}
}

// TestSaturatedRoleStaysFrozenAcrossRestart is the behavioral half: a
// role that exhausted MaxRestarts must still refuse to spawn after the
// daemon restarts, no matter how far the clock has moved. Before
// RoleHealth was persisted the fresh controller started with an empty
// map and handed the saturated role a free respawn. See aae-orc-qdew.
func TestSaturatedRoleStaysFrozenAcrossRestart(t *testing.T) {
	skipIfNoTmux(t)
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	store1, ctrl1, cleanup1 := setupWithBolt(t, path)
	clock := newTestClock(time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC))
	ctrl1.now = clock.Now

	ws := "test-rh-frozen"
	createTeamFixture(t, store1, ws, "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			MaxRestarts: 1,
		},
	})

	// Burn the single restart, then saturate on the next crash.
	for i := 0; i < 2; i++ {
		ctrl1.ReconcileOnce()
		got := store1.ListSessionsByTeamRole(ws, "squad", "worker")
		if len(got) == 0 {
			t.Fatalf("iteration %d: expected a running session", i)
		}
		killPaneAndWait(t, got[0].PaneID)
		ctrl1.ReconcileOnce() // reap + crash bookkeeping
		clock.Advance(10 * time.Minute)
	}

	rh, ok := ctrl1.RoleHealthSnapshot(ws, "squad", "worker")
	if !ok {
		t.Fatal("expected role health after saturation")
	}
	if rh.BackoffUntil != saturationFreezeUntil {
		t.Fatalf("expected saturation freeze before restart, got BackoffUntil=%s count=%d",
			rh.BackoffUntil, rh.RestartCount)
	}

	// Simulate the daemon restart: release the bolt file without
	// touching panes (what Detach does), then reopen into fresh
	// in-memory state.
	cleanup1()

	store2, ctrl2, cleanup2 := setupWithBolt(t, path)
	t.Cleanup(cleanup2)
	clock2 := newTestClock(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	ctrl2.now = clock2.Now

	rh2, ok := ctrl2.RoleHealthSnapshot(ws, "squad", "worker")
	if !ok {
		t.Fatal("saturated role health did not survive the restart")
	}
	if rh2.BackoffUntil != saturationFreezeUntil {
		t.Fatalf("expected saturation freeze after restart, got %s", rh2.BackoffUntil)
	}

	// A day later the reconciler must still refuse to spawn.
	clock2.Advance(24 * time.Hour)
	ctrl2.ReconcileOnce()
	alive := 0
	for _, s := range store2.ListSessionsByTeamRole(ws, "squad", "worker") {
		if s.State.CountsAsAlive() {
			alive++
		}
	}
	if alive != 0 {
		t.Fatalf("saturated role respawned after restart: %d alive session(s)", alive)
	}
}

// TestContextReadingIsNotAHeartbeat is the coupling regression guard for
// the usage accountant.
//
// The accountant writes through Store.UpdateSessionContext, which
// deliberately does not touch LastHeartbeat. Routing it through
// UpdateSessionHeartbeat instead would be one convenient line, and it
// would silently redefine a heartbeat healthcheck from "the agent
// reported in" to "the harness emitted bytes", marking a hung but still
// streaming session healthy. This asserts the two stay separate at the
// level where the coupling lives, not just in the store.
func TestContextReadingIsNotAHeartbeat(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-ctx-not-heartbeat", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy: api.RestartNever,
			HealthCheck:   &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Millisecond, FailureThreshold: 1},
		},
	})

	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-ctx-not-heartbeat", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	key := sessions[0].Key()

	// The harness is streaming: context readings keep arriving.
	store.UpdateSessionContext(key, api.SessionContext{
		ContextTokens: 34136, ContextLimit: 1_000_000, ContextPercent: 3.4136,
	})

	// A session that streams but never heartbeats is still unhealthy.
	ctrl.ReconcileOnce()
	sess, err := store.GetSession(key)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.HealthState != api.HealthUnhealthy {
		t.Errorf("health = %s after a context reading and no heartbeat, want %s",
			sess.HealthState, api.HealthUnhealthy)
	}
	if sess.ContextTokens != 34136 {
		t.Errorf("context tokens = %d, want 34136 (the reading itself must survive)", sess.ContextTokens)
	}

	// And it is still not shift-ready: allReady requires a first heartbeat.
	role := api.Role{
		Name:        "worker",
		HealthCheck: &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: time.Hour, FailureThreshold: 3},
	}
	if err := store.UpdateSession(key, func(live *api.Session) error {
		live.State = api.SessionRunning
		live.HealthState = api.HealthUnknown
		return nil
	}); err != nil {
		t.Fatalf("reset session state: %v", err)
	}
	fresh, err := store.GetSession(key)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if ctrl.allReady([]api.Session{fresh}, &role) {
		t.Error("a session with a context reading but no heartbeat was declared shift-ready")
	}
}

// TestShiftRoleReadyEventFiresOnceAtTheTransition covers the gap orc
// finding-018 measured: the instant the control plane decides a successor
// generation may take over is made inside allReady, between session.created
// and session.deleted, and until this event nothing was written to the ring
// for it. The finding had to poll the session table at 50 Hz to timestamp it.
//
// The role is heartbeat-gated so the launching phase lasts several ticks
// with no heartbeat arriving. That separates "not ready yet" from "ready" in
// time, which a role with no healthcheck cannot do (it is ready on the same
// tick it is created). Falsification: without the emit in shiftLaunch the
// ring carries no team.shift-role-ready and the count assertion fails; with
// an emit placed inside allReady instead of at the phase flip, the count is
// the number of launching ticks rather than 1.
func TestShiftRoleReadyEventFiresOnceAtTheTransition(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	ctrl.ShiftTimeout = 1 * time.Hour

	// A one-hour heartbeat timeout keeps every session in HealthUnknown, so
	// the only thing gating the shift is allReady's first-heartbeat rule.
	createTeamFixture(t, store, "test-shift-ready", "squad", []api.Role{
		{
			Name: "worker", Replicas: 2,
			Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			HealthCheck: &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Hour, FailureThreshold: 3},
		},
	})
	teamKey := "test-shift-ready/squad"

	readyEvents := func() []events.Event {
		return ring.Snapshot(events.Filter{Kind: events.KindShiftRoleReady}, 0)
	}

	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRoleGeneration("test-shift-ready", "squad", "worker", 1)); got != 2 {
		t.Fatalf("expected 2 gen-1 sessions, got %d", got)
	}
	if got := len(readyEvents()); got != 0 {
		t.Fatalf("readiness events before any shift = %d, want 0", got)
	}

	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	// Several ticks in the launching phase with no heartbeat: the successors
	// exist and are running, but the control plane has not committed to them.
	for i := 0; i < 4; i++ {
		ctrl.ReconcileOnce()
		team, _ := store.GetTeam(teamKey)
		if team.Shift.Phase != api.ShiftLaunching {
			t.Fatalf("tick %d: phase = %s, want launching (no heartbeat has arrived)", i, team.Shift.Phase)
		}
		if got := len(readyEvents()); got != 0 {
			t.Fatalf("tick %d: readiness events = %d, want 0 before the successors are ready", i, got)
		}
	}

	gen2 := store.ListSessionsByTeamRoleGeneration("test-shift-ready", "squad", "worker", 2)
	if len(gen2) != 2 {
		t.Fatalf("expected 2 gen-2 sessions launched, got %d", len(gen2))
	}
	wantKeys := make([]string, 0, len(gen2))
	for _, s := range gen2 {
		wantKeys = append(wantKeys, s.Key())
		if err := store.UpdateSession(s.Key(), func(live *api.Session) error {
			live.LastHeartbeat = time.Now().UTC()
			return nil
		}); err != nil {
			t.Fatalf("set heartbeat on %s: %v", s.Key(), err)
		}
	}

	// The tick that observes the heartbeats is the readiness transition.
	ctrl.ReconcileOnce()
	team, _ := store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftDraining {
		t.Fatalf("phase = %s after heartbeats, want draining", team.Shift.Phase)
	}
	evs := readyEvents()
	if len(evs) != 1 {
		t.Fatalf("readiness events at the transition = %d, want exactly 1", len(evs))
	}

	ev := evs[0]
	if ev.Workspace != "test-shift-ready" || ev.Team != "squad" || ev.Role != "worker" {
		t.Errorf("event scope = %s/%s role %s, want test-shift-ready/squad role worker",
			ev.Workspace, ev.Team, ev.Role)
	}
	if ev.Generation != 2 {
		t.Errorf("event generation = %d, want 2 (the successor generation)", ev.Generation)
	}
	if ev.Severity != events.SeverityInfo {
		t.Errorf("severity = %s, want %s", ev.Severity, events.SeverityInfo)
	}
	for _, key := range wantKeys {
		if !strings.Contains(ev.Message, key) {
			t.Errorf("message %q does not name successor session %s", ev.Message, key)
		}
	}
	if !strings.Contains(ev.Message, string(api.HealthCheckHeartbeat)) {
		t.Errorf("message %q does not name the gate that admitted the successors", ev.Message)
	}

	// The readiness stamp must precede the first predecessor teardown.
	// That ordering is the whole point of having it in the ring.
	for _, d := range ring.Snapshot(events.Filter{Kind: events.KindSessionDeleted}, 0) {
		if d.Seq < ev.Seq {
			t.Errorf("session.deleted seq %d precedes readiness seq %d", d.Seq, ev.Seq)
		}
	}

	// Drive the shift to completion: the count must stay at 1 across every
	// remaining tick, including the ones that run after the shift clears.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		team, _ = store.GetTeam(teamKey)
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift did not complete, phase %s", team.Shift.Phase)
	}
	ctrl.ReconcileOnce()
	if got := len(readyEvents()); got != 1 {
		t.Fatalf("readiness events after the shift completed = %d, want exactly 1", got)
	}
}

// TestShiftRoleReadyEventAbsentWhenSuccessorNeverReady is the negative half
// of the bar: a successor generation that never satisfies allReady must never
// produce a readiness stamp, however many ticks it is given. The shift times
// out and rolls back instead. Falsification: an emit keyed on session
// creation or on pane-Running rather than on the allReady verdict would fire
// here, because these successors do reach Running.
func TestShiftRoleReadyEventAbsentWhenSuccessorNeverReady(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	clock := newTestClock(time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now
	ctrl.ShiftTimeout = 2 * time.Minute

	createTeamFixture(t, store, "test-shift-never-ready", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			HealthCheck: &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Hour, FailureThreshold: 3},
		},
	})
	teamKey := "test-shift-never-ready/squad"

	ctrl.ReconcileOnce()
	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	for i := 0; i < 5; i++ {
		ctrl.ReconcileOnce()
	}

	// The successor is Running but has never heartbeat, so it is not ready.
	gen2 := store.ListSessionsByTeamRoleGeneration("test-shift-never-ready", "squad", "worker", 2)
	if len(gen2) != 1 {
		t.Fatalf("expected 1 gen-2 session launched, got %d", len(gen2))
	}
	if gen2[0].State != api.SessionRunning {
		t.Fatalf("successor state = %s, want running (the test needs a live-but-unready pane)", gen2[0].State)
	}

	clock.Advance(3 * time.Minute)
	ctrl.ReconcileOnce()

	team, _ := store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %s, want none after the timeout aborted the shift", team.Shift.Phase)
	}
	if got := len(ring.Snapshot(events.Filter{Kind: events.KindShiftTimedOut}, 0)); got == 0 {
		t.Fatal("expected a team.shift-timed-out event")
	}
	if got := len(ring.Snapshot(events.Filter{Kind: events.KindShiftRoleReady}, 0)); got != 0 {
		t.Fatalf("readiness events for a successor that never became ready = %d, want 0", got)
	}
}

// TestShiftRoleReadyEventIsPerRole pins the granularity claim: the readiness
// stamp is per role, not per shift and not per session. A two-role shift
// crosses the launching-to-draining boundary twice, in shift order, so it
// must produce two events; a four-replica role crosses it once, so it must
// produce one. Falsification: an emit per successor session would give 5
// here rather than 2, and an emit hung off team.shift-completed would give 1.
func TestShiftRoleReadyEventIsPerRole(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createTeamFixture(t, store, "test-shift-ready-multi", "squad", []api.Role{
		{Name: "supervisor", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "worker", Replicas: 4, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	teamKey := "test-shift-ready-multi/squad"

	ctrl.ReconcileOnce()
	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}
	for i := 0; i < 40; i++ {
		ctrl.ReconcileOnce()
		team, _ := store.GetTeam(teamKey)
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}
	team, _ := store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift did not complete, phase %s", team.Shift.Phase)
	}

	evs := ring.Snapshot(events.Filter{Kind: events.KindShiftRoleReady}, 0)
	if len(evs) != 2 {
		t.Fatalf("readiness events for a 2-role shift = %d, want 2 (one per role)", len(evs))
	}
	// Snapshot is oldest-first, and shiftOrder puts the supervisor last.
	if evs[0].Role != "worker" || evs[1].Role != "supervisor" {
		t.Errorf("readiness roles = [%s %s], want [worker supervisor] (supervisor shifts last)",
			evs[0].Role, evs[1].Role)
	}
	for _, ev := range evs {
		if ev.Generation != 2 {
			t.Errorf("role %s: generation = %d, want 2", ev.Role, ev.Generation)
		}
		if !strings.Contains(ev.Message, "gate=running") {
			t.Errorf("role %s: message %q, want gate=running for a role with no healthcheck", ev.Role, ev.Message)
		}
	}
}

// roleGen is one role's post-condition: how many sessions it should have
// and which generation they should carry.
type roleGen struct {
	role       string
	count      int
	generation int64
}

// TestShiftAbortStateCoherence covers aae-orc-d0pt: the state a stuck
// shift's abort leaves behind must match what the abort event claims. Two
// cases, because the honest answer differs by how far the shift got.
//
// Falsification: without the Generation restore in abortStuckShift the
// launching case fails on team.Generation (2, want 1); without the
// old-generation tagging in reconcileShift it fails on the non-shifting
// role's sessions surviving the abort at generation 2; without the
// phase/index guard the draining case fails by restoring a generation
// whose sessions were already drained.
func TestShiftAbortStateCoherence(t *testing.T) {
	skipIfNoTmux(t)

	beat := func() *api.HealthCheck {
		// Generous timeout: the sessions stay HealthUnknown rather than
		// unhealthy, so the only thing blocking the shift is allReady's
		// first-heartbeat requirement, which never arrives.
		return &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 1 * time.Hour, FailureThreshold: 3}
	}
	sleeper := api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}

	tests := []struct {
		name string
		ws   string
		// roles in shift order; the stuck one carries the heartbeat gate.
		roles []api.Role
		// drive advances the shift to the point the abort should fire.
		drive func(t *testing.T, store *api.Store, sessMgr *session.Manager, ctrl *Controller, teamKey string)
		// wantGen is the team generation the abort must leave behind.
		wantGen int64
		// wantRoles is the per-role session state after the abort.
		wantRoles []roleGen
		wantMsg   string
	}{
		{
			// Nothing has drained, so the pre-shift state is still intact
			// and rollback is the honest outcome: the generation counter
			// goes back, and no session anywhere carries the abandoned
			// generation. The sidecar is deleted mid-shift to force the
			// repair path that mints replacements while a shift is open.
			name: "launching first role rolls back",
			ws:   "test-abort-launching",
			roles: []api.Role{
				{Name: "worker", Replicas: 1, Runtime: sleeper, HealthCheck: beat()},
				{Name: "sidecar", Replicas: 1, Runtime: sleeper},
			},
			drive: func(t *testing.T, store *api.Store, sessMgr *session.Manager, ctrl *Controller, teamKey string) {
				t.Helper()
				ctrl.ReconcileOnce() // launches the stuck gen-2 worker
				sides := store.ListSessionsByTeamRole("test-abort-launching", "squad", "sidecar")
				if len(sides) != 1 {
					t.Fatalf("sidecar sessions before repair = %d, want 1", len(sides))
				}
				if err := sessMgr.Delete(sides[0].Key()); err != nil {
					t.Fatalf("delete sidecar: %v", err)
				}
				ctrl.ReconcileOnce() // repairs the sidecar mid-shift
			},
			wantGen: 1,
			wantRoles: []roleGen{
				{role: "worker", count: 1, generation: 1},
				{role: "sidecar", count: 1, generation: 1},
			},
			wantMsg: "rolled back to gen 1",
		},
		{
			// alpha has already shifted and its old generation is gone, so
			// restoring the counter would point the team at a generation
			// that no longer exists. The shift stops where it stands and
			// the message says so.
			name: "draining past first role stops forward",
			ws:   "test-abort-forward",
			roles: []api.Role{
				{Name: "alpha", Replicas: 1, Runtime: sleeper},
				{Name: "beta", Replicas: 1, Runtime: sleeper, HealthCheck: beat()},
			},
			drive: func(t *testing.T, store *api.Store, sessMgr *session.Manager, ctrl *Controller, teamKey string) {
				t.Helper()
				// Ticks: alpha launches and goes ready, drains, index
				// advances, then beta launches and sticks.
				for i := 0; i < 6; i++ {
					ctrl.ReconcileOnce()
				}
				team, err := store.GetTeam(teamKey)
				if err != nil {
					t.Fatalf("get team: %v", err)
				}
				if team.Shift.RoleIndex != 1 {
					t.Fatalf("role index = %d, want 1 (alpha shifted, beta stuck)", team.Shift.RoleIndex)
				}
			},
			wantGen: 2,
			wantRoles: []roleGen{
				{role: "alpha", count: 1, generation: 2},
				{role: "beta", count: 1, generation: 1},
			},
			wantMsg: "stopped at gen 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, sessMgr, ctrl, cleanup := setup(t)
			t.Cleanup(cleanup)
			ring := events.NewRing(0)
			ctrl.Events = ring
			clock := newTestClock(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
			ctrl.now = clock.Now
			ctrl.ShiftTimeout = 2 * time.Minute

			createTeamFixture(t, store, tc.ws, "squad", tc.roles)
			teamKey := tc.ws + "/squad"

			ctrl.ReconcileOnce()
			if err := ctrl.InitiateShift(teamKey, ""); err != nil {
				t.Fatalf("initiate shift: %v", err)
			}
			tc.drive(t, store, sessMgr, ctrl, teamKey)

			clock.Advance(3 * time.Minute)
			ctrl.ReconcileOnce()

			team, err := store.GetTeam(teamKey)
			if err != nil {
				t.Fatalf("get team: %v", err)
			}
			if team.Generation != tc.wantGen {
				t.Errorf("team generation = %d, want %d", team.Generation, tc.wantGen)
			}
			if team.Shift.Phase != api.ShiftNone || team.Shift.OldGeneration != 0 ||
				team.Shift.RoleIndex != 0 || len(team.Shift.Roles) != 0 || !team.Shift.StartedAt.IsZero() {
				t.Errorf("shift state = %+v, want zero (shift cleared)", team.Shift)
			}
			for _, want := range tc.wantRoles {
				all := store.ListSessionsByTeamRole(tc.ws, "squad", want.role)
				if len(all) != want.count {
					t.Errorf("role %s: %d sessions, want %d", want.role, len(all), want.count)
				}
				for _, s := range all {
					if s.Generation != want.generation {
						t.Errorf("role %s: session %s at generation %d, want %d",
							want.role, s.Name, s.Generation, want.generation)
					}
				}
			}
			evs := ring.Snapshot(events.Filter{Kind: events.KindShiftTimedOut}, 0)
			if len(evs) != 1 {
				t.Fatalf("shift-timed-out events = %d, want 1", len(evs))
			}
			if !strings.Contains(evs[0].Message, tc.wantMsg) {
				t.Errorf("abort message = %q, want it to contain %q", evs[0].Message, tc.wantMsg)
			}

			// One more tick: the post-abort state must be a fixed point,
			// not a state normal reconciliation immediately re-mints out of.
			ctrl.ReconcileOnce()
			for _, want := range tc.wantRoles {
				for _, s := range store.ListSessionsByTeamRole(tc.ws, "squad", want.role) {
					if s.Generation != want.generation {
						t.Errorf("after a settling tick, role %s: session %s at generation %d, want %d",
							want.role, s.Name, s.Generation, want.generation)
					}
				}
			}
		})
	}
}

// TestShiftRepairBeforeTurnDoesNotCountAsLaunch covers the healthy-path
// half of aae-orc-d0pt's tagging defect. A role repaired while an earlier
// role is shifting must keep the old generation, or shiftLaunch counts the
// repair as that role's new generation when its turn comes and the role is
// declared shifted without ever rotating a session.
//
// Falsification: with the repair tagged at the team generation, beta's turn
// finds its replica count already satisfied at generation 2, so no
// beta-g2 session is created and the session predating the shift is the
// one carried forward.
func TestShiftRepairBeforeTurnDoesNotCountAsLaunch(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	sleeper := api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}

	createTeamFixture(t, store, "test-shift-repair", "squad", []api.Role{
		{Name: "alpha", Replicas: 1, Runtime: sleeper},
		{Name: "beta", Replicas: 1, Runtime: sleeper},
	})
	teamKey := "test-shift-repair/squad"

	ctrl.ReconcileOnce()
	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	// Kill beta while alpha holds the shift, then let the reconciler
	// repair it. beta has not had its turn, so the replacement belongs to
	// the generation beta is still running.
	betas := store.ListSessionsByTeamRole("test-shift-repair", "squad", "beta")
	if len(betas) != 1 {
		t.Fatalf("beta sessions = %d, want 1", len(betas))
	}
	if err := sessMgr.Delete(betas[0].Key()); err != nil {
		t.Fatalf("delete beta: %v", err)
	}
	ctrl.ReconcileOnce()

	repaired := store.ListSessionsByTeamRole("test-shift-repair", "squad", "beta")
	if len(repaired) != 1 {
		t.Fatalf("beta sessions after repair = %d, want 1", len(repaired))
	}
	if repaired[0].Generation != 1 {
		t.Fatalf("mid-shift repair of a role awaiting its turn = generation %d, want 1",
			repaired[0].Generation)
	}
	repairedKey := repaired[0].Key()

	// Run the shift out. beta's turn must launch a fresh generation-2
	// session and drain the repair, not adopt it.
	for i := 0; i < 40; i++ {
		ctrl.ReconcileOnce()
		team, err := store.GetTeam(teamKey)
		if err != nil {
			t.Fatalf("get team: %v", err)
		}
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}
	team, err := store.GetTeam(teamKey)
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift did not complete, phase %s", team.Shift.Phase)
	}
	if _, err := store.GetSession(repairedKey); err == nil {
		t.Errorf("session %s survived the shift, want it drained with generation 1", repairedKey)
	}
	for _, role := range []string{"alpha", "beta"} {
		sessions := store.ListSessionsByTeamRole("test-shift-repair", "squad", role)
		if len(sessions) != 1 {
			t.Errorf("role %s: %d sessions after the shift, want 1", role, len(sessions))
			continue
		}
		if sessions[0].Generation != 2 {
			t.Errorf("role %s: session %s at generation %d after the shift, want 2",
				role, sessions[0].Name, sessions[0].Generation)
		}
	}
}

// seedSession inserts a session directly into the store without spawning a
// tmux pane. PaneID stays empty, so ReapDead skips it (it treats an empty
// pane as "already reaped or never had one") and evaluateHealth skips it
// unless it is Running. This lets a counting test construct a precise mix of
// live and terminal new-generation rows without racing the session
// lifecycle. Runtime and CreatedAt default to a sleeper / now when unset.
func seedSession(t *testing.T, store *api.Store, s api.Session) {
	t.Helper()
	if s.Runtime.Command == "" {
		s.Runtime = api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if err := store.CreateSession(&s); err != nil {
		t.Fatalf("seed session %s: %v", s.Key(), err)
	}
}

// enterLaunchingShift puts an already-created team into a shift's launching
// phase over a single role, as InitiateShift would, but without driving the
// reconciler — the counting tests seed the generation rows themselves.
func enterLaunchingShift(t *testing.T, store *api.Store, teamKey, role string) {
	t.Helper()
	if err := store.UpdateTeam(teamKey, func(live *api.Team) error {
		live.Generation = 2
		live.Shift = api.ShiftState{
			Phase:         api.ShiftLaunching,
			OldGeneration: 1,
			Roles:         []string{role},
			RoleIndex:     0,
			StartedAt:     time.Now().UTC(),
		}
		return nil
	}); err != nil {
		t.Fatalf("enter launching shift: %v", err)
	}
}

// TestShiftLaunchDoesNotCountFailedNewGenTowardLaunch is the create-gate half
// of aae-orc-6kgq. shiftLaunch counted new-generation rows in any state
// against the replica count, so a Failed successor satisfied the launch and
// suppressed the live replacement that should have taken its place. The gate
// must count only live rows (api.SessionState.CountsAsAlive), exactly as
// reconcileRoleAt already does on the non-generation query.
func TestShiftLaunchDoesNotCountFailedNewGenTowardLaunch(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-6kgq-create", "squad", []api.Role{
		{Name: "worker", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	teamKey := "test-6kgq-create/squad"

	// A single dead new-generation row, no live successor. Before the fix
	// len(newGen)==1 satisfies replicas=1 and nothing is spawned.
	enterLaunchingShift(t, store, teamKey, "worker")
	seedSession(t, store, api.Session{Name: "squad-worker-g2-0", Workspace: "test-6kgq-create", Team: "squad", Role: "worker", Generation: 2, State: api.SessionFailed})

	ctrl.ReconcileOnce()

	gen2 := store.ListSessionsByTeamRoleGeneration("test-6kgq-create", "squad", "worker", 2)
	if api.CountAlive(gen2) < 1 {
		t.Fatalf("no live gen-2 successor spawned: the Failed row suppressed the replacement (alive=%d of %d rows)",
			api.CountAlive(gen2), len(gen2))
	}
}

// TestShiftLaunchDeadNewGenRowDoesNotBlockDrainGate is the ready-gate half of
// aae-orc-6kgq. With the live successors already at replica count, a leftover
// dead new-generation row must not keep the launch from advancing to drain.
// Feeding allReady the unfiltered slice let the Failed row fail the readiness
// check and hold the shift in launching forever.
func TestShiftLaunchDeadNewGenRowDoesNotBlockDrainGate(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-6kgq-drain", "squad", []api.Role{
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	teamKey := "test-6kgq-drain/squad"

	enterLaunchingShift(t, store, teamKey, "worker")
	// Two live successors satisfy replicas=2; one extra dead row must not
	// block the gate.
	seedSession(t, store, api.Session{Name: "squad-worker-g2-0", Workspace: "test-6kgq-drain", Team: "squad", Role: "worker", Generation: 2, State: api.SessionRunning})
	seedSession(t, store, api.Session{Name: "squad-worker-g2-1", Workspace: "test-6kgq-drain", Team: "squad", Role: "worker", Generation: 2, State: api.SessionRunning})
	seedSession(t, store, api.Session{Name: "squad-worker-g2-2", Workspace: "test-6kgq-drain", Team: "squad", Role: "worker", Generation: 2, State: api.SessionFailed})

	ctrl.ReconcileOnce()

	team, _ := store.GetTeam(teamKey)
	if team.Shift.Phase != api.ShiftDraining {
		t.Fatalf("phase = %s, want draining: two live successors are ready, the dead row must not block the gate",
			team.Shift.Phase)
	}
}

// TestReapAccountingEmitsOneEventPerRolePerTick is aae-orc-m8n0. The reap
// path charges a role once per tick however many replicas it lost, but that
// accounting was log-only: killing a three-replica role's tmux session put
// three session.crashed on the ring and nothing explaining that they were
// charged once, suppressed twice, and cooled into a backoff window. The reap
// path must reach event parity with the health path — one KindCrashLoopBackoff
// per charged role per tick, carrying the charge/suppress counts.
func TestReapAccountingEmitsOneEventPerRolePerTick(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	// Both producers share one ring, as the daemon wires them: ReapDead
	// emits session.crashed through the manager, the accounting through the
	// controller.
	ring := events.NewRing(0)
	ctrl.Events = ring
	sessMgr.Events = ring

	clock := newTestClock(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	ws := "test-m8n0"
	createTeamFixture(t, store, ws, "squad", []api.Role{
		{Name: "worker", Replicas: 3, RestartPolicy: api.RestartAlways, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole(ws, "squad", "worker")); got != 3 {
		t.Fatalf("expected 3 sessions, got %d", got)
	}

	// Take all three panes down together, then reap them in one tick.
	killWorkspaceAndWait(t, ws)
	ctrl.ReconcileOnce()

	crashed := ring.Snapshot(events.Filter{Kind: events.KindSessionCrashed}, 0)
	if len(crashed) != 3 {
		t.Fatalf("session.crashed events = %d, want 3 (one per lost replica)", len(crashed))
	}

	accounting := ring.Snapshot(events.Filter{Kind: events.KindCrashLoopBackoff}, 0)
	if len(accounting) != 1 {
		t.Fatalf("crash-accounting events = %d, want exactly 1 for the role (charged once, suppressed twice)", len(accounting))
	}
	ev := accounting[0]
	if ev.Workspace != ws || ev.Team != "squad" || ev.Role != "worker" {
		t.Errorf("event scope = %s/%s role %s, want %s/squad role worker", ev.Workspace, ev.Team, ev.Role, ws)
	}
	if ev.Severity != events.SeverityWarning {
		t.Errorf("severity = %s, want %s", ev.Severity, events.SeverityWarning)
	}
	if !strings.Contains(ev.Message, "charged 1") || !strings.Contains(ev.Message, "suppressed 2") {
		t.Errorf("message %q does not report charged 1 / suppressed 2", ev.Message)
	}
}

// TestShiftDrainEmptyOldGenerationEmitsDistinctEvent is aae-orc-094e.
// shiftDrain read an empty old generation as a successful drain and advanced
// silently, unable to tell "finished draining N sessions" from "advanced
// through a role it never moved" (a mis-tag, an early delete, or an
// intentional scale-up). A per-role drained counter distinguishes the two:
// when the shift reaches an empty old generation having drained nothing, it
// emits KindShiftDrainedEmpty and still advances.
func TestShiftDrainEmptyOldGenerationEmitsDistinctEvent(t *testing.T) {
	store := api.NewStore()
	ctrl := NewController(store, nil)
	ring := events.NewRing(0)
	ctrl.Events = ring

	createTeamFixture(t, store, "test-094e", "squad", []api.Role{
		{Name: "worker", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	teamKey := "test-094e/squad"

	// A shift already draining worker, with zero old-generation rows: the
	// old generation is empty from the start, so nothing is drained.
	if err := store.UpdateTeam(teamKey, func(live *api.Team) error {
		live.Generation = 2
		live.Shift = api.ShiftState{
			Phase:         api.ShiftDraining,
			OldGeneration: 1,
			Roles:         []string{"worker"},
			RoleIndex:     0,
			StartedAt:     time.Now().UTC(),
		}
		return nil
	}); err != nil {
		t.Fatalf("seed draining shift: %v", err)
	}

	team, _ := store.GetTeam(teamKey)
	ctrl.reconcileShift(&team)

	evs := ring.Snapshot(events.Filter{Kind: events.KindShiftDrainedEmpty}, 0)
	if len(evs) != 1 {
		t.Fatalf("KindShiftDrainedEmpty events = %d, want 1: the shift advanced through a role it never drained", len(evs))
	}
	if evs[0].Role != "worker" || evs[0].Workspace != "test-094e" || evs[0].Team != "squad" {
		t.Errorf("event scope = %s/%s role %s, want test-094e/squad role worker", evs[0].Workspace, evs[0].Team, evs[0].Role)
	}

	// Surface, don't judge: the shift still advances past the role.
	got, _ := store.GetTeam(teamKey)
	if got.Shift.RoleIndex != 1 {
		t.Errorf("RoleIndex = %d, want 1 (the shift must still advance)", got.Shift.RoleIndex)
	}
}

// TestProjectHeldRoleRowsSynthesizesCrashloopRow is aae-orc-prhx (and closes
// aae-orc-83m9). A single-replica restart_policy=always role crash-looping in
// its backoff window has no live session row — restartSession deletes it
// before setting the backoff — so it is absent from get sessions for the
// whole window and reads as never-declared. The read-path join synthesizes a
// crashloop-backoff row for such a held-down role, while roles that keep a
// real row (saturated/frozen) and gaps RoleHealth cannot explain get nothing.
func TestProjectHeldRoleRowsSynthesizesCrashloopRow(t *testing.T) {
	store := api.NewStore()
	ctrl := NewController(store, nil)
	clock := newTestClock(time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-prhx", "squad", []api.Role{
		{Name: "worker", Replicas: 1, RestartPolicy: api.RestartAlways, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "boss", Replicas: 1, RestartPolicy: api.RestartAlways, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "runner", Replicas: 1, RestartPolicy: api.RestartAlways, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	// worker: crash-looping, in its backoff window, zero live rows → held.
	rh := ctrl.getRoleHealth("test-prhx/squad/worker")
	rh.RestartCount = 2
	rh.BackoffUntil = clock.Now().Add(90 * time.Second)

	// boss: has RoleHealth but keeps a real (failed) row → representation
	// already present, must NOT be synthesized (the 83m9 saturated case).
	bh := ctrl.getRoleHealth("test-prhx/squad/boss")
	bh.RestartCount = 1
	bh.BackoffUntil = saturationFreezeUntil
	seedSession(t, store, api.Session{Name: "squad-boss-g1-0", Workspace: "test-prhx", Team: "squad", Role: "boss", Generation: 1, State: api.SessionFailed})

	// runner: below replicas with NO RoleHealth → a mid-flight spawn gap,
	// must NOT be synthesized.

	rows := ctrl.ProjectHeldRoleRows(store)

	byRole := map[string][]api.Session{}
	for _, r := range rows {
		byRole[r.Role] = append(byRole[r.Role], r)
	}
	if len(byRole["boss"]) != 0 {
		t.Errorf("boss synthesized %d row(s), want 0 (it keeps its real failed row)", len(byRole["boss"]))
	}
	if len(byRole["runner"]) != 0 {
		t.Errorf("runner synthesized %d row(s), want 0 (unexplained gap, reconciler will fill)", len(byRole["runner"]))
	}
	w := byRole["worker"]
	if len(w) != 1 {
		t.Fatalf("worker synthesized %d row(s), want 1 (held down in backoff, no live row)", len(w))
	}
	if w[0].State != api.SessionCrashLoopBackOff {
		t.Errorf("worker synthetic state = %s, want %s", w[0].State, api.SessionCrashLoopBackOff)
	}
	if w[0].Workspace != "test-prhx" || w[0].Team != "squad" {
		t.Errorf("worker synthetic scope = %s/%s, want test-prhx/squad", w[0].Workspace, w[0].Team)
	}
	if w[0].RestartCount != 2 {
		t.Errorf("worker synthetic RestartCount = %d, want 2", w[0].RestartCount)
	}
	if !strings.Contains(w[0].Reason, "backoff until") {
		t.Errorf("worker synthetic Reason = %q, want it to carry the backoff-until deadline", w[0].Reason)
	}
}
