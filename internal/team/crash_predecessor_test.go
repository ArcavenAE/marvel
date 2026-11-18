package team

import (
	"reflect"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Crash repair names the seat it replaces (marvel#541, marvel#1402). The
// reconcile spawn loop clears a role's Crashed rows just before it builds the
// replacement, so the keys have to be read first. crashedPredecessors is that
// read: the keys of the role's Crashed seats of one generation, in index order,
// minus any a live seat already names. These tests drive it on a store with no
// tmux, so they run wherever the package runs.

const crashWS = "crashws"

func crashRole(replicas int) api.Role {
	return api.Role{
		Name: "writer", Replicas: replicas,
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
}

func crashEnv(t *testing.T, replicas int) (*api.Store, *Controller, *api.Team) {
	t.Helper()
	store := api.NewStore()
	ctrl := NewController(store, nil)
	createTeamFixture(t, store, crashWS, "ops", []api.Role{crashRole(replicas)})
	got, err := store.GetTeam(crashWS + "/ops")
	if err != nil {
		t.Fatal(err)
	}
	return store, ctrl, &got
}

func crashRow(t *testing.T, store *api.Store, name string, gen int64, state api.SessionState, predecessor string) string {
	t.Helper()
	sess := api.Session{
		Name: name, Workspace: crashWS, Team: "ops", Role: "writer",
		Generation: gen, State: state, Predecessor: predecessor,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSession(&sess); err != nil {
		t.Fatal(err)
	}
	return sess.Key()
}

func wantPredecessors(t *testing.T, got, want []string, when string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: predecessors = %v, want %v", when, got, want)
	}
}

// TestCrashRepairReplacementNamesItsPredecessor: one replica crashed, so the
// replacement names that seat by its key, not by its index or its name.
func TestCrashRepairReplacementNamesItsPredecessor(t *testing.T) {
	store, ctrl, team := crashEnv(t, 1)
	crashed := crashRow(t, store, "ops-writer-g1-0", 1, api.SessionCrashed, "")

	got := ctrl.crashedPredecessors(team, "writer", team.Generation)
	wantPredecessors(t, got, []string{crashed}, "one crashed seat")
	if len(got) == 1 && got[0] != crashWS+"/ops-writer-g1-0" {
		t.Errorf("predecessor = %q, want the workspace/session key %q", got[0], crashWS+"/ops-writer-g1-0")
	}

	// Two crashed seats come back in numeric index order, so the first
	// replacement takes the lower index whichever order the store lists them
	// in: by key, "g1-10" sorts before "g1-2".
	store2, ctrl2, team2 := crashEnv(t, 3)
	second := crashRow(t, store2, "ops-writer-g1-10", 1, api.SessionCrashed, "")
	first := crashRow(t, store2, "ops-writer-g1-2", 1, api.SessionCrashed, "")
	wantPredecessors(t, ctrl2.crashedPredecessors(team2, "writer", team2.Generation), []string{first, second}, "two crashed seats")
}

// TestCrashRepairAtReplicasTwoNamesPredecessorAcrossIndex: the replacement's own
// index is the highest live one plus one, so it never matches the crashed
// seat's. The live replica is not named, and a crashed seat of another
// generation or another role is left alone.
func TestCrashRepairAtReplicasTwoNamesPredecessorAcrossIndex(t *testing.T) {
	store, ctrl, team := crashEnv(t, 2)
	crashRow(t, store, "ops-writer-g1-0", 1, api.SessionRunning, "")
	crashed := crashRow(t, store, "ops-writer-g1-1", 1, api.SessionCrashed, "")
	crashRow(t, store, "ops-writer-g0-3", 0, api.SessionCrashed, "")
	other := api.Session{
		Name: "ops-reader-g1-0", Workspace: crashWS, Team: "ops", Role: "reader",
		Generation: 1, State: api.SessionCrashed, CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSession(&other); err != nil {
		t.Fatal(err)
	}

	got := ctrl.crashedPredecessors(team, "writer", team.Generation)
	wantPredecessors(t, got, []string{crashed}, "replicas 2, one live and one crashed")
}

// TestCrashRepairSkipsPredecessorStillAlive: a key a live seat already names is
// spent, as a wake spends it, so a second replacement does not claim it.
func TestCrashRepairSkipsPredecessorStillAlive(t *testing.T) {
	store, ctrl, team := crashEnv(t, 2)
	named := crashRow(t, store, "ops-writer-g1-0", 1, api.SessionCrashed, "")
	free := crashRow(t, store, "ops-writer-g1-1", 1, api.SessionCrashed, "")
	crashRow(t, store, "ops-writer-g1-2", 1, api.SessionRunning, named)

	got := ctrl.crashedPredecessors(team, "writer", team.Generation)
	wantPredecessors(t, got, []string{free}, "one crashed key already named by a live seat")

	// A spawn with no crashed seat to pair, a scale-up, gets nothing.
	store3, ctrl3, team3 := crashEnv(t, 2)
	crashRow(t, store3, "ops-writer-g1-0", 1, api.SessionRunning, "")
	if got := ctrl3.crashedPredecessors(team3, "writer", team3.Generation); len(got) != 0 {
		t.Errorf("no crashed seat: predecessors = %v, want none", got)
	}
}

// TestCrashRepairSpawnCarriesThePredecessor: the whole path. A pane dies, the
// reap marks the seat Crashed, and after the backoff the reconciler clears the
// marker and spawns the replacement. The replacement's row names the seat it
// replaces, which the adapter exports as MARVEL_PREDECESSOR. Reading the key
// after the clear, or not handing it to the spawn, leaves it empty.
func TestCrashRepairSpawnCarriesThePredecessor(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-crash-pred", "squad", []api.Role{
		{
			Name: "worker", Replicas: 1,
			Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		},
	})

	ctrl.ReconcileOnce()
	first := store.ListSessionsByTeamRole("test-crash-pred", "squad", "worker")
	if len(first) != 1 || first[0].Predecessor != "" {
		t.Fatalf("setup: first spawn = %+v, want one seat with no predecessor", first)
	}
	killPaneAndWait(t, first[0].PaneID)
	ctrl.ReconcileOnce() // reap: the row stays as a Crashed marker
	clock.Advance(90 * time.Second)
	ctrl.ReconcileOnce() // backoff elapsed: clear the marker, spawn the replacement

	got := store.ListSessionsByTeamRole("test-crash-pred", "squad", "worker")
	if len(got) != 1 || got[0].State != api.SessionRunning {
		t.Fatalf("after repair: sessions = %+v, want one Running replacement", got)
	}
	if got[0].Predecessor != first[0].Key() {
		t.Errorf("replacement predecessor = %q, want the crashed seat %q", got[0].Predecessor, first[0].Key())
	}
}
