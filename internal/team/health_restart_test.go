package team

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#1214: the health tick no longer persists a change of LastHealthCheck
// alone, so the file can hold a LastHealthCheck older than memory did. A seat
// with a fresh heartbeat and no other change must still read healthy after a
// restart, because the verdict rests on LastHeartbeat, HealthState and
// FailureCount, which are all written when they change.
func TestHeartbeatSeatStaysHealthyAcrossARestartWithAStaleLastHealthCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	store := api.NewStore()
	if err := store.OpenBolt(path); err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	ctrl := NewController(store, nil)
	ws := "test-restart-lhc"
	createTeamFixture(t, store, ws, "squad", []api.Role{{
		Name: "worker", Replicas: 1,
		Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"600"}},
		HealthCheck: &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: time.Hour, FailureThreshold: 3},
	}})
	sess := &api.Session{
		Name: "squad-worker-g1-0", Workspace: ws, Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%1", CreatedAt: time.Now().UTC().Add(-time.Minute),
		Runtime: api.Runtime{Name: "sleep", Command: "sleep"},
	}
	if err := store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Two ticks. The first sets HealthHealthy (a real change, written). The
	// second changes LastHealthCheck only, which is not written.
	ctrl.evaluateHealth()
	u0 := store.BoltUpdates()
	ctrl.evaluateHealth()
	if got := store.BoltUpdates() - u0; got != 0 {
		t.Fatalf("a health tick that changed only LastHealthCheck started %d transactions, want 0", got)
	}
	inMemory, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	// The restart: a new store on the same file, and a controller over it.
	store2 := api.NewStore()
	if err := store2.OpenBolt(path); err != nil {
		t.Fatalf("reopen bolt: %v", err)
	}
	t.Cleanup(func() { _ = store2.CloseBolt() })
	reread, err := store2.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if !reread.LastHealthCheck.Before(inMemory.LastHealthCheck) {
		t.Fatalf("the file holds LastHealthCheck %v, not older than memory's %v; the test is not exercising a stale value",
			reread.LastHealthCheck, inMemory.LastHealthCheck)
	}
	NewController(store2, nil).evaluateHealth()
	after, err := store2.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if after.HealthState != api.HealthHealthy || after.FailureCount != 0 {
		t.Errorf("after a restart the seat reads health %q with %d failures, want healthy and 0", after.HealthState, after.FailureCount)
	}
}
