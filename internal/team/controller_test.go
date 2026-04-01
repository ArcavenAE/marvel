package team

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
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

func createTeamFixture(t *testing.T, store *api.Store, wsName, teamName string, roles []api.Role) *api.Team {
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
	return team
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

	team := createTeamFixture(t, store, "test-scaledown", "agents", []api.Role{
		{Name: "worker", Replicas: 3, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()
	if len(store.ListSessionsByTeamRole("test-scaledown", "agents", "worker")) != 3 {
		t.Fatal("expected 3 sessions after scale up")
	}

	team.Roles[0].Replicas = 1
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

	team := createTeamFixture(t, store, "test-multi", "squad", []api.Role{
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

	team.Roles[1].Replicas = 1
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

	team := createTeamFixture(t, store, "test-shift", "squad", []api.Role{
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	// Initial reconcile creates gen-1 sessions.
	ctrl.ReconcileOnce()
	gen1 := store.ListSessionsByTeamRoleGeneration("test-shift", "squad", "worker", 1)
	if len(gen1) != 2 {
		t.Fatalf("expected 2 gen-1 sessions, got %d", len(gen1))
	}

	// Initiate shift.
	if err := ctrl.InitiateShift("test-shift/squad", ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	if team.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", team.Generation)
	}
	if team.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("expected launching, got %s", team.Shift.Phase)
	}

	// Run reconcile ticks until shift completes.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}
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

	team := createTeamFixture(t, store, "test-shift-multi", "squad", []api.Role{
		{Name: "supervisor", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()

	// Initiate shift — supervisor should shift last.
	if err := ctrl.InitiateShift("test-shift-multi/squad", ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	if team.Shift.Roles[0] != "worker" {
		t.Fatalf("expected worker to shift first, got %s", team.Shift.Roles[0])
	}
	if team.Shift.Roles[1] != "supervisor" {
		t.Fatalf("expected supervisor to shift last, got %s", team.Shift.Roles[1])
	}

	// Run reconcile ticks until shift completes.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}

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

	team := createTeamFixture(t, store, "test-shift-single", "squad", []api.Role{
		{Name: "supervisor", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})

	ctrl.ReconcileOnce()

	// Shift only workers.
	if err := ctrl.InitiateShift("test-shift-single/squad", "worker"); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}

	if len(team.Shift.Roles) != 1 {
		t.Fatalf("expected 1 role in shift, got %d", len(team.Shift.Roles))
	}

	// Run ticks until complete.
	for i := 0; i < 20; i++ {
		ctrl.ReconcileOnce()
		if team.Shift.Phase == api.ShiftNone {
			break
		}
	}

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

	// Simulate a fresh heartbeat.
	sess := sessions[0]
	sess.LastHeartbeat = time.Now().UTC()

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
	sess.LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)

	// First eval: failure count 1 (below threshold of 2).
	ctrl.ReconcileOnce()
	sess, _ = store.GetSession(sess.Key())
	if sess == nil {
		t.Fatal("session should still exist (restart_policy=never)")
	}
	if sess.FailureCount != 1 {
		t.Fatalf("expected 1 failure after first eval, got %d", sess.FailureCount)
	}

	// Second eval: failure count 2 (meets threshold).
	ctrl.ReconcileOnce()
	sess, _ = store.GetSession(sess.Key())
	if sess == nil {
		t.Fatal("session should still exist (restart_policy=never)")
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

	sess, _ := store.GetSession(sessions[0].Key())
	if sess == nil {
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
	sessions[0].LastHeartbeat = time.Now().UTC().Add(-1 * time.Hour)

	// Eval + reconcile: unhealthy → delete → reconciler recreates.
	ctrl.ReconcileOnce()

	sessions = store.ListSessionsByTeamRole("test-health-restart", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after restart, got %d", len(sessions))
	}
	// Recreated session has a fresh CreatedAt.
	if !sessions[0].CreatedAt.After(origCreatedAt) {
		t.Fatal("expected new session with later CreatedAt after restart")
	}
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
