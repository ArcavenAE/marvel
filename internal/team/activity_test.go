package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// headlessRuntime and feedRuntime build the two interactive/headless runtime
// shapes the observability gate keys on, so the table rows read as intent.
func headlessRuntime() api.Runtime {
	return api.Runtime{Name: "claude", Command: "claude", Mode: api.RuntimeModeHeadless}
}

func statuslineRuntime() api.Runtime {
	return api.Runtime{Name: "claude", Command: "claude", ContextFeed: api.ContextFeedStatusline}
}

func bareInteractiveRuntime() api.Runtime {
	return api.Runtime{Name: "claude", Command: "claude"}
}

// TestEvaluateActivity is the whole truth table for the restart-neutral
// staleness advisory (aae-orc-9box). It is a pure-function test: no tmux, no
// store, no clock injection — every input is explicit, so the false-positive
// guards are asserted directly rather than inferred from an integration run.
func TestEvaluateActivity(t *testing.T) {
	now := time.Now().UTC()
	const window = 10 * time.Minute

	heartbeatCheck := &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 30 * time.Second, FailureThreshold: 3}
	processAliveCheck := &api.HealthCheck{Type: api.HealthCheckProcessAlive}

	tests := []struct {
		name    string
		timeout time.Duration
		runtime api.Runtime
		check   *api.HealthCheck
		context time.Time // ContextAt; zero == never observed
		created time.Time
		want    api.ActivityState
	}{
		{
			name:    "opt-out: zero timeout disables the signal even when clearly stale",
			timeout: 0,
			runtime: headlessRuntime(),
			context: now.Add(-1 * time.Hour),
			created: now.Add(-2 * time.Hour),
			want:    api.ActivityUnknown,
		},
		{
			name:    "active: activity within the window",
			timeout: window,
			runtime: headlessRuntime(),
			context: now.Add(-1 * time.Minute),
			created: now.Add(-1 * time.Hour),
			want:    api.ActivityActive,
		},
		{
			name:    "active: activity exactly at the window boundary is not yet stale",
			timeout: window,
			runtime: headlessRuntime(),
			context: now.Add(-window),
			created: now.Add(-1 * time.Hour),
			want:    api.ActivityActive,
		},
		{
			name:    "stalled: worked, then went silent past the window (expired-creds / hung harness)",
			timeout: window,
			runtime: headlessRuntime(),
			context: now.Add(-20 * time.Minute),
			created: now.Add(-1 * time.Hour),
			want:    api.ActivityStalled,
		},
		{
			name:    "stalled-from-birth: never observed, headless channel, past the startup grace (dead-at-login)",
			timeout: window,
			runtime: headlessRuntime(),
			context: time.Time{},
			created: now.Add(-20 * time.Minute),
			want:    api.ActivityStalled,
		},
		{
			name:    "grace: never observed, headless channel, still inside the startup grace",
			timeout: window,
			runtime: headlessRuntime(),
			context: time.Time{},
			created: now.Add(-1 * time.Minute),
			want:    api.ActivityUnknown,
		},
		{
			name:    "stalled-from-birth: statusline feed is an activity channel",
			timeout: window,
			runtime: statuslineRuntime(),
			context: time.Time{},
			created: now.Add(-20 * time.Minute),
			want:    api.ActivityStalled,
		},
		{
			name:    "stalled-from-birth: a heartbeat healthcheck is an activity channel",
			timeout: window,
			runtime: bareInteractiveRuntime(),
			check:   heartbeatCheck,
			context: time.Time{},
			created: now.Add(-20 * time.Minute),
			want:    api.ActivityStalled,
		},
		{
			// The load-bearing false-positive guard: a working interactive
			// session with no cooperative feed and no heartbeat check produces
			// nothing marvel observes, so a zero ContextAt means "unseen", not
			// "idle". It must NEVER be flagged stalled.
			name:    "guard: never observed, no activity channel, past grace → Unknown not Stalled",
			timeout: window,
			runtime: bareInteractiveRuntime(),
			context: time.Time{},
			created: now.Add(-1 * time.Hour),
			want:    api.ActivityUnknown,
		},
		{
			// process-alive is a liveness check, not an activity channel: a
			// dead-at-login process is still process-alive. It must not gate
			// the never-observed branch into Stalled.
			name:    "guard: process-alive healthcheck is not an activity channel",
			timeout: window,
			runtime: bareInteractiveRuntime(),
			check:   processAliveCheck,
			context: time.Time{},
			created: now.Add(-1 * time.Hour),
			want:    api.ActivityUnknown,
		},
		{
			// Once ContextAt is set the reading is channel-agnostic: marvel
			// demonstrably saw work, so a later silence is assessable even for
			// a session whose role declares no channel.
			name:    "observed-then-stale is assessable regardless of declared channel",
			timeout: window,
			runtime: bareInteractiveRuntime(),
			context: now.Add(-30 * time.Minute),
			created: now.Add(-2 * time.Hour),
			want:    api.ActivityStalled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &api.Session{
				Runtime:   tt.runtime,
				CreatedAt: tt.created,
			}
			s.ContextAt = tt.context
			role := &api.Role{
				Name:            "worker",
				ActivityTimeout: tt.timeout,
				HealthCheck:     tt.check,
			}
			got := evaluateActivity(s, role, now)
			if got != tt.want {
				t.Fatalf("evaluateActivity = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEvaluateActivityNilRole guards the nil-role path (a session whose role
// was removed from the manifest between snapshot and evaluation).
func TestEvaluateActivityNilRole(t *testing.T) {
	now := time.Now().UTC()
	s := &api.Session{Runtime: headlessRuntime(), CreatedAt: now.Add(-1 * time.Hour)}
	if got := evaluateActivity(s, nil, now); got != api.ActivityUnknown {
		t.Fatalf("nil role: evaluateActivity = %q, want %q", got, api.ActivityUnknown)
	}
}

// TestActivityObservable pins the channel gate the never-observed branch
// depends on.
func TestActivityObservable(t *testing.T) {
	heartbeatCheck := &api.HealthCheck{Type: api.HealthCheckHeartbeat}
	processAliveCheck := &api.HealthCheck{Type: api.HealthCheckProcessAlive}

	tests := []struct {
		name    string
		runtime api.Runtime
		check   *api.HealthCheck
		want    bool
	}{
		{"headless stream", headlessRuntime(), nil, true},
		{"statusline context feed", statuslineRuntime(), nil, true},
		{"heartbeat healthcheck", bareInteractiveRuntime(), heartbeatCheck, true},
		{"bare interactive, no channel", bareInteractiveRuntime(), nil, false},
		{"process-alive is not a channel", bareInteractiveRuntime(), processAliveCheck, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &api.Session{Runtime: tt.runtime}
			role := &api.Role{HealthCheck: tt.check}
			if got := activityObservable(s, role); got != tt.want {
				t.Fatalf("activityObservable = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestActivityStalledNeverRestarts is the guarantee test: a session flagged
// ActivityStalled is NOT restarted and its liveness/restart bookkeeping is
// untouched. The role uses a process-alive healthcheck so the HEALTH axis
// never restarts on its own — any restart would therefore have to come from
// the activity axis, and the assertion is that none does.
func TestActivityStalledNeverRestarts(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-activity-norestart", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime:         api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			RestartPolicy:   api.RestartAlways,
			HealthCheck:     &api.HealthCheck{Type: api.HealthCheckProcessAlive},
			ActivityTimeout: 30 * time.Second,
		},
	})

	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-activity-norestart", "squad", "worker")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	sess := sessions[0]

	// Inject a stale activity timestamp: marvel last saw work an hour ago.
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.ContextAt = time.Now().UTC().Add(-1 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("inject stale context: %v", err)
	}

	// Two health evaluations: a stalled session must stay put across ticks,
	// not accumulate toward any threshold.
	ctrl.evaluateHealth()
	ctrl.evaluateHealth()

	got, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatalf("session must still exist — a stalled session is never restarted: %v", err)
	}
	if got.ActivityState != api.ActivityStalled {
		t.Fatalf("expected ActivityStalled, got %q", got.ActivityState)
	}
	// Liveness is unaffected: the process is alive, the pane exists.
	if got.HealthState != api.HealthHealthy {
		t.Fatalf("expected HealthHealthy (liveness intact), got %q", got.HealthState)
	}
	if got.State != api.SessionRunning {
		t.Fatalf("expected SessionRunning (no restart), got %q", got.State)
	}
	if got.FailureCount != 0 {
		t.Fatalf("activity staleness must not charge FailureCount, got %d", got.FailureCount)
	}
	if got.RestartCount != 0 {
		t.Fatalf("activity staleness must not trigger a restart, RestartCount = %d", got.RestartCount)
	}
}

// TestActivityAdvisoryThroughEvaluateHealth exercises the active and opt-out
// paths end to end through evaluateHealth, so the wiring (closure sets
// ActivityState on every branch) is covered alongside the pure function.
func TestActivityAdvisoryThroughEvaluateHealth(t *testing.T) {
	skipIfNoTmux(t)

	t.Run("fresh activity reads active", func(t *testing.T) {
		store, _, ctrl, cleanup := setup(t)
		t.Cleanup(cleanup)
		createTeamFixture(t, store, "test-activity-active", "squad", []api.Role{
			{
				Name: "worker", Replicas: 1,
				Runtime:         api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
				HealthCheck:     &api.HealthCheck{Type: api.HealthCheckProcessAlive},
				ActivityTimeout: 30 * time.Second,
			},
		})
		ctrl.ReconcileOnce()
		sess := store.ListSessionsByTeamRole("test-activity-active", "squad", "worker")[0]
		if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.ContextAt = time.Now().UTC()
			return nil
		}); err != nil {
			t.Fatalf("stamp fresh context: %v", err)
		}
		ctrl.evaluateHealth()
		got, _ := store.GetSession(sess.Key())
		if got.ActivityState != api.ActivityActive {
			t.Fatalf("expected ActivityActive, got %q", got.ActivityState)
		}
	})

	t.Run("no activity_timeout leaves the axis Unknown even when stale", func(t *testing.T) {
		store, _, ctrl, cleanup := setup(t)
		t.Cleanup(cleanup)
		createTeamFixture(t, store, "test-activity-optout", "squad", []api.Role{
			{
				Name: "worker", Replicas: 1,
				Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
				HealthCheck: &api.HealthCheck{Type: api.HealthCheckProcessAlive},
				// ActivityTimeout deliberately unset (0 == disabled).
			},
		})
		ctrl.ReconcileOnce()
		sess := store.ListSessionsByTeamRole("test-activity-optout", "squad", "worker")[0]
		if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.ContextAt = time.Now().UTC().Add(-1 * time.Hour)
			return nil
		}); err != nil {
			t.Fatalf("inject stale context: %v", err)
		}
		ctrl.evaluateHealth()
		got, _ := store.GetSession(sess.Key())
		if got.ActivityState != api.ActivityUnknown {
			t.Fatalf("opt-out role must stay ActivityUnknown, got %q", got.ActivityState)
		}
	})
}
