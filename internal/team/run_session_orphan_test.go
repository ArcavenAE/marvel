package team

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A session made by `marvel run` into a declared team under a role the team
// does not declare is the operator's, not the manifest's. The orphan sweep
// drains sessions of a role removed from a manifest; it must not read a run
// session as one. A run session carries no generation, which is how the
// controller tells it from a session it spawned.
// Falsification: without the generation test in reconcileOrphanedSessions,
// the run session is deleted on the first tick and the count below is 0.
func TestRunSessionOfAnUndeclaredRoleSurvivesTheOrphanSweep(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-runorphan", "squad", []api.Role{
		{Name: "a", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	})
	ctrl.ReconcileOnce()

	run := &api.Session{
		Name: "b1", Workspace: "test-runorphan", Team: "squad", Role: "branch-run",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := sessMgr.Create(run); err != nil {
		t.Fatalf("create run session: %v", err)
	}
	for i := 0; i < 10; i++ {
		ctrl.ReconcileOnce()
	}

	if got := len(store.ListSessionsByTeamRole("test-runorphan", "squad", "branch-run")); got != 1 {
		t.Fatalf("run sessions after ten ticks = %d, want 1", got)
	}
	if got := len(store.ListSessionsByTeamRole("test-runorphan", "squad", "a")); got != 1 {
		t.Fatalf("declared role a sessions = %d, want 1", got)
	}
}
