package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/suspend"
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

// A pause longer than the heartbeat timeout does not fail the first health tick
// once the shift has been applied: the seat reads healthy with no failures,
// where the same stamp unshifted would count a failure and, at the threshold,
// order a restart. The shift itself is suspend.ShiftedHeartbeat, applied by the
// daemon at resume to every running session.
func TestAPauseLongerThanTheTimeoutDoesNotFailTheFirstTickAfterTheShift(t *testing.T) {
	check := &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 5 * time.Minute, FailureThreshold: 2}
	now := time.Now().UTC()
	pause := 90 * time.Minute
	// Heartbeated a minute before the suspend began, frozen for the pause.
	last := now.Add(-pause - time.Minute)

	shifted := healthAfterOneTick(t, check, suspend.ShiftedHeartbeat(last, pause, now))
	if shifted.FailureCount != 0 || shifted.HealthState != api.HealthHealthy {
		t.Errorf("after the shift: FailureCount %d, health %q; want 0 and healthy", shifted.FailureCount, shifted.HealthState)
	}
	unshifted := healthAfterOneTick(t, check, last)
	if unshifted.FailureCount == 0 {
		t.Error("the control, the same stamp unshifted, counted no failure: the test proves nothing")
	}
}
