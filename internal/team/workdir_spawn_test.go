package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Spawn placement (docs/design/session-working-directory.md, decisions 4 and
// 7). The role's resolved workdir is copied onto the session at spawn and the
// pane starts there. Restart and shift read the applied team, so an edited
// workdir moves the next session.

// cwdRecorder writes a script that records its own working directory, by
// session name, into out, then sleeps. It returns a role running the script.
func cwdRecorder(t *testing.T, out, name, workDir string) api.Role {
	t.Helper()
	script := filepath.Join(t.TempDir(), "rec.sh")
	body := "#!/bin/sh\npwd -P > " + out + "/$MARVEL_SESSION\nexec sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return api.Role{
		Name:     name,
		Replicas: 1,
		WorkDir:  workDir,
		Runtime:  api.Runtime{Name: "rec", Command: script},
	}
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func waitCwd(t *testing.T, out, session string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(filepath.Join(out, session))
		if err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b))
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never reported its directory: %v", session, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func createPlacedTeam(t *testing.T, store *api.Store, ws, root, teamDir string, roles []api.Role) {
	t.Helper()
	if err := store.CreateWorkspace(&api.Workspace{Name: ws, Root: root, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTeam(&api.Team{
		Name: "squad", Workspace: ws, Roles: roles, WorkDir: teamDir, Generation: 1,
		ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// A session is placed by role over team over workspace root, recorded on the
// session, and its pane runs there.
func TestSpawnPlacesSessionByRoleOverTeamOverRoot(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	root, teamDir, roleDir := realDir(t), realDir(t), realDir(t)
	out := realDir(t)
	createPlacedTeam(t, store, "test-wd-spawn", root, teamDir, []api.Role{
		cwdRecorder(t, out, "byrole", roleDir),
		cwdRecorder(t, out, "byteam", ""),
	})
	ctrl.ReconcileOnce()

	for role, want := range map[string]string{"byrole": roleDir, "byteam": teamDir} {
		sessions := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-spawn", "squad", role, 1))
		if len(sessions) != 1 {
			t.Fatalf("role %s: %d sessions, want 1", role, len(sessions))
		}
		if sessions[0].WorkDir != want {
			t.Errorf("role %s: Session.WorkDir = %q, want %q", role, sessions[0].WorkDir, want)
		}
		if got := waitCwd(t, out, sessions[0].Name); got != want {
			t.Errorf("role %s: pane cwd = %q, want %q", role, got, want)
		}
	}
}

// With nothing declared the session is placed at the workspace root, and with
// no root either it is placed nowhere, which is how every session ran before.
func TestSpawnFallsBackToRootThenToNothing(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	root := realDir(t)
	out := realDir(t)
	createPlacedTeam(t, store, "test-wd-root", root, "", []api.Role{cwdRecorder(t, out, "atroot", "")})
	createPlacedTeam2 := func() {
		if err := store.CreateWorkspace(&api.Workspace{Name: "test-wd-none", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateTeam(&api.Team{
			Name: "squad", Workspace: "test-wd-none", Roles: []api.Role{sleepRole("nowhere", 1)}, Generation: 1,
			ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	createPlacedTeam2()
	ctrl.ReconcileOnce()

	at := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-root", "squad", "atroot", 1))
	if len(at) != 1 || at[0].WorkDir != root {
		t.Fatalf("root-placed session = %+v, want WorkDir %q", at, root)
	}
	none := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-none", "squad", "nowhere", 1))
	if len(none) != 1 || none[0].WorkDir != "" {
		t.Fatalf("unplaced session = %+v, want an empty WorkDir", none)
	}
}

// A shift after the workdir is edited moves the new generation, because the
// successor is placed from the applied team and not from the dead session.
func TestShiftAfterEditingWorkDirMovesTheNewGeneration(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	root, before, after := realDir(t), realDir(t), realDir(t)
	out := realDir(t)
	createPlacedTeam(t, store, "test-wd-shift", root, "", []api.Role{cwdRecorder(t, out, "crew", before)})
	ctrl.ReconcileOnce()
	first := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-shift", "squad", "crew", 1))
	if len(first) != 1 || waitCwd(t, out, first[0].Name) != before {
		t.Fatalf("first generation = %+v, want one session running in %q", first, before)
	}

	// What a re-apply with an edited workdir does to the stored team.
	if err := store.UpdateTeam("test-wd-shift/squad", func(live *api.Team) error {
		live.Roles[0].WorkDir = after
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.InitiateShift("test-wd-shift/squad", "crew"); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()

	next := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-shift", "squad", "crew", 2))
	if len(next) != 1 {
		t.Fatalf("second generation = %d sessions, want 1", len(next))
	}
	if next[0].WorkDir != after {
		t.Errorf("successor WorkDir = %q, want %q", next[0].WorkDir, after)
	}
	if got := waitCwd(t, out, next[0].Name); got != after {
		t.Errorf("successor pane cwd = %q, want %q", got, after)
	}
}
