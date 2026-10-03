package team

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
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

// A session is placed from the team's applied anchor and never from the live
// workspace root. A team applied with no placement carries no anchor, so its
// sessions are placed nowhere even when the workspace has a root: that root is
// one value per workspace, and reading it at spawn would move a team whenever
// another team's apply moved it.
func TestSpawnPlacesFromTheTeamAnchorNeverTheLiveRoot(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	root, anchor := realDir(t), realDir(t)
	out := realDir(t)
	createPlacedTeam(t, store, "test-wd-noanchor", root, "", []api.Role{cwdRecorder(t, out, "rootonly", "")})
	createPlacedTeam(t, store, "test-wd-anchor", root, anchor, []api.Role{cwdRecorder(t, out, "anchored", "")})
	ctrl.ReconcileOnce()

	none := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-noanchor", "squad", "rootonly", 1))
	if len(none) != 1 || none[0].WorkDir != "" {
		t.Fatalf("a team with no anchor in a workspace with a root: %+v, want one session placed nowhere", none)
	}
	at := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-anchor", "squad", "anchored", 1))
	if len(at) != 1 || at[0].WorkDir != anchor {
		t.Fatalf("an anchored team: %+v, want WorkDir %q", at, anchor)
	}
}

// Two teams of one workspace keep their own anchors however the workspace root
// moves afterwards.
func TestTwoTeamsKeepTheirAnchorsWhenTheWorkspaceRootMoves(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	first, second, moved := realDir(t), realDir(t), realDir(t)
	out := realDir(t)
	if err := store.CreateWorkspace(&api.Workspace{Name: "test-wd-two", Root: first, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"alpha": first, "beta": second} {
		if err := store.CreateTeam(&api.Team{
			Name: name, Workspace: "test-wd-two", WorkDir: dir, Generation: 1,
			Roles:              []api.Role{cwdRecorder(t, out, "seat", "")},
			ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A later apply of another team moves the workspace root.
	if err := store.UpdateWorkspace("test-wd-two", func(w *api.Workspace) error { w.Root = moved; return nil }); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()

	for name, want := range map[string]string{"alpha": first, "beta": second} {
		got := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-two", name, "seat", 1))
		if len(got) != 1 || got[0].WorkDir != want {
			t.Fatalf("team %s: %+v, want WorkDir %q and not the moved root %q", name, got, want, moved)
		}
		if cwd := waitCwd(t, out, got[0].Name); cwd != want {
			t.Errorf("team %s: pane cwd = %q, want %q", name, cwd, want)
		}
	}
}

func refusedEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindSessionPlacementRefused}, 0)
}

// tmux starts a pane in $HOME when new-window is given a directory that does
// not exist, and exits 0, so a pinned directory that was removed would move
// the seat silently. The spawn is refused instead, with an event that names
// the team, the role and the path, and nothing is created.
func TestSpawnRefusesAMissingWorkDirAndNeverFallsToHome(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(64)
	sessMgr.Events = ring
	gone := filepath.Join(realDir(t), "removed-worktree")
	out := realDir(t)
	createPlacedTeam(t, store, "test-wd-missing", "", gone, []api.Role{cwdRecorder(t, out, "seat", "")})

	ctrl.ReconcileOnce()
	ctrl.ReconcileOnce()

	if got := store.ListSessionsByTeamRoleGeneration("test-wd-missing", "squad", "seat", 1); len(got) != 0 {
		t.Fatalf("a session exists for a team whose directory is missing: %+v", got)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("a pane started and ran from somewhere (%d record(s)); it must not", len(entries))
	}
	ev := refusedEvents(ring)
	if len(ev) != 1 {
		t.Fatalf("%d refusal events over two ticks, want one per change: %+v", len(ev), ev)
	}
	for _, want := range []string{"squad", "seat", gone} {
		if !strings.Contains(ev[0].Message+ev[0].Team+ev[0].Role, want) {
			t.Errorf("refusal event %+v does not name %q", ev[0], want)
		}
	}
	if ev[0].Team != "squad" || ev[0].Role != "seat" || ev[0].Severity != events.SeverityWarning {
		t.Errorf("event fields = team %q role %q severity %v, want squad, seat, warning", ev[0].Team, ev[0].Role, ev[0].Severity)
	}

	// Once the directory exists the next tick spawns there.
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()
	got := aliveSessions(store.ListSessionsByTeamRoleGeneration("test-wd-missing", "squad", "seat", 1))
	if len(got) != 1 || got[0].WorkDir != gone {
		t.Fatalf("after the directory exists: %+v, want one session at %q", got, gone)
	}
	if cwd := waitCwd(t, out, got[0].Name); cwd != gone {
		t.Errorf("pane cwd = %q, want %q", cwd, gone)
	}
}

// A path that exists but is a file is refused the same way.
func TestSpawnRefusesAWorkDirThatIsAFile(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(64)
	sessMgr.Events = ring
	file := filepath.Join(realDir(t), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	createPlacedTeam(t, store, "test-wd-file", "", file, []api.Role{cwdRecorder(t, realDir(t), "seat", "")})

	ctrl.ReconcileOnce()

	if got := store.ListSessionsByTeamRoleGeneration("test-wd-file", "squad", "seat", 1); len(got) != 0 {
		t.Fatalf("a session was created in a path that is a file: %+v", got)
	}
	if len(refusedEvents(ring)) != 1 {
		t.Fatalf("refusal events = %+v, want one", refusedEvents(ring))
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

// A refused spawn is retried every reconcile tick (2s), so a missing anchor used
// to log the same error about 1800 times an hour per role. The refusal is logged
// when it starts and then at most every five minutes, once, by the manager; the
// controller does not log it again.
func TestPlacementRefusalLogIsRateLimited(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	sessMgr.Clock = func() time.Time { return clock }
	sessMgr.Events = events.NewRing(64)
	gone := filepath.Join(realDir(t), "removed-for-log")
	createPlacedTeam(t, store, "test-wd-log", "", gone, []api.Role{cwdRecorder(t, realDir(t), "seat", "")})

	var buf syncBuffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	count := func() int { return strings.Count(buf.String(), gone) }

	for i := 0; i < 5; i++ {
		clock = clock.Add(2 * time.Second)
		ctrl.ReconcileOnce()
	}
	if got := count(); got != 1 {
		t.Fatalf("%d log lines naming the missing directory over five ticks, want 1:\n%s", got, buf.String())
	}
	clock = clock.Add(6 * time.Minute)
	ctrl.ReconcileOnce()
	ctrl.ReconcileOnce()
	if got := count(); got != 2 {
		t.Errorf("%d log lines after five minutes, want one more (2):\n%s", got, buf.String())
	}
}

// The event is emitted when the refusal starts and again at a low cadence while
// it lasts, so it cannot roll off the ring unseen.
func TestPlacementRefusalIsReEmittedAtALowCadence(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	sessMgr.Clock = func() time.Time { return clock }
	ring := events.NewRing(64)
	sessMgr.Events = ring
	gone := filepath.Join(realDir(t), "removed-for-events")
	createPlacedTeam(t, store, "test-wd-reemit", "", gone, []api.Role{cwdRecorder(t, realDir(t), "seat", "")})

	for i := 0; i < 3; i++ {
		clock = clock.Add(2 * time.Second)
		ctrl.ReconcileOnce()
	}
	if got := len(refusedEvents(ring)); got != 1 {
		t.Fatalf("%d events over three ticks, want 1", got)
	}
	clock = clock.Add(10 * time.Minute)
	ctrl.ReconcileOnce()
	if got := len(refusedEvents(ring)); got != 1 {
		t.Errorf("%d events after ten minutes, want still 1", got)
	}
	clock = clock.Add(6 * time.Minute)
	ctrl.ReconcileOnce()
	ctrl.ReconcileOnce()
	if got := len(refusedEvents(ring)); got != 2 {
		t.Errorf("%d events after sixteen minutes, want 2", got)
	}
}

// The refusal is a standing condition the daemon can report, and it clears when
// the directory returns.
func TestPlacementRefusalIsAStandingCondition(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	sessMgr.Clock = func() time.Time { return clock }
	sessMgr.Events = events.NewRing(64)
	gone := filepath.Join(realDir(t), "removed-for-condition")
	createPlacedTeam(t, store, "test-wd-cond", "", gone, []api.Role{cwdRecorder(t, realDir(t), "seat", "")})

	ctrl.ReconcileOnce()
	got := sessMgr.PlacementRefusals()
	if len(got) != 1 {
		t.Fatalf("refusals = %+v, want one", got)
	}
	r := got[0]
	if r.Workspace != "test-wd-cond" || r.Team != "squad" || r.Role != "seat" || r.WorkDir != gone || !r.Since.Equal(clock) {
		t.Errorf("refusal = %+v, want workspace test-wd-cond, squad/seat, %s, since %s", r, gone, clock)
	}
	if !strings.Contains(r.Message, "does not exist") {
		t.Errorf("message = %q, want the reason", r.Message)
	}
	clock = clock.Add(time.Minute)
	ctrl.ReconcileOnce()
	if again := sessMgr.PlacementRefusals(); len(again) != 1 || !again[0].Since.Equal(r.Since) {
		t.Errorf("a repeated refusal moved Since: %+v", again)
	}

	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	ctrl.ReconcileOnce()
	if left := sessMgr.PlacementRefusals(); len(left) != 0 {
		t.Errorf("refusals after the directory returned = %+v, want none", left)
	}
}

// syncBuffer is a log sink two goroutines can write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A standing refusal must not outlive the want behind it: a role scaled to
// zero, a role removed from the team, and a deleted team are not being refused
// any more, so the condition goes, whether or not the directory returns.
func TestPlacementRefusalClearsWhenTheRoleStopsWantingReplicas(t *testing.T) {
	skipIfNoTmux(t)
	cases := map[string]func(t *testing.T, store *api.Store, ws string){
		"scaled to zero": func(t *testing.T, store *api.Store, ws string) {
			t.Helper()
			if err := store.UpdateTeam(ws+"/squad", func(tm *api.Team) error {
				for i := range tm.Roles {
					tm.Roles[i].Replicas = 0
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		},
		"role removed": func(t *testing.T, store *api.Store, ws string) {
			t.Helper()
			if err := store.UpdateTeam(ws+"/squad", func(tm *api.Team) error { tm.Roles = nil; return nil }); err != nil {
				t.Fatal(err)
			}
		},
		"team deleted": func(t *testing.T, store *api.Store, ws string) {
			t.Helper()
			if err := store.DeleteTeam(ws + "/squad"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range cases {
		store, sessMgr, ctrl, cleanup := setup(t)
		ws := "test-wd-stale-" + strings.ReplaceAll(name, " ", "-")
		sessMgr.Events = events.NewRing(64)
		gone := filepath.Join(realDir(t), "removed-for-stale")
		createPlacedTeam(t, store, ws, "", gone, []api.Role{cwdRecorder(t, realDir(t), "seat", "")})

		ctrl.ReconcileOnce()
		if got := sessMgr.PlacementRefusals(); len(got) != 1 {
			cleanup()
			t.Fatalf("%s: refusals before = %+v, want one", name, got)
		}
		change(t, store, ws)
		ctrl.ReconcileOnce()
		if got := sessMgr.PlacementRefusals(); len(got) != 0 {
			t.Errorf("%s: refusals after = %+v, want none", name, got)
		}
		cleanup()
	}
}

// A hold (bus gate, crash backoff, admission, schedule) spawns nothing this tick
// but the role still wants a replica, so a standing refusal stays and keeps its
// start time; a flapping gate must not clear it and make it re-emit. Only a
// role that is steady or scaling down is not being refused any more.
func TestPlacementRefusalSurvivesHoldsAndClearsOnlyOnSteadyOrScaleDown(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	sessMgr.Clock = func() time.Time { return clock }
	sessMgr.Events = events.NewRing(64)
	ws := "test-wd-holds"
	gone := filepath.Join(realDir(t), "removed-for-holds")
	createPlacedTeam(t, store, ws, "", gone, []api.Role{cwdRecorder(t, realDir(t), "seat", "")})

	ctrl.ReconcileOnce()
	first := sessMgr.PlacementRefusals()
	if len(first) != 1 {
		t.Fatalf("refusals before = %+v, want one", first)
	}
	apply := func(plan RolePlan) {
		t.Helper()
		tm, err := store.GetTeam(ws + "/squad")
		if err != nil {
			t.Fatal(err)
		}
		ctrl.mu.Lock()
		defer ctrl.mu.Unlock()
		ctrl.applyRolePlan(&tm, &tm.Roles[0], plan)
	}

	clock = clock.Add(time.Minute)
	apply(RolePlan{Role: "seat", Action: RoleHold})
	kept := sessMgr.PlacementRefusals()
	if len(kept) != 1 || !kept[0].Since.Equal(first[0].Since) {
		t.Fatalf("a hold changed the standing refusal: %+v, want it kept with Since %s", kept, first[0].Since)
	}

	apply(RolePlan{Role: "seat", Action: RoleSteady})
	if left := sessMgr.PlacementRefusals(); len(left) != 0 {
		t.Errorf("refusals after a steady plan = %+v, want none", left)
	}

	ctrl.ReconcileOnce()
	if again := sessMgr.PlacementRefusals(); len(again) != 1 {
		t.Fatalf("refusal did not return on the next spawn attempt: %+v", again)
	}
	apply(RolePlan{Role: "seat", Action: RoleScaleDown})
	if left := sessMgr.PlacementRefusals(); len(left) != 0 {
		t.Errorf("refusals after a scale-down plan = %+v, want none", left)
	}
}
