package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// movedTwice builds a seat's view at one commit and moves it to another, so the
// first is superseded.
func movedTwice(t *testing.T) (*kRig, api.Session) {
	t.Helper()
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := r.session("seat", views...)
	r.k.Build(sess, views)
	r.g.sha["a"] = shaTwo
	r.now = r.now.Add(time.Hour)
	r.k.Tick()
	if cur, _ := r.curOf(sess, "alpha"); !strings.HasSuffix(cur, shaTwo) {
		t.Fatalf("cur = %q, want it on %s", cur, shaTwo)
	}
	return r, sess
}

func (r *kRig) treeDir(sess api.Session, view, sha string) string {
	return filepath.Join(r.k.ViewsDir, sess.Key(), view, "trees", sha)
}

// The keeper seals the commits it is told to, in the named view, and no others.
func TestKeeperSealsTheNamedSupersededTree(t *testing.T) {
	r, sess := movedTwice(t)
	if err := r.k.Seal(sess, "alpha", []string{shaOne}); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.treeDir(sess, "alpha", shaOne), "sub")); err == nil {
		t.Error("the superseded tree is still readable after the seal")
	}
	if got := mustRead(t, filepath.Join(r.k.ViewsDir, sess.Key(), "alpha", "cur", "sub", "file.txt")); got != "tree "+shaTwo {
		t.Errorf("the current tree = %q", got)
	}
}

// A commit list comes from stored team state, so a name that climbs out of the
// view's trees, or names another view, is refused and nothing is touched.
func TestKeeperSealRefusesNamesOutsideTheViewsTrees(t *testing.T) {
	r, sess := movedTwice(t)
	for _, name := range []string{"../../alpha/cur", "..", shaOne + "/..", "cur"} {
		if err := r.k.Seal(sess, "alpha", []string{name}); err == nil {
			t.Errorf("Seal(%q) succeeded", name)
		}
	}
	if err := r.k.Seal(sess, "../alpha", []string{shaOne}); err == nil {
		t.Error("a view name that climbs out was accepted")
	}
	if _, err := os.Stat(filepath.Join(r.treeDir(sess, "alpha", shaOne), "sub", "file.txt")); err != nil {
		t.Errorf("a refused seal touched the tree: %v", err)
	}
}

// Teardown removes sealed trees with the rest of the seat's directory.
func TestTeardownRemovesSealedTrees(t *testing.T) {
	r, sess := movedTwice(t)
	if err := r.k.Seal(sess, "alpha", []string{shaOne}); err != nil {
		t.Fatal(err)
	}
	if err := r.k.Teardown(sess.Key()); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(r.k.ViewsDir, sess.Key())); !os.IsNotExist(err) {
		t.Errorf("the seat's views directory is still there: %v", err)
	}
}

// Past MaxHeldTrees readable superseded trees the view's refresh pauses and the
// supervisor is told once; it resumes when a tree is sealed.
func TestKeeperPausesRefreshPastTheHeldTreeBound(t *testing.T) {
	r, sess := movedTwice(t)
	held := MaxHeldTrees
	r.k.Held = func(s api.Session, view string) int {
		if s.Key() != sess.Key() || view != "alpha" {
			t.Errorf("Held asked about %s/%s", s.Key(), view)
		}
		return held
	}
	third := strings.Repeat("3", 40)
	r.g.sha["a"] = third

	for i := 0; i < 3; i++ {
		r.now = r.now.Add(time.Hour)
		r.k.Tick()
	}
	if cur, _ := r.curOf(sess, "alpha"); !strings.HasSuffix(cur, shaTwo) {
		t.Fatalf("a held view moved to %q", cur)
	}
	evs := r.kinds(events.KindViewRetentionHeld)
	if len(evs) != 1 {
		t.Fatalf("view.retention-held events = %d over three paused ticks, want 1", len(evs))
	}
	if evs[0].Severity != events.SeverityWarning || evs[0].Session != sess.Key() || !strings.Contains(evs[0].Message, "alpha") {
		t.Errorf("event = %+v", evs[0])
	}

	lines, err := r.k.Refresh(sess.Key(), "alpha")
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], "held") {
		t.Errorf("a refresh request on a held view = %v, %v, want a held line", lines, err)
	}
	if cur, _ := r.curOf(sess, "alpha"); !strings.HasSuffix(cur, shaTwo) {
		t.Errorf("a refresh request moved a held view to %q", cur)
	}

	held = MaxHeldTrees - 1
	r.now = r.now.Add(time.Hour)
	r.k.Tick()
	if cur, _ := r.curOf(sess, "alpha"); !strings.HasSuffix(cur, third) {
		t.Fatalf("cur = %q after release, want it on %s", cur, third)
	}

	held = MaxHeldTrees
	r.g.sha["a"] = shaOne
	for i := 0; i < 2; i++ {
		r.now = r.now.Add(time.Hour)
		r.k.Tick()
	}
	if n := len(r.kinds(events.KindViewRetentionHeld)); n != 2 {
		t.Errorf("view.retention-held events = %d after a second hold, want 2", n)
	}
}

// Below the bound nothing changes: the view follows its ref.
func TestKeeperFollowsTheRefBelowTheHeldTreeBound(t *testing.T) {
	r, sess := movedTwice(t)
	r.k.Held = func(api.Session, string) int { return MaxHeldTrees - 1 }
	r.g.sha["a"] = strings.Repeat("3", 40)
	r.now = r.now.Add(time.Hour)
	r.k.Tick()
	if cur, _ := r.curOf(sess, "alpha"); !strings.HasSuffix(cur, r.g.sha["a"]) {
		t.Errorf("cur = %q, want it on the new commit", cur)
	}
	if n := len(r.kinds(events.KindViewRetentionHeld)); n != 0 {
		t.Errorf("view.retention-held events = %d below the bound", n)
	}
}

// A seat's first build is never held: a stale record from an earlier seat that
// held the same key must not keep a new seat without its view.
func TestKeeperBuildsAtSpawnEvenWhenTheHoldIsFull(t *testing.T) {
	r := newKRig(t)
	r.k.Held = func(api.Session, string) int { return MaxHeldTrees + 3 }
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := r.session("seat", views...)
	r.k.Build(sess, views)
	if cur, ok := r.curOf(sess, "alpha"); !ok || !strings.HasSuffix(cur, shaOne) {
		t.Errorf("cur = %q, %v; the spawn build was held", cur, ok)
	}
	if n := len(r.kinds(events.KindViewRetentionHeld)); n != 0 {
		t.Errorf("view.retention-held events = %d at spawn", n)
	}
}
