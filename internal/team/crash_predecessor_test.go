package team

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
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
	if len(got) == 0 && len(want) == 0 {
		return
	}
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

// TestWakeNamesTheHibernatedSeatNotACrashedMarker: hibernate leaves a Crashed
// marker where it finds one, so a wake can meet both a hibernated seat and a
// crashed one for the same role. The wake's predecessor is the hibernated seat,
// whose handoff the wake exists to resume; the crash lookup fills in only where
// the wake names none.
func TestWakeNamesTheHibernatedSeatNotACrashedMarker(t *testing.T) {
	store, ctrl, cleanup := setupWithBolt(t, filepath.Join(t.TempDir(), "marvel.db"))
	t.Cleanup(cleanup)
	f := &wakeFixture{t: t, store: store, ctrl: ctrl, clock: newTestClock(listEpoch), ring: events.NewRing(0), ws: "test-wake-vs-crash"}
	ctrl.now = f.clock.Now
	ctrl.Events = f.ring
	createTeamFixture(t, store, f.ws, "squad", wakeRoles())
	hibernated := f.ws + "/squad-supervisor-before"
	if err := store.UpdateTeam(f.ws+"/squad", func(live *api.Team) error {
		live.ConvergencePosture = api.PostureHold
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: f.clock.Now(), Mode: api.HibernateModeHandoff, Seats: []string{hibernated}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordResume(api.ResumeEntry{Session: hibernated, Workspace: f.ws, Team: "squad", Role: "supervisor", LaunchedAt: listEpoch.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	crashed := api.Session{
		Name: "squad-supervisor-g1-7", Workspace: f.ws, Team: "squad", Role: "supervisor",
		Generation: 1, State: api.SessionCrashed, CreatedAt: listEpoch.Add(-3 * time.Hour),
	}
	if err := store.CreateSession(&crashed); err != nil {
		t.Fatal(err)
	}

	f.wake()
	f.ctrl.ReconcileOnce()

	var live []api.Session
	for _, s := range store.ListSessionsByTeamRole(f.ws, "squad", "supervisor") {
		if s.State.CountsAsAlive() {
			live = append(live, s)
		}
	}
	if len(live) != 1 || live[0].Predecessor != hibernated {
		t.Fatalf("supervisor seats = %+v, want one whose predecessor is %s, not the crashed marker %s", live, hibernated, crashed.Key())
	}
}

// The Crashed rows are cleared in the tick that reads them, but a replacement
// may not be created in that tick: a view build defers it, Create refuses it,
// or plan.Spawn is smaller than the crashed count. The keys are kept until a
// created seat takes them (review of marvel#1493, Medium 1).
func TestCrashPredecessorsSurviveTheClearUntilASeatTakesThem(t *testing.T) {
	store, ctrl, team := crashEnv(t, 3)
	k0 := crashRow(t, store, "ops-writer-g1-0", 1, api.SessionCrashed, "")
	k2 := crashRow(t, store, "ops-writer-g1-2", 1, api.SessionCrashed, "")
	clear := func() {
		for _, s := range store.ListSessionsByTeamRole(crashWS, "ops", "writer") {
			if s.State == api.SessionCrashed {
				if err := store.DeleteSession(s.Key()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	// Tick 1 reads both, then the rows are cleared, and nothing is created.
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", team.Generation), []string{k0, k2}, "tick 1")
	clear()
	if rows := store.ListSessionsByTeamRole(crashWS, "ops", "writer"); len(rows) != 0 {
		t.Fatalf("setup: %d rows left after the clear", len(rows))
	}
	// Tick 2 finds no Crashed row and still has both keys.
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", team.Generation), []string{k0, k2}, "tick 2, rows gone")

	// A seat is created with the first key and the loop reports it taken: the
	// key goes, the second stays, whatever becomes of the seat's row afterwards.
	ctrl.crashPredecessorTaken(team, "writer", team.Generation, k0)
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", team.Generation), []string{k2}, "after the first is taken")

	// A key a live seat names is spent even if nobody reported it taken.
	crashRow(t, store, "ops-writer-g1-4", 1, api.SessionRunning, k2)
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", team.Generation), nil, "after the second is named by a live seat")

	// A later crash adds its key to the pending list in index order.
	k10 := crashRow(t, store, "ops-writer-g1-10", 1, api.SessionCrashed, "")
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", team.Generation), []string{k10}, "a new crash")
}

// A pending key belongs to one generation: after a shift the new generation's
// spawns never take a seat of the old one.
func TestCrashPredecessorsStayWithTheirGeneration(t *testing.T) {
	store, ctrl, team := crashEnv(t, 1)
	k := crashRow(t, store, "ops-writer-g1-0", 1, api.SessionCrashed, "")
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", 1), []string{k}, "generation 1")
	if got := ctrl.pendingCrashPredecessors(team, "writer", 2); len(got) != 0 {
		t.Errorf("generation 2 predecessors = %v, want none", got)
	}
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", 1), []string{k}, "generation 1 again")
}

// The whole path for a role with a view: the first Prepare of a seat's view
// starts the build and returns false, so the replacement is not created in the
// tick that clears the crashed row. The seat created on a later tick still
// names the crashed one (review of marvel#1493, Medium 1).
func TestCrashRepairDeferredByAViewBuildStillNamesTheSeat(t *testing.T) {
	// The remote hangs, so each seat's view build outlasts one tick and its spawn
	// waits until the 500ms spawn bound passes: a deferral that does not depend
	// on timing between two ticks.
	var hung atomic.Bool
	hung.Store(true)
	store, ctrl, keeper, _ := viewSpawnRig(t, spawnGit{hung: &hung}, 500*time.Millisecond)
	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	deadline := time.Now().Add(10 * time.Second)
	for len(teamSessions(store, "slow")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the seat with a view never spawned")
		}
		ctrl.ReconcileOnce()
		time.Sleep(50 * time.Millisecond)
	}
	first := teamSessions(store, "slow")[0]
	killPaneAndWait(t, first.PaneID)
	ctrl.ReconcileOnce() // reap: the row stays as a Crashed marker
	// The replacement has the crashed seat's name, so its view starts over.
	if err := keeper.Teardown(first.Key()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(90 * time.Second)

	var deferred bool
	var got []api.Session
	deadline = time.Now().Add(10 * time.Second)
	for {
		ctrl.ReconcileOnce()
		got = nil
		for _, s := range teamSessions(store, "slow") {
			if s.State.CountsAsAlive() {
				got = append(got, s)
			}
		}
		if len(got) > 0 {
			break
		}
		deferred = true
		if time.Now().After(deadline) {
			t.Fatal("the replacement never spawned")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !deferred {
		t.Fatal("setup: the replacement was created in the first tick, so the view build did not defer it")
	}
	if len(got) != 1 || got[0].Predecessor != first.Key() {
		names := make([]string, 0, len(got))
		for _, s := range got {
			names = append(names, s.Key()+" (predecessor "+s.Predecessor+")")
		}
		t.Errorf("replacements = %v, want one seat whose predecessor is the crashed %q", names, first.Key())
	}
}

// A wake that names fewer seats than it spawns fills the rest from the crashed
// markers of the role: the first seat names the hibernated one, the second the
// crashed one (review of marvel#1493, the wake and crash case).
func TestWakeFillsTheRemainingSeatsFromCrashedMarkers(t *testing.T) {
	store, ctrl, cleanup := setupWithBolt(t, filepath.Join(t.TempDir(), "marvel.db"))
	t.Cleanup(cleanup)
	f := &wakeFixture{t: t, store: store, ctrl: ctrl, clock: newTestClock(listEpoch), ring: events.NewRing(0), ws: "test-wake-fill"}
	ctrl.now = f.clock.Now
	ctrl.Events = f.ring
	roles := wakeRoles()
	roles[0].Replicas = 2 // two supervisor seats, one hibernated
	createTeamFixture(t, store, f.ws, "squad", roles)
	hibernated := f.ws + "/squad-supervisor-before"
	if err := store.UpdateTeam(f.ws+"/squad", func(live *api.Team) error {
		live.ConvergencePosture = api.PostureHold
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: f.clock.Now(), Mode: api.HibernateModeHandoff, Seats: []string{hibernated}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordResume(api.ResumeEntry{Session: hibernated, Workspace: f.ws, Team: "squad", Role: "supervisor", LaunchedAt: listEpoch.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	crashed := api.Session{
		Name: "squad-supervisor-g1-7", Workspace: f.ws, Team: "squad", Role: "supervisor",
		Generation: 1, State: api.SessionCrashed, CreatedAt: listEpoch.Add(-3 * time.Hour),
	}
	if err := store.CreateSession(&crashed); err != nil {
		t.Fatal(err)
	}

	f.wake()
	f.ctrl.ReconcileOnce()

	var preds []string
	for _, s := range store.ListSessionsByTeamRole(f.ws, "squad", "supervisor") {
		if s.State.CountsAsAlive() {
			preds = append(preds, s.Predecessor)
		}
	}
	sort.Strings(preds)
	want := []string{crashed.Key(), hibernated}
	sort.Strings(want)
	if !reflect.DeepEqual(preds, want) {
		t.Errorf("supervisor seats name %v, want the hibernated seat and the crashed marker: %v", preds, want)
	}
}

func crashRig(t *testing.T, replicas int) (*api.Store, *Controller, *testClock, string) {
	t.Helper()
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	clock := newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now
	root := realDir(t)
	if err := store.CreateWorkspace(&api.Workspace{Name: "test-crash-rig", Root: root, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTeam(&api.Team{
		Name: "squad", Workspace: "test-crash-rig", Generation: 1,
		Roles:              []api.Role{{Name: "worker", Replicas: replicas, WorkDir: root, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}}},
		ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return store, ctrl, clock, root
}

func aliveWorkers(store *api.Store) []api.Session {
	var out []api.Session
	for _, s := range store.ListSessionsByTeamRole("test-crash-rig", "squad", "worker") {
		if s.State.CountsAsAlive() {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// A key is spent once a created seat takes it. At replicas 2 the first seat
// crashes and is replaced, and the replacement crashes in turn: it names the seat
// it replaces, and the key the first replacement took is not offered again.
func TestCrashRepairNamesOnlyTheLatestCrashedSeat(t *testing.T) {
	store, ctrl, clock, _ := crashRig(t, 2)
	ctrl.ReconcileOnce()
	seats := aliveWorkers(store)
	if len(seats) != 2 {
		t.Fatalf("setup: %d seats after the first pass, want 2", len(seats))
	}
	repair := func(victim api.Session) api.Session {
		t.Helper()
		before := map[time.Time]bool{}
		for _, s := range aliveWorkers(store) {
			before[s.CreatedAt] = true
		}
		killPaneAndWait(t, victim.PaneID)
		ctrl.ReconcileOnce() // reap
		clock.Advance(30 * time.Minute)
		ctrl.ReconcileOnce() // backoff elapsed: the replacement
		var fresh []api.Session
		for _, s := range aliveWorkers(store) {
			if !before[s.CreatedAt] {
				fresh = append(fresh, s)
			}
		}
		if len(fresh) != 1 {
			var all []string
			for _, s := range store.ListSessionsByTeamRole("test-crash-rig", "squad", "worker") {
				all = append(all, s.Name+" "+string(s.State)+" pred="+s.Predecessor)
			}
			t.Fatalf("after repairing %s: replacements = %d, want one; sessions: %v", victim.Key(), len(fresh), all)
		}
		return fresh[0]
	}
	first := repair(seats[0])
	if first.Predecessor != seats[0].Key() {
		t.Fatalf("first replacement names %q, want %q", first.Predecessor, seats[0].Key())
	}
	second := repair(first)
	if second.Predecessor != first.Key() {
		t.Errorf("second replacement names %q, want the seat it replaces, %q", second.Predecessor, first.Key())
	}
}

// A refused Create takes nothing: the placement is fixed on a later tick, and
// the replacement created then still names the crashed seat.
func TestCrashRepairRefusedCreateKeepsTheKey(t *testing.T) {
	store, ctrl, clock, root := crashRig(t, 1)
	ctrl.ReconcileOnce()
	seats := aliveWorkers(store)
	if len(seats) != 1 {
		t.Fatalf("setup: %d seats after the first pass, want 1", len(seats))
	}
	killPaneAndWait(t, seats[0].PaneID)
	ctrl.ReconcileOnce() // reap
	clock.Advance(30 * time.Minute)

	moved := root + ".moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce() // the workdir is gone: Create refuses the replacement
	if got := aliveWorkers(store); len(got) != 0 {
		t.Fatalf("setup: a replacement was created with the workdir gone: %+v", got)
	}
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()
	got := aliveWorkers(store)
	if len(got) != 1 || got[0].Predecessor != seats[0].Key() {
		t.Errorf("replacement after the refusal = %+v, want one seat naming %q", got, seats[0].Key())
	}
}

// Keys kept from an earlier tick and keys read now come back in one index order.
func TestCrashPredecessorsKeepIndexOrderAcrossTicks(t *testing.T) {
	store, ctrl, team := crashEnv(t, 3)
	k10 := crashRow(t, store, "ops-writer-g1-10", 1, api.SessionCrashed, "")
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", team.Generation), []string{k10}, "tick 1")
	if err := store.DeleteSession(k10); err != nil {
		t.Fatal(err)
	}
	k2 := crashRow(t, store, "ops-writer-g1-2", 1, api.SessionCrashed, "")
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", team.Generation), []string{k2, k10}, "tick 2, an older crash with a higher index")
}

func pendingCount(c *Controller) int {
	c.crashMu.Lock()
	defer c.crashMu.Unlock()
	return len(c.crashPending)
}

// Pending keys go when the team leaves the generation they were read in, when
// the team is deleted, and when the role is no longer declared; a later spawn
// of that role never names a seat from before.
func TestCrashPendingIsPrunedWhenTheTeamMovesOn(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, store *api.Store, team *api.Team){
		"generation changes": func(t *testing.T, store *api.Store, team *api.Team) {
			if err := store.UpdateTeam(team.Key(), func(live *api.Team) error { live.Generation = 2; return nil }); err != nil {
				t.Fatal(err)
			}
		},
		"team deleted": func(t *testing.T, store *api.Store, team *api.Team) {
			if err := store.DeleteTeam(team.Key()); err != nil {
				t.Fatal(err)
			}
		},
		"role removed": func(t *testing.T, store *api.Store, team *api.Team) {
			if err := store.UpdateTeam(team.Key(), func(live *api.Team) error { live.Roles = nil; return nil }); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, ctrl, team := crashEnv(t, 1)
			k := crashRow(t, store, "ops-writer-g1-0", 1, api.SessionCrashed, "")
			wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", 1), []string{k}, "before")
			if err := store.DeleteSession(k); err != nil {
				t.Fatal(err)
			}
			if n := pendingCount(ctrl); n != 1 {
				t.Fatalf("setup: %d pending lists, want 1", n)
			}
			change(t, store, team)
			ctrl.pruneCrashPending()
			if n := pendingCount(ctrl); n != 0 {
				t.Errorf("%d pending lists after the prune, want none", n)
			}
		})
	}
}

// A control for the prune: a team that stays as it is keeps its pending keys.
func TestCrashPendingIsKeptWhileTheTeamStays(t *testing.T) {
	store, ctrl, team := crashEnv(t, 1)
	k := crashRow(t, store, "ops-writer-g1-0", 1, api.SessionCrashed, "")
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", 1), []string{k}, "before")
	if err := store.DeleteSession(k); err != nil {
		t.Fatal(err)
	}
	ctrl.pruneCrashPending()
	wantPredecessors(t, ctrl.pendingCrashPredecessors(team, "writer", 1), []string{k}, "after a prune that changes nothing")
}

// A role that wants no spawn has no replacement coming: a pass that finds it
// steady forgets its pending keys, so the seat created when it scales up again
// does not name a seat from before.
func TestCrashPendingIsDroppedWhenTheRoleScalesDown(t *testing.T) {
	store, ctrl, clock, root := crashRig(t, 1)
	ctrl.ReconcileOnce()
	seats := aliveWorkers(store)
	if len(seats) != 1 {
		t.Fatalf("setup: %d seats after the first pass, want 1", len(seats))
	}
	killPaneAndWait(t, seats[0].PaneID)
	ctrl.ReconcileOnce() // reap
	clock.Advance(30 * time.Minute)
	moved := root + ".moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce() // the replacement is refused: its key stays pending
	if n := pendingCount(ctrl); n != 1 {
		t.Fatalf("setup: %d pending lists after the refused spawn, want 1", n)
	}

	setReplicas := func(n int) {
		if err := store.UpdateTeam("test-crash-rig/squad", func(live *api.Team) error { live.Roles[0].Replicas = n; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	setReplicas(0)
	ctrl.ReconcileOnce() // steady at zero
	if n := pendingCount(ctrl); n != 0 {
		t.Errorf("%d pending lists after the role scaled to zero, want none", n)
	}
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	setReplicas(1)
	ctrl.ReconcileOnce()
	got := aliveWorkers(store)
	if len(got) != 1 || got[0].Predecessor != "" {
		t.Errorf("seat after the scale-up = %+v, want one seat with no predecessor", got)
	}
}

// The reconcile pass itself prunes: a team that moves to a new generation while
// a replacement is still refused loses the old generation's pending keys.
func TestCrashPendingIsPrunedByTheReconcilePass(t *testing.T) {
	store, ctrl, clock, root := crashRig(t, 1)
	ctrl.ReconcileOnce()
	seats := aliveWorkers(store)
	if len(seats) != 1 {
		t.Fatalf("setup: %d seats after the first pass, want 1", len(seats))
	}
	killPaneAndWait(t, seats[0].PaneID)
	ctrl.ReconcileOnce() // reap
	clock.Advance(30 * time.Minute)
	if err := os.Rename(root, root+".moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(root+".moved", root) })
	ctrl.ReconcileOnce() // refused: the key stays pending
	if n := pendingCount(ctrl); n != 1 {
		t.Fatalf("setup: %d pending lists after the refused spawn, want 1", n)
	}
	if err := store.UpdateTeam("test-crash-rig/squad", func(live *api.Team) error { live.Generation = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()
	if n := pendingCount(ctrl); n != 0 {
		t.Errorf("%d pending lists after a pass in the new generation, want none", n)
	}
}
