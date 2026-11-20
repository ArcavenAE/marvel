package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// A health restart deletes the failed seat's row before the reconciler spawns
// the replacement, so the replacement has no row to read as its predecessor and
// its MARVEL_PREDECESSOR is empty (marvel#1402). The crash path keeps the key in
// the controller's pending list (crash_predecessor.go); the restart path must do
// the same. These tests need tmux, as the neighboring crash-repair tests do.

// healthRestart runs the restart a failed health check starts for victim, then
// lets the backoff elapse and reconciles once, and returns the seats alive after.
func healthRestart(t *testing.T, store *api.Store, ctrl *Controller, clock *testClock, victim api.Session) []api.Session {
	t.Helper()
	team, err := store.GetTeam("test-crash-rig/squad")
	if err != nil {
		t.Fatal(err)
	}
	role := team.Roles[0]
	ctrl.restartSession(&victim, &team, &role)
	clock.Advance(30 * time.Minute)
	ctrl.ReconcileOnce()
	return aliveWorkers(store)
}

// freshSeats are the seats in after that were not in before. A single replica
// restarts under the same key, so a seat is told apart by when it was created.
func freshSeats(before, after []api.Session) []api.Session {
	seen := map[time.Time]bool{}
	for _, s := range before {
		seen[s.CreatedAt] = true
	}
	var out []api.Session
	for _, s := range after {
		if !seen[s.CreatedAt] {
			out = append(out, s)
		}
	}
	return out
}

// One replica fails its health check and is restarted: the replacement names
// the seat it replaces, by key.
func TestHealthRestartReplacementNamesTheSeatItReplaces(t *testing.T) {
	store, ctrl, clock, _ := crashRig(t, 1)
	ctrl.ReconcileOnce()
	seats := aliveWorkers(store)
	if len(seats) != 1 {
		t.Fatalf("setup: %d seats after the first pass, want 1", len(seats))
	}
	fresh := freshSeats(seats, healthRestart(t, store, ctrl, clock, seats[0]))
	if len(fresh) != 1 {
		t.Fatalf("replacements = %d, want one", len(fresh))
	}
	if fresh[0].Predecessor != seats[0].Key() {
		t.Errorf("replacement predecessor = %q, want the restarted seat %q", fresh[0].Predecessor, seats[0].Key())
	}
}

// At replicas 2 the replacement's own index differs from the seat's, and the
// seat that stays alive is not named.
func TestHealthRestartAtReplicasTwoNamesTheRestartedSeatOnly(t *testing.T) {
	store, ctrl, clock, _ := crashRig(t, 2)
	ctrl.ReconcileOnce()
	seats := aliveWorkers(store)
	if len(seats) != 2 {
		t.Fatalf("setup: %d seats after the first pass, want 2", len(seats))
	}
	victim, bystander := seats[1], seats[0]
	fresh := freshSeats(seats, healthRestart(t, store, ctrl, clock, victim))
	if len(fresh) != 1 {
		t.Fatalf("replacements = %d, want one", len(fresh))
	}
	if fresh[0].Predecessor != victim.Key() {
		t.Errorf("replacement predecessor = %q, want the restarted seat %q", fresh[0].Predecessor, victim.Key())
	}
	for _, s := range aliveWorkers(store) {
		if s.Key() == bystander.Key() && s.Predecessor != "" {
			t.Errorf("the seat that stayed alive now names %q, want none", s.Predecessor)
		}
	}
}

// A replacement that is restarted in turn names the replacement, not the first
// seat: a key a created seat took is not offered again.
func TestHealthRestartTwiceNamesTheLatestSeat(t *testing.T) {
	store, ctrl, clock, _ := crashRig(t, 1)
	ctrl.ReconcileOnce()
	first := aliveWorkers(store)
	second := freshSeats(first, healthRestart(t, store, ctrl, clock, first[0]))
	if len(second) != 1 {
		t.Fatalf("first restart: replacements = %d, want one", len(second))
	}
	third := freshSeats(append(first, second...), healthRestart(t, store, ctrl, clock, second[0]))
	if len(third) != 1 {
		t.Fatalf("second restart: replacements = %d, want one", len(third))
	}
	if third[0].Predecessor != second[0].Key() {
		t.Errorf("second replacement predecessor = %q, want %q", third[0].Predecessor, second[0].Key())
	}
}
