package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A role whose policy reads a settings file is held, never launched without the
// permission set the file carries: the spawn is refused before any session
// exists, no failure or restart is counted, the error event is emitted once,
// and every reconcile tries again until the file reads. A changed file reaches
// a live session on the next reconcile.

const settingsFileJSON = `{"permissions": {"allow": ["Read"], "deny": ["Bash(rm:*)"]}}`

// settingsFileRig is a one-role team whose role references a policy that reads
// the file at path, with the manager's projection and event output kept in
// scratch.
func settingsFileRig(t *testing.T, ws, path string, runtime api.Runtime) (*api.Store, *Controller, *events.Ring, string) {
	t.Helper()
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(256)
	sessMgr.Events = ring
	ctrl.Events = ring
	projDir := t.TempDir()
	sessMgr.ProjectionDir = projDir

	createTeamFixture(t, store, ws, "squad", []api.Role{
		{Name: "worker", Replicas: 1, Policy: "contract", Runtime: runtime},
	})
	if err := store.CreatePolicy(&api.Policy{Name: "contract", Workspace: ws, SettingsFile: path, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return store, ctrl, ring, projDir
}

func sleepRuntime() api.Runtime {
	return api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}
}

func projectionFailures(ring *events.Ring) int {
	return len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed}, 0))
}

func TestHeldSpawnCreatesNoSessionAndCountsNoFailure(t *testing.T) {
	skipIfNoTmux(t)
	path := filepath.Join(t.TempDir(), "worker.json")
	store, ctrl, ring, _ := settingsFileRig(t, "test-sf-first", path, sleepRuntime())

	for tick := 0; tick < 4; tick++ {
		ctrl.ReconcileOnce()
	}
	if n := len(store.ListSessionsByTeamRole("test-sf-first", "squad", "worker")); n != 0 {
		t.Fatalf("a spawn whose settings file is missing created %d session(s), want 0", n)
	}
	if n := projectionFailures(ring); n != 1 {
		t.Errorf("policy.projection-failed events over four ticks = %d, want 1", n)
	}
	if rh, ok := ctrl.RoleHealthSnapshot("test-sf-first", "squad", "worker"); ok && (rh.RestartCount != 0 || !rh.BackoffUntil.IsZero()) {
		t.Errorf("held ticks counted against the role: %+v, want no restart and no backoff", rh)
	}

	// The same role spawns once the file reads.
	if err := os.WriteFile(path, []byte(settingsFileJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-sf-first", "squad", "worker")
	if len(sessions) != 1 || sessions[0].State != api.SessionRunning {
		t.Fatalf("after the file read: sessions = %+v, want one running", sessions)
	}
}

func TestShiftSuccessorHeldBySettingsFileLeavesThePredecessorRunning(t *testing.T) {
	skipIfNoTmux(t)
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := os.WriteFile(path, []byte(settingsFileJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store, ctrl, ring, _ := settingsFileRig(t, "test-sf-shift", path, sleepRuntime())
	teamKey := "test-sf-shift/squad"

	ctrl.ReconcileOnce()
	gen1 := store.ListSessionsByTeamRoleGeneration("test-sf-shift", "squad", "worker", 1)
	if len(gen1) != 1 {
		t.Fatalf("expected one gen-1 session, got %d", len(gen1))
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.InitiateShift(teamKey, ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}
	for tick := 0; tick < 4; tick++ {
		ctrl.ReconcileOnce()
	}
	if n := len(store.ListSessionsByTeamRoleGeneration("test-sf-shift", "squad", "worker", 2)); n != 0 {
		t.Errorf("a successor with an unreadable settings file was created: %d gen-2 session(s)", n)
	}
	live, err := store.GetSession(gen1[0].Key())
	if err != nil || live.State != api.SessionRunning {
		t.Errorf("predecessor = %+v (err %v), want it still running", live, err)
	}
	if team, _ := store.GetTeam(teamKey); team.Shift.Phase != api.ShiftLaunching {
		t.Errorf("shift phase = %q, want launching", team.Shift.Phase)
	}
	if n := projectionFailures(ring); n != 1 {
		t.Errorf("policy.projection-failed events = %d, want 1", n)
	}

	// Once the file reads the shift goes on and the role is replaced.
	if err := os.WriteFile(path, []byte(settingsFileJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < 20; tick++ {
		ctrl.ReconcileOnce()
		if team, _ := store.GetTeam(teamKey); team.Shift.Phase == api.ShiftNone {
			break
		}
	}
	if team, _ := store.GetTeam(teamKey); team.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift did not complete once the file read, phase %q", team.Shift.Phase)
	}
	if n := len(store.ListSessionsByTeamRoleGeneration("test-sf-shift", "squad", "worker", 2)); n != 1 {
		t.Errorf("gen-2 sessions after the shift = %d, want 1", n)
	}
}

// ReconcileOnce re-projects a changed settings file for a live session: the
// session tests call ReprojectFileBacked directly, so this is the test that
// fails if the controller stops calling it.
func TestReconcileOnceReprojectsAChangedSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := os.WriteFile(path, []byte(settingsFileJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store, ctrl, ring, projDir := settingsFileRig(t, "test-sf-reproject", path, api.Runtime{Name: "claude", Command: "claude"})

	// A live session of the role, with no pane: nothing here launches.
	sess := &api.Session{
		Name: "squad-worker-g1-0", Workspace: "test-sf-reproject", Team: "squad", Role: "worker",
		Generation: 1, Runtime: api.Runtime{Name: "claude", Command: "claude"},
	}
	if err := store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.State = api.SessionRunning
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	projected := filepath.Join(projDir, strings.ReplaceAll(sess.Key(), "/", "-")+".settings.json")

	ctrl.ReconcileOnce()
	first, err := os.ReadFile(projected)
	if err != nil {
		t.Fatalf("the first reconcile did not project the settings file: %v", err)
	}
	if !strings.Contains(string(first), `"Bash(rm:*)"`) {
		t.Fatalf("projection lacks the file's deny rule:\n%s", first)
	}

	if err := os.WriteFile(path, []byte(`{"permissions": {"allow": ["Read"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()
	second, err := os.ReadFile(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(second), "Bash(rm:*)") {
		t.Errorf("a rewritten settings file was not re-projected by ReconcileOnce:\n%s", second)
	}
	evs := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected, Session: sess.Key()}, 0)
	if len(evs) != 2 {
		t.Errorf("policy.projected events = %d, want 2", len(evs))
	}
}
