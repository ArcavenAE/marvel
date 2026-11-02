package team

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#1214: a health tick writes the sessions it changed in one bbolt
// transaction, and nothing when it changed nothing. No tmux is needed: the
// seats are store records with no pane, and evaluateHealth reads only the store.
func TestHealthTickWritesChangedSeatsInOneTransaction(t *testing.T) {
	const seats = 20
	store := api.NewStore()
	if err := store.OpenBolt(filepath.Join(t.TempDir(), "marvel.bolt")); err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	t.Cleanup(func() { _ = store.CloseBolt() })
	ctrl := NewController(store, nil)
	ws := "test-health-write"
	createTeamFixture(t, store, ws, "fleet", []api.Role{{
		Name: "worker", Replicas: seats,
		Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"600"}},
		HealthCheck: &api.HealthCheck{Type: api.HealthCheckProcessAlive},
	}})
	keys := make([]string, 0, seats)
	for i := 0; i < seats; i++ {
		sess := &api.Session{
			Name: fmt.Sprintf("fleet-worker-g1-%d", i), Workspace: ws, Team: "fleet", Role: "worker",
			State: api.SessionRunning, PaneID: fmt.Sprintf("%%%d", i), CreatedAt: time.Now().UTC(),
			Runtime: api.Runtime{Name: "sleep", Command: "sleep"},
		}
		if err := store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, sess.Key())
	}

	// First tick: every seat's HealthState changes (unknown to alive).
	u0, v0 := store.BoltUpdates(), store.ResourceVersion()
	ctrl.evaluateHealth()
	if n := store.BoltUpdates() - u0; n != 1 {
		t.Errorf("the first tick over %d changed seats started %d transactions, want 1", seats, n)
	}
	if n := store.ResourceVersion() - v0; n != 1 {
		t.Errorf("the first tick advanced the version by %d, want 1", n)
	}

	// Second tick: nothing changed.
	u1 := store.BoltUpdates()
	ctrl.evaluateHealth()
	if n := store.BoltUpdates() - u1; n != 0 {
		t.Errorf("a tick that changed nothing started %d transactions, want 0", n)
	}

	// A memory-only reading changes for every seat, as a stream or heartbeat
	// feed does between ticks: the next tick carries all of them in one write.
	for i, k := range keys {
		store.UpdateSessionContext(k, api.SessionContext{ContextSource: api.ContextSourceHeartbeat, ContextPercent: float64(10 + i)})
	}
	u2 := store.BoltUpdates()
	ctrl.evaluateHealth()
	if n := store.BoltUpdates() - u2; n != 1 {
		t.Errorf("a tick carrying %d memory-only changes started %d transactions, want 1", seats, n)
	}
}
