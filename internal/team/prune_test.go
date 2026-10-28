package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

const pruneTestSource = "host-a:/srv/manifests/main.toml"

// These tests start no pane: they read and write the store, and a pruned
// session has no pane to kill. So they do not skip where ps cannot run.

// markPruning sets the team's Pruning as a prune would.
func markPruning(t *testing.T, store *api.Store, key string) {
	t.Helper()
	if err := store.UpdateTeam(key, func(live *api.Team) error {
		live.Pruning = &api.TeamPruning{Source: pruneTestSource, At: listEpoch}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A pruning team starts no shift: marvel shift is refused (ruling Q10).
func TestInitiateShiftRefusesAPruningTeam(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	createTeamFixture(t, store, "test-prune-shift", "crew", []api.Role{sleepRole("crew", 0)})
	markPruning(t, store, "test-prune-shift/crew")

	err := ctrl.InitiateShift("test-prune-shift/crew", "")
	if err == nil || !strings.Contains(err.Error(), "being pruned") {
		t.Fatalf("InitiateShift on a pruning team = %v, want a refusal naming the prune", err)
	}
	if got, _ := store.GetTeam("test-prune-shift/crew"); got.Shift.Phase != api.ShiftNone {
		t.Errorf("phase = %q, want none", got.Shift.Phase)
	}
}

// Neither automatic trigger acts on a pruning team, and neither opens a
// handoff request for it (ruling Q10).
func TestShiftTriggersSkipAPruningTeam(t *testing.T) {
	for _, tc := range []struct {
		name           string
		role           func(dir string) api.Role
		age            time.Duration
		tokens, window int
	}{
		{"max age", func(dir string) api.Role { return maxAgeRole(dir) }, testMaxAge + time.Hour, 0, 0},
		{"context pressure", pressureRole, time.Hour, 900_000, 1_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newListFixture(t, "test-prune-trig-"+strings.ReplaceAll(tc.name, " ", "-"), tc.role(t.TempDir()))
			f.seed(tc.age, 10*time.Minute, tc.tokens, tc.window)
			markPruning(t, f.store, f.teamKey)

			got := f.evaluate()
			if len(f.notices) != 0 || len(got.ShiftRequests) != 0 || got.Shift.Phase != api.ShiftNone {
				t.Fatalf("notices %q, requests %+v, phase %q; want none for a pruning team", f.notices, got.ShiftRequests, got.Shift.Phase)
			}
			if n := f.count(events.KindShiftAutoTriggered) + f.count(events.KindShiftHandoffRequested); n != 0 {
				t.Errorf("trigger events = %d, want 0", n)
			}
		})
	}
}

// While a team is pruning its roles want no seats, so the reconciler starts
// none, whatever the declared count (ruling Q2).
func TestReconcileStartsNoSeatForAPruningTeam(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	createTeamFixture(t, store, "test-prune-desired", "crew", []api.Role{sleepRole("crew", 2)})
	markPruning(t, store, "test-prune-desired/crew")

	ctrl.ReconcileOnce()
	if got := store.ListSessionsByTeam("test-prune-desired", "crew"); len(got) != 0 {
		t.Fatalf("sessions = %d, want 0: a pruning team wants no seats", len(got))
	}
}

// pruneFixture is a controller with two finished-seat teams, each claimed by
// nothing and released by pruneTestSource, as the apply leaves them.
func pruneFixture(t *testing.T) (*api.Store, *Controller, *events.Ring) {
	t.Helper()
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	if err := store.CreateWorkspace(&api.Workspace{Name: "test-prune", CreatedAt: listEpoch}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gone", "also"} {
		if err := store.CreateTeam(&api.Team{
			Name: name, Workspace: "test-prune", Roles: []api.Role{sleepRole("crew", 1)}, Generation: 1,
			Claims: &api.TeamClaims{Sources: map[string]time.Time{}, ReleasedBy: pruneTestSource, ReleasedAt: listEpoch},
		}); err != nil {
			t.Fatal(err)
		}
		seedSession(t, store, api.Session{Name: name + "-crew-g1-0", Workspace: "test-prune", Team: name, Role: "crew", Generation: 1, State: api.SessionFailed})
	}
	return store, ctrl, ring
}

// Prune deletes each team that passes both checks, its sessions with it, and
// records one team.pruned event per team with the source (rulings Q1, Q11).
func TestPruneDeletesTheTeamsThatPassAndRecordsEach(t *testing.T) {
	store, ctrl, ring := pruneFixture(t)
	mark := api.TeamPruning{Source: pruneTestSource, At: listEpoch}

	deleted, skipped := ctrl.Prune([]string{"test-prune/gone", "test-prune/also"}, mark, api.PruneRecheck(pruneTestSource))
	if strings.Join(deleted, ",") != "test-prune/also,test-prune/gone" || len(skipped) != 0 {
		t.Fatalf("deleted %q, skipped %+v; want both deleted, in key order", deleted, skipped)
	}
	for _, name := range []string{"gone", "also"} {
		if _, err := store.GetTeam("test-prune/" + name); err == nil {
			t.Errorf("team %s still stored", name)
		}
		if n := len(store.ListSessionsByTeam("test-prune", name)); n != 0 {
			t.Errorf("team %s keeps %d session(s)", name, n)
		}
	}
	evs := ring.Snapshot(events.Filter{Kind: events.KindTeamPruned}, 0)
	if len(evs) != 2 {
		t.Fatalf("team.pruned events = %d, want 2", len(evs))
	}
	for _, ev := range evs {
		if !strings.Contains(ev.Message, pruneTestSource) {
			t.Errorf("event %+v does not name the source", ev)
		}
	}
}

// A team that fails the check at the mark is never marked; one that passes the
// mark and fails the re-check just before the delete has its mark cleared.
// Neither is deleted, and each comes back with its reason (rulings Q1, Q3).
func TestPruneKeepsATeamThatFailsEitherCheck(t *testing.T) {
	store, ctrl, ring := pruneFixture(t)
	mark := api.TeamPruning{Source: pruneTestSource, At: listEpoch}
	calls := map[string]int{}
	check := func(tm *api.Team, _ []api.Session) string {
		calls[tm.Name]++
		switch {
		case tm.Name == "gone":
			return "fails at the mark"
		case calls[tm.Name] > 1:
			return "fails before the delete"
		}
		return ""
	}

	deleted, skipped := ctrl.Prune([]string{"test-prune/gone", "test-prune/also"}, mark, check)
	if len(deleted) != 0 {
		t.Fatalf("deleted %q, want none", deleted)
	}
	want := map[string]string{"test-prune/gone": "fails at the mark", "test-prune/also": "fails before the delete"}
	if len(skipped) != 2 {
		t.Fatalf("skipped %+v, want both", skipped)
	}
	for _, s := range skipped {
		if want[s.Team] != s.Reason {
			t.Errorf("skipped %s for %q, want %q", s.Team, s.Reason, want[s.Team])
		}
	}
	for _, name := range []string{"gone", "also"} {
		got, err := store.GetTeam("test-prune/" + name)
		if err != nil {
			t.Fatalf("team %s deleted: %v", name, err)
		}
		if got.Pruning != nil {
			t.Errorf("team %s keeps Pruning %+v", name, got.Pruning)
		}
	}
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindTeamPruned}, 0)); n != 0 {
		t.Errorf("team.pruned events = %d, want 0", n)
	}
}

// A team holding a marvel run session is kept by the check at the mark, and
// the run session survives the prune (section 5).
func TestPruneLeavesARunSessionAndItsTeam(t *testing.T) {
	store, ctrl, _ := pruneFixture(t)
	seedSession(t, store, api.Session{Name: "gone-run", Workspace: "test-prune", Team: "gone", Role: "crew", Generation: 0, State: api.SessionRunning})
	mark := api.TeamPruning{Source: pruneTestSource, At: listEpoch}

	deleted, skipped := ctrl.Prune([]string{"test-prune/gone"}, mark, api.PruneRecheck(pruneTestSource))
	if len(deleted) != 0 || len(skipped) != 1 || !strings.Contains(skipped[0].Reason, "run session") {
		t.Fatalf("deleted %q, skipped %+v; want ws/gone kept for its run session", deleted, skipped)
	}
	if _, err := store.GetSession("test-prune/gone-run"); err != nil {
		t.Errorf("the run session did not survive: %v", err)
	}
	if got, err := store.GetTeam("test-prune/gone"); err != nil || got.Pruning != nil {
		t.Errorf("team = %+v, %v; want it stored and unmarked", got.Pruning, err)
	}
}
