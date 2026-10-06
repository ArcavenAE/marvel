package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/view"
)

const viewSHA = "3333333333333333333333333333333333333333"

// viewGit is a git that always resolves to viewSHA, or fails the fetch.
type viewGit struct {
	fetchErr error
	// hang makes the fetch wait for its context, as a hung remote does.
	hang bool
}

func (g viewGit) Fetch(ctx context.Context, _, _, _ string) error {
	if g.hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return g.fetchErr
}

func (viewGit) Resolve(context.Context, string, string) (string, error) { return viewSHA, nil }

func (viewGit) Archive(_ context.Context, _, _, dest string) error {
	return os.WriteFile(filepath.Join(dest, "README"), []byte("tree"), 0o644)
}

const viewManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "reader"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"

    [[team.role.view]]
    name = "repo"
    remote = "remote"
    ref = "main"
`

func viewManager(t *testing.T, git view.Git) (*Manager, *events.Ring) {
	t.Helper()
	ring := events.NewRing(32)
	dir := filepath.Join(t.TempDir(), "views")
	mgr := &Manager{
		store:         api.NewStore(),
		adapters:      runtime.NewRegistry(),
		ProjectionDir: t.TempDir(),
		Events:        ring,
	}
	mgr.Views = &view.Keeper{ViewsDir: dir, Events: ring, Git: git}
	// The trees are read-only; only the keeper restores write to remove them.
	t.Cleanup(func() { _ = mgr.Views.Teardown("acme/squad-reader-g1-0") })
	m, err := api.ParseManifestBytes([]byte(viewManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return mgr, ring
}

// A seat is given its view at spawn: the tree is built before the command line
// is made, so MARVEL_VIEW_<NAME> names a path that already holds the tree.
func TestPlanLaunchBuildsTheViewAndNamesItInTheEnvironment(t *testing.T) {
	mgr, _ := viewManager(t, viewGit{})
	sess := sessionFor("reader", "claude")

	plan := mgr.planLaunch(sess)

	want := filepath.Join(mgr.Views.ViewsDir, sess.Key(), "repo", "cur")
	if got := plan.env["MARVEL_VIEW_REPO"]; got != want {
		t.Fatalf("MARVEL_VIEW_REPO = %q, want %q", got, want)
	}
	if b, err := os.ReadFile(filepath.Join(want, "VIEW_SHA")); err != nil || strings.TrimSpace(string(b)) != viewSHA {
		t.Fatalf("VIEW_SHA through the path = %q, %v, want the resolved commit", b, err)
	}
}

// A view that cannot be built does not fail the launch: the command line is
// made, the variable is absent, and view.unavailable says why.
func TestPlanLaunchWithoutAViewStillLaunchesAndSaysSo(t *testing.T) {
	mgr, ring := viewManager(t, viewGit{fetchErr: errors.New("network down")})
	sess := sessionFor("reader", "claude")

	plan := mgr.planLaunch(sess)

	if plan.command == "" {
		t.Fatal("a failed view build stopped the launch")
	}
	if _, set := plan.env["MARVEL_VIEW_REPO"]; set {
		t.Errorf("MARVEL_VIEW_REPO is set to %q though the build failed", plan.env["MARVEL_VIEW_REPO"])
	}
	if got := len(ring.Snapshot(events.Filter{Kind: events.KindViewUnavailable}, 0)); got != 1 {
		t.Errorf("view.unavailable events = %d, want 1", got)
	}
}

// Deleting a session removes its views, read-only trees included.
func TestDroppingASessionRemovesItsViews(t *testing.T) {
	mgr, _ := viewManager(t, viewGit{})
	sess := sessionFor("reader", "claude")
	sess.State = api.SessionRunning
	if err := mgr.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	mgr.planLaunch(sess)
	dir := filepath.Join(mgr.Views.ViewsDir, sess.Key())
	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("precondition: no views built: %v", err)
	}

	if err := mgr.dropSession(*sess, "test"); err != nil {
		t.Fatalf("dropSession: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("the session's views survive its deletion: %v", err)
	}
}

// A hung remote at spawn does not hold the launch: planLaunch returns at the
// spawn bound with the command line made and no variable set, the same as any
// other failed build.
func TestPlanLaunchWithAHungRemoteReturnsAtTheSpawnBound(t *testing.T) {
	mgr, ring := viewManager(t, viewGit{hang: true})
	mgr.Views.SpawnTimeout = 50 * time.Millisecond
	sess := sessionFor("reader", "claude")

	done := make(chan launchPlan, 1)
	go func() { done <- mgr.planLaunch(sess) }()
	select {
	case plan := <-done:
		if plan.command == "" {
			t.Fatal("a hung remote stopped the launch")
		}
		if _, set := plan.env["MARVEL_VIEW_REPO"]; set {
			t.Errorf("MARVEL_VIEW_REPO is set to %q though the build was cut off", plan.env["MARVEL_VIEW_REPO"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("planLaunch waited on a hung remote past the spawn bound")
	}
	if got := len(ring.Snapshot(events.Filter{Kind: events.KindViewUnavailable}, 0)); got != 1 {
		t.Errorf("view.unavailable events = %d, want 1", got)
	}
}
