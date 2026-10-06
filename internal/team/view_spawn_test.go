package team

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/view"
)

// A seat's first view build must not run inside the controller's lock: a hung
// remote at spawn would otherwise stall every team's reconcile for the build's
// bound (docs/design/readonly-view.md section 7; aae-orc-mwbql).

const viewSpawnSHA = "6666666666666666666666666666666666666666"

// spawnGit resolves to viewSpawnSHA. When hang is set its fetch waits for its
// context, as a hung remote does.
type spawnGit struct{ hang bool }

func (g spawnGit) Fetch(ctx context.Context, _, _, _ string) error {
	if g.hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (spawnGit) Resolve(context.Context, string, string) (string, error) { return viewSpawnSHA, nil }

func (spawnGit) Archive(_ context.Context, _, _, dest string) error {
	return os.WriteFile(filepath.Join(dest, "README"), []byte("tree"), 0o644)
}

// envRecorder is a role whose script records its MARVEL_VIEW_REPO, or "unset",
// by session name into out, then sleeps.
func envRecorder(t *testing.T, out, name string, views []api.View) api.Role {
	t.Helper()
	script := filepath.Join(t.TempDir(), "env.sh")
	body := "#!/bin/sh\necho \"${MARVEL_VIEW_REPO:-unset}\" > " + out + "/$MARVEL_SESSION\nexec sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return api.Role{
		Name: name, Replicas: 1, Views: views,
		Runtime: api.Runtime{Name: "rec", Command: script},
	}
}

var repoView = []api.View{{Name: "repo", Remote: "remote", Ref: "main"}}

// viewSpawnRig is two teams in one workspace: "slow" declares a view, "fast"
// declares none.
func viewSpawnRig(t *testing.T, git view.Git, spawnBound time.Duration) (*api.Store, *Controller, *view.Keeper, string) {
	t.Helper()
	skipIfNoTmux(t)
	store, mgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	out := realDir(t)
	keeper := &view.Keeper{
		ViewsDir: filepath.Join(realDir(t), "views"), Git: git, Events: events.NewRing(64),
		SpawnTimeout: spawnBound,
	}
	mgr.Views = keeper
	t.Cleanup(func() {
		for _, s := range store.ListSessions() {
			_ = keeper.Teardown(s.Key())
		}
	})
	if err := store.CreateWorkspace(&api.Workspace{Name: "test-view-spawn", Root: realDir(t), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, tm := range []struct {
		name string
		role api.Role
	}{
		{"slow", envRecorder(t, out, "reader", repoView)},
		{"fast", envRecorder(t, out, "plain", nil)},
	} {
		if err := store.CreateTeam(&api.Team{
			Name: tm.name, Workspace: "test-view-spawn", Roles: []api.Role{tm.role}, Generation: 1,
			ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return store, ctrl, keeper, out
}

func teamSessions(store *api.Store, team string) []api.Session {
	return store.ListSessionsByTeam("test-view-spawn", team)
}

// One team's hung view fetch does not hold another team's reconcile: the pass
// returns promptly, the other team's seat is up, and the slow team's seat is
// deferred rather than spawned or failed.
func TestHungViewFetchDoesNotStallAnotherTeamsReconcile(t *testing.T) {
	store, ctrl, keeper, _ := viewSpawnRig(t, spawnGit{hang: true}, 10*time.Second)
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		ctrl.ReconcileOnce()
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("a reconcile with a hung view fetch took more than 3s; the build holds the controller lock (spawn bound %s)", keeper.SpawnTimeout)
	}
	if n := len(teamSessions(store, "fast")); n != 1 {
		t.Errorf("the team with no views has %d sessions after one pass, want 1", n)
	}
	if n := len(teamSessions(store, "slow")); n != 0 {
		t.Errorf("the team with a hung view has %d sessions after one pass, want its spawn deferred", n)
	}
}

// With a healthy remote the seat is deferred at most a few ticks, then spawns
// with MARVEL_VIEW_REPO naming a tree that exists.
func TestSeatSpawnsWithItsViewOnceTheBuildFinishes(t *testing.T) {
	store, ctrl, keeper, out := viewSpawnRig(t, spawnGit{}, 10*time.Second)
	deadline := time.Now().Add(10 * time.Second)
	for len(teamSessions(store, "slow")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the seat with a view never spawned")
		}
		ctrl.ReconcileOnce()
		time.Sleep(50 * time.Millisecond)
	}
	name := teamSessions(store, "slow")[0].Name
	got := waitCwd(t, out, name)
	want := filepath.Join(keeper.ViewsDir, "test-view-spawn", name, "repo", "cur")
	if got != want {
		t.Errorf("MARVEL_VIEW_REPO = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, "README")); err != nil {
		t.Errorf("the view's tree is not readable through the path: %v", err)
	}
}

// A hung remote cannot hold a seat back for ever: past the spawn bound the
// seat starts without the view, and view.unavailable says so once.
func TestSeatSpawnsWithoutItsViewPastTheSpawnBound(t *testing.T) {
	store, ctrl, keeper, out := viewSpawnRig(t, spawnGit{hang: true}, 300*time.Millisecond)
	deadline := time.Now().Add(10 * time.Second)
	for len(teamSessions(store, "slow")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the seat with a hung view never spawned")
		}
		ctrl.ReconcileOnce()
		time.Sleep(50 * time.Millisecond)
	}
	name := teamSessions(store, "slow")[0].Name
	if got := waitCwd(t, out, name); got != "unset" {
		t.Errorf("MARVEL_VIEW_REPO = %q for a view that never built, want it unset", got)
	}
	var unavailable int
	for _, ev := range keeper.Events.(*events.Ring).Snapshot(events.Filter{Kind: events.KindViewUnavailable}, 0) {
		if strings.Contains(ev.Message, "repo") {
			unavailable++
		}
	}
	if unavailable != 1 {
		t.Errorf("view.unavailable events = %d, want 1", unavailable)
	}
}
