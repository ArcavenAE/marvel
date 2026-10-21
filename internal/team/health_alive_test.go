package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// healthAfterOneTick seeds one running session for a role with the given
// check, runs one health evaluation, and returns the session as stored.
func healthAfterOneTick(t *testing.T, check *api.HealthCheck, lastHeartbeat time.Time) api.Session {
	t.Helper()
	store := api.NewStore()
	ctrl := NewController(store, nil)
	createTeamFixture(t, store, "test-alive", "squad", []api.Role{{
		Name: "worker", Replicas: 1, RestartPolicy: api.RestartAlways,
		Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		HealthCheck: check,
	}})
	sess := api.Session{
		Name: "squad-worker-g1-0", Workspace: "test-alive", Team: "squad", Role: "worker",
		Generation: 1, State: api.SessionRunning, LastHeartbeat: lastHeartbeat,
	}
	seedSession(t, store, sess)
	ctrl.evaluateHealth()
	got, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatalf("session after evaluateHealth: %v", err)
	}
	return got
}

// A process-alive check runs nothing: the controller sees a running session
// whose pane exists and nothing more (ReapDead owns the pane's absence). That
// is liveness, so it reads alive. healthy is kept for a check that ran and
// passed (party-t1-1).
func TestProcessAliveRoleReadsAliveNotHealthy(t *testing.T) {
	got := healthAfterOneTick(t, &api.HealthCheck{Type: api.HealthCheckProcessAlive}, time.Time{})
	if got.HealthState != api.HealthAlive {
		t.Errorf("HealthState = %q for a process-alive role, want %q: no check beyond the pane ran", got.HealthState, api.HealthAlive)
	}
	if got.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", got.FailureCount)
	}
}

// A heartbeat inside its timeout is a check that ran and passed: healthy.
func TestPassedHeartbeatStillReadsHealthy(t *testing.T) {
	check := &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: time.Hour, FailureThreshold: 3}
	got := healthAfterOneTick(t, check, time.Now().UTC())
	if got.HealthState != api.HealthHealthy {
		t.Errorf("HealthState = %q after a fresh heartbeat, want %q", got.HealthState, api.HealthHealthy)
	}
}

// No check declared: marvel asserts nothing, as before.
func TestNoHealthCheckStillReadsUnknown(t *testing.T) {
	got := healthAfterOneTick(t, nil, time.Time{})
	if got.HealthState != api.HealthUnknown {
		t.Errorf("HealthState = %q with no health check, want %q", got.HealthState, api.HealthUnknown)
	}
}

// Restart-count decay counted a process-alive role's replicas as healthy
// before alive existed. Renaming the state must not change that: a role on
// process-alive that stops crashing still has its RestartCount decay after
// restartCountDecayWindow (aae-orc-fv3h), or its backoff grows for good.
func TestRestartCountDecaysForAProcessAliveRole(t *testing.T) {
	store := api.NewStore()
	ctrl := NewController(store, nil)
	clock := newTestClock(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-alive-decay", "squad", []api.Role{{
		Name: "worker", Replicas: 1, RestartPolicy: api.RestartAlways,
		Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		HealthCheck: &api.HealthCheck{Type: api.HealthCheckProcessAlive},
	}})
	rh := ctrl.getRoleHealth("test-alive-decay/squad/worker")
	rh.RestartCount = 3
	rh.BackoffUntil = clock.Now().Add(-time.Minute)
	seedSession(t, store, api.Session{
		Name: "squad-worker-g1-0", Workspace: "test-alive-decay", Team: "squad", Role: "worker",
		Generation: 1, State: api.SessionRunning,
	})

	ctrl.evaluateHealth()
	clock.Advance(restartCountDecayWindow + time.Minute)
	ctrl.evaluateHealth()

	got, ok := ctrl.RoleHealthSnapshot("test-alive-decay", "squad", "worker")
	if !ok {
		t.Fatal("role health vanished, want a zeroed record")
	}
	if got.RestartCount != 0 || !got.BackoffUntil.IsZero() {
		t.Errorf("after a full window alive: RestartCount=%d BackoffUntil=%v, want 0 and zero (decayed as before)",
			got.RestartCount, got.BackoffUntil)
	}
}
