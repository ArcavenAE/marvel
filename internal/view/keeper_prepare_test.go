package view

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// prepared waits for Prepare to report the seat ready, polling because the
// build runs in the background.
func (r *kRig) prepared(sess api.Session, views []api.View) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !r.k.Prepare(sess, views) {
		if time.Now().After(deadline) {
			r.t.Fatal("Prepare never reported the seat ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *kRig) fetchCount(remote string) int {
	r.g.mu.Lock()
	defer r.g.mu.Unlock()
	return r.g.fetches[remote]
}

// Prepare builds off the caller and Build then binds what is there: one fetch
// in all, and the tree is present when the seat spawns.
func TestPrepareBuildsInTheBackgroundAndBuildFetchesNothingMore(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}

	r.prepared(sess, views)
	r.k.Build(sess, views)

	if got, ok := r.curOf(sess, "alpha"); !ok || got != filepath.Join("trees", shaOne) {
		t.Errorf("cur = %q (present %v), want trees/%s", got, ok, shaOne)
	}
	if n := r.fetchCount("a"); n != 1 {
		t.Errorf("fetches = %d, want 1: the spawn must not fetch again", n)
	}
}

// Prepare never waits for a remote. Once the spawn bound has run since the
// first ask it reports ready, says view.unavailable once, and the seat starts
// without the view.
func TestPrepareDoesNotWaitForAHungRemote(t *testing.T) {
	r := newKRig(t)
	r.k.SpawnTimeout = time.Second
	r.g.sha["a"] = shaOne
	gate := make(chan struct{})
	r.g.block["a"] = gate
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}
	// Release the fetch, wait for the background build to finish writing, then
	// remove what it wrote, before the temp directory goes.
	t.Cleanup(func() {
		close(gate)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			r.k.mu.Lock()
			done := true
			for _, tr := range r.k.tracked {
				done = done && tr.tried
			}
			r.k.mu.Unlock()
			if done {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = r.k.Teardown(sess.Key())
	})

	start := time.Now()
	if r.k.Prepare(sess, views) {
		t.Fatal("Prepare reported ready with the fetch still hanging inside the bound")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Prepare took %s, want it to return at once", d)
	}

	r.now = r.now.Add(2 * time.Second)
	if !r.k.Prepare(sess, views) {
		t.Fatal("Prepare still holds the seat past the spawn bound")
	}
	if !r.k.Prepare(sess, views) {
		t.Fatal("Prepare held the seat on a repeat call")
	}
	if n := len(r.kinds(events.KindViewUnavailable)); n != 1 {
		t.Errorf("view.unavailable events = %d, want 1", n)
	}
	if _, ok := r.curOf(sess, "alpha"); ok {
		t.Error("a view that never built has a cur")
	}
}

// A view that failed is done trying: the seat is let go, and the failure was
// said once.
func TestPrepareLetsASeatGoWhenTheBuildFailed(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	r.g.fetchErr["a"] = errors.New("network down")
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}

	r.prepared(sess, views)
	r.k.Build(sess, views)

	if n := len(r.kinds(events.KindViewUnavailable)); n != 1 {
		t.Errorf("view.unavailable events = %d, want 1", n)
	}
	if n := r.fetchCount("a"); n != 1 {
		t.Errorf("fetches = %d, want 1", n)
	}
}

// A view prepared for a seat that does not exist yet survives the tick and its
// sweep, and is cleaned up once nothing has asked for it for prepareTTL.
func TestPreparedViewSurvivesTheTickUntilItsTTL(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}
	r.prepared(sess, views)

	r.k.Tick()
	if _, ok := r.curOf(sess, "alpha"); !ok {
		t.Fatal("the tick removed a prepared view whose seat has not spawned yet")
	}

	r.now = r.now.Add(prepareTTL + time.Second)
	r.k.Tick()
	if _, err := os.Lstat(filepath.Join(r.k.ViewsDir, sess.Key())); !os.IsNotExist(err) {
		t.Errorf("an unclaimed prepared view outlived its TTL (err %v)", err)
	}
}

// Asking again keeps a prepared view alive: the TTL runs from the last ask.
func TestPreparedViewIsKeptWhileTheControllerKeepsAsking(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	r.g.block["a"] = nil
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}
	r.prepared(sess, views)

	r.now = r.now.Add(prepareTTL - time.Minute)
	r.k.Prepare(sess, views)
	r.now = r.now.Add(prepareTTL - time.Minute)
	r.k.Tick()
	if _, ok := r.curOf(sess, "alpha"); !ok {
		t.Fatal("a view the controller kept asking about was swept")
	}
}

// The spawn bound is inclusive: a seat is let go at exactly the bound and held a
// moment before it.
func TestPrepareLetsTheSeatGoAtExactlyTheSpawnBound(t *testing.T) {
	r := newKRig(t)
	r.k.SpawnTimeout = time.Minute
	r.g.sha["a"] = shaOne
	gate := make(chan struct{})
	r.g.block["a"] = gate
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}
	t.Cleanup(func() {
		close(gate)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			r.k.mu.Lock()
			done := true
			for _, tr := range r.k.tracked {
				done = done && tr.tried
			}
			r.k.mu.Unlock()
			if done {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = r.k.Teardown(sess.Key())
	})

	if r.k.Prepare(sess, views) {
		t.Fatal("ready at the first ask with the fetch hanging")
	}
	r.now = r.now.Add(time.Minute - time.Nanosecond)
	if r.k.Prepare(sess, views) {
		t.Fatal("ready a nanosecond before the bound")
	}
	r.now = r.now.Add(time.Nanosecond)
	if !r.k.Prepare(sess, views) {
		t.Fatal("still held at exactly the bound")
	}
}
