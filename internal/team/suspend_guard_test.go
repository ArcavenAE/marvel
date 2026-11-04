package team

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// While the cluster is suspended the reconcile tick does nothing: it neither
// reaps a pane that reads dead because tmux is stopped nor spawns a seat
// against a frozen server (docs/design/fleet-suspend-resume.md section 3).
func TestReconcileDoesNothingWhileSuspended(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-suspend-tick", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		},
	})

	suspended := true
	ctrl.Suspended = func() bool { return suspended }
	ctrl.ReconcileOnce()
	if got := store.ListSessionsByTeamRole("test-suspend-tick", "squad", "worker"); len(got) != 0 {
		t.Fatalf("a suspended tick spawned %d session(s), want none", len(got))
	}

	suspended = false
	ctrl.ReconcileOnce()
	if got := store.ListSessionsByTeamRole("test-suspend-tick", "squad", "worker"); len(got) != 1 {
		t.Fatalf("after the guard lifts the tick spawned %d session(s), want 1", len(got))
	}
}
