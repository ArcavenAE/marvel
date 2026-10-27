package view

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// refreshKeeper is a keeper on a hung remote whose one running seat declares
// one view, so the view.refresh verb can reach it.
func refreshKeeper(t *testing.T) (*Keeper, *hungGit, api.Session) {
	t.Helper()
	g := &hungGit{entered: make(chan struct{})}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionRunning}
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	k := &Keeper{
		ViewsDir:     filepath.Join(t.TempDir(), "views"),
		Git:          g,
		Events:       events.NewRing(16),
		SpawnTimeout: time.Minute,
		FetchTimeout: time.Minute,
		Declared:     func() []Declaration { return []Declaration{{Session: sess, Views: views}} },
	}
	return k, g, sess
}

// marvel#1119: a view.refresh ran under its own context, outside the builds
// Close counts, so a refresh in flight at shutdown kept fetching after Close
// returned and could write once teardown had removed the seat's directory.
// Close cancels it and waits for it.
func TestCloseStopsAndWaitsForARefreshInFlight(t *testing.T) {
	k, g, sess := refreshKeeper(t)
	go func() { _, _ = k.Refresh(sess.Key(), "alpha") }()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never reached the fetch")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		k.Close()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s with a refresh on a hung remote")
	}
	if n := g.active(); n != 0 {
		t.Fatalf("%d fetch(es) still in flight after Close returned; the refresh can still write under %s", n, k.ViewsDir)
	}
}

// A view.refresh after Close is refused and runs no build.
func TestRefreshAfterCloseIsRefusedAndRunsNothing(t *testing.T) {
	k, g, sess := refreshKeeper(t)
	k.Close()

	type result struct {
		lines []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		lines, err := k.Refresh(sess.Key(), "alpha")
		done <- result{lines, err}
	}()
	select {
	case r := <-done:
		if r.err == nil {
			t.Errorf("Refresh after Close = %q, nil error; want it refused", r.lines)
		}
	case <-g.entered:
		t.Fatal("Refresh after Close started a fetch")
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh after Close did not return within 5s")
	}
	if n := g.active(); n != 0 {
		t.Errorf("Refresh after Close left %d fetch(es) in flight", n)
	}
}
