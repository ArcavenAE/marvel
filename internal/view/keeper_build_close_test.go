package view

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// marvel#1121: the spawn's Build ran under its own context, outside the builds
// Close counts, so a spawn in progress at shutdown kept fetching after Close
// returned. Close cancels it and waits for it.
func TestCloseStopsAndWaitsForABuildInFlight(t *testing.T) {
	k, g, sess := refreshKeeper(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		k.Build(sess, []api.View{kView("alpha", "a", 10*time.Minute)})
	}()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the build never reached the fetch")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		k.Close()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s with a spawn build on a hung remote")
	}
	if n := g.active(); n != 0 {
		t.Fatalf("%d fetch(es) still in flight after Close returned; the build can still write under %s", n, k.ViewsDir)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Build did not return within 5s of Close")
	}
}

// A Build after Close runs no fetch.
func TestBuildAfterCloseRunsNothing(t *testing.T) {
	k, g, sess := refreshKeeper(t)
	k.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		k.Build(sess, []api.View{kView("alpha", "a", 10*time.Minute)})
	}()
	select {
	case <-done:
	case <-g.entered:
		t.Fatal("Build after Close started a fetch")
	case <-time.After(5 * time.Second):
		t.Fatal("Build after Close did not return within 5s")
	}
	if n := g.active(); n != 0 {
		t.Errorf("Build after Close left %d fetch(es) in flight", n)
	}
}

// marvel#1121: a refresh queued behind a view's held slot waits for it under
// the keeper's lifetime, so Close lets it go with errClosed. A blocking send on
// the slot would hold Close forever, since the refresh is counted in builds.
// The Declared hook fires once the refresh is counted, so Close cannot run
// before it.
func TestCloseLetsGoARefreshQueuedBehindAHeldSlot(t *testing.T) {
	g := &hungGit{entered: make(chan struct{})}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionRunning}
	v := kView("alpha", "a", 10*time.Minute)
	asked := make(chan struct{})
	var once sync.Once
	k := &Keeper{
		ViewsDir:     filepath.Join(t.TempDir(), "views"),
		Git:          g,
		Events:       events.NewRing(16),
		SpawnTimeout: time.Minute,
		FetchTimeout: time.Minute,
		Declared: func() []Declaration {
			once.Do(func() { close(asked) })
			return []Declaration{{Session: sess, Views: []api.View{v}}}
		},
	}
	// Hold the view's slot, as a tick or verb in flight would, and give it back
	// at the end so nothing is left blocked on it.
	held := k.track(sess, v)
	held.run <- struct{}{}
	t.Cleanup(held.release)

	refreshed := make(chan error, 1)
	go func() {
		_, err := k.Refresh(sess.Key(), "alpha")
		refreshed <- err
	}()
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never looked up its seat")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		k.Close()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s with a refresh queued behind a held slot")
	}
	select {
	case err := <-refreshed:
		if !errors.Is(err, errClosed) {
			t.Errorf("queued Refresh at Close returned %v, want errClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued Refresh did not return within 5s of Close")
	}
	select {
	case <-g.entered:
		t.Error("the queued Refresh reached the fetch")
	default:
	}
}
