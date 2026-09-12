package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The churn finding-036 measured, under the reconciler, and its absence
// after ADR-010 + aae-orc-bxeh: a headless role that exits 0 is marked
// succeeded, holds its replica slot, and is NOT refilled on any later
// tick. No RoleHealth is created because completion is not a crash.
//
// The control is a headless role that exits non-zero: it is charged as a
// crash and respawned after backoff, exactly as before, so a broken job
// still retries.
func TestHeadlessCompletionHoldsSlotAndIsNotRefilled(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	clock := newTestClock(time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-headless-done", "jobs", []api.Role{
		{
			Name: "job", Replicas: 1,
			// Team sessions launch through the adapter path, which quotes
			// each arg itself (the generic adapter's buildCommand).
			Runtime: api.Runtime{Name: "sh", Command: "sh", Args: []string{"-c", "exit 0"}, Mode: api.RuntimeModeHeadless},
		},
		{
			Name: "broken", Replicas: 1,
			Runtime: api.Runtime{Name: "sh", Command: "sh", Args: []string{"-c", "exit 3"}, Mode: api.RuntimeModeHeadless},
		},
	})

	ctrl.ReconcileOnce() // spawns both
	waitAllPanesDead(t, store, "test-headless-done", "jobs")
	ctrl.ReconcileOnce() // reap: one completion, one crash

	done := store.ListSessionsByTeamRole("test-headless-done", "jobs", "job")
	if len(done) != 1 || done[0].State != api.SessionSucceeded || done[0].ExitStatus != "0" {
		t.Fatalf("job after reap = %+v, want one succeeded row with exit 0", summarize(done))
	}
	firstRun := done[0].Name
	if _, ok := ctrl.RoleHealthSnapshot("test-headless-done", "jobs", "job"); ok {
		t.Fatal("completion created RoleHealth; a finished job is not a crash and must not be charged")
	}

	broken := store.ListSessionsByTeamRole("test-headless-done", "jobs", "broken")
	if len(broken) != 1 || broken[0].State != api.SessionCrashed || broken[0].ExitStatus != "3" {
		t.Fatalf("broken after reap = %+v, want one crashed row with exit 3", summarize(broken))
	}
	if rh, ok := ctrl.RoleHealthSnapshot("test-headless-done", "jobs", "broken"); !ok || rh.RestartCount != 1 {
		t.Fatalf("broken RoleHealth = %+v (%v), want restart #1 charged", rh, ok)
	}

	// Ticks well past every backoff window: the finished job is never
	// refilled; the broken one is respawned (and will crash again).
	for i := 0; i < 3; i++ {
		clock.Advance(10 * time.Minute)
		ctrl.ReconcileOnce()
	}
	done = store.ListSessionsByTeamRole("test-headless-done", "jobs", "job")
	if len(done) != 1 || done[0].Name != firstRun || done[0].State != api.SessionSucceeded {
		t.Fatalf("job after later ticks = %+v, want the original succeeded row, untouched", summarize(done))
	}
	plan := ctrl.planRole(mustTeam(t, store, "test-headless-done/jobs"), &api.Role{Name: "job", Replicas: 1}, 1)
	if plan.Action != RoleSteady || plan.Actual != 1 {
		t.Fatalf("job plan = %+v, want steady with actual=1 (the completed run holds its slot)", plan)
	}

	broken = store.ListSessionsByTeamRole("test-headless-done", "jobs", "broken")
	for _, s := range broken {
		if s.State == api.SessionSucceeded {
			t.Fatalf("broken role has a succeeded row %+v; a non-zero exit must never read as completion", s)
		}
	}
	rh, _ := ctrl.RoleHealthSnapshot("test-headless-done", "jobs", "broken")
	if rh.RestartCount < 2 {
		t.Fatalf("broken RestartCount = %d after three backoff-clearing ticks, want respawns to continue", rh.RestartCount)
	}
}

func waitAllPanesDead(t *testing.T, store *api.Store, ws, team string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive := 0
		for _, s := range store.ListSessionsByTeam(ws, team) {
			out, err := tmuxTestCmd("list-panes", "-t", s.PaneID, "-F", "#{pane_dead}").CombinedOutput()
			if err == nil && strings.TrimSpace(string(out)) == "0" {
				alive++
			}
		}
		if alive == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("panes still alive after 3s")
}

func mustTeam(t *testing.T, store *api.Store, key string) *api.Team {
	t.Helper()
	tm, err := store.GetTeam(key)
	if err != nil {
		t.Fatalf("get team %s: %v", key, err)
	}
	return &tm
}

func summarize(sessions []api.Session) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.Name+"="+string(s.State)+"(exit "+s.ExitStatus+")")
	}
	return out
}
