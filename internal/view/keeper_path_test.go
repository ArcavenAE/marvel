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

// A session key is workspace/name and a view name is one path element, both
// joined under the views directory. A name that is not one clean element would
// put the join, and the recursive remove at teardown, outside that directory.

// victimBeside makes a directory next to the keeper's views directory, which no
// keeper operation may touch, and returns its file.
func victimBeside(t *testing.T, r *kRig, name string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(r.k.ViewsDir), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(f, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func mustExist(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s: %v", why, err)
	}
}

func TestTeardownRefusesAKeyOutsideTheViewsDir(t *testing.T) {
	r := newKRig(t)
	victim := victimBeside(t, r, "victim")
	if err := os.MkdirAll(r.k.ViewsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"../victim", "ws/..", "..", ".", "", "ws", "ws/", "/ws", "a/b/c", `ws\seat`, `..\victim`, "ws/seat/..",
	} {
		err := r.k.Teardown(key)
		if err == nil {
			t.Errorf("Teardown(%q) = nil, want a refusal", key)
		}
		mustExist(t, victim, "Teardown("+key+") removed a directory outside the views dir")
		mustExist(t, r.k.ViewsDir, "Teardown("+key+") removed the views dir")
	}
	if n := len(r.kinds(events.KindViewUnavailable)); n == 0 {
		t.Error("a refused teardown emitted no event")
	}
}

// Control: a clean key still tears down its own directory.
func TestTeardownStillRemovesACleanKey(t *testing.T) {
	r := newKRig(t)
	dir := filepath.Join(r.k.ViewsDir, "ws", "seat")
	if err := os.MkdirAll(filepath.Join(dir, "v"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.k.Teardown("ws/seat"); err != nil {
		t.Fatalf("Teardown(ws/seat) = %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("the seat's views dir is still there: %v", err)
	}
}

func TestBuildRefusesASessionKeyOutsideTheViewsDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		sess api.Session
		view string
	}{
		{"workspace dot-dot", api.Session{Workspace: "..", Name: "escape", Team: "t", Role: "r"}, "v"},
		{"workspace with slash", api.Session{Workspace: "a/../..", Name: "escape", Team: "t", Role: "r"}, "v"},
		{"view with slash", api.Session{Workspace: "ws", Name: "seat", Team: "t", Role: "r"}, "../../../escape"},
		{"view dot-dot", api.Session{Workspace: "ws", Name: "seat", Team: "t", Role: "r"}, ".."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newKRig(t)
			r.g.sha["a"] = shaOne
			sess := tc.sess
			sess.State = api.SessionRunning
			views := []api.View{kView(tc.view, "a", 10*time.Minute)}
			r.k.Build(sess, views)

			parent := filepath.Dir(r.k.ViewsDir)
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name() != filepath.Base(r.k.ViewsDir) {
					t.Errorf("a build wrote %q outside the views dir", filepath.Join(parent, e.Name()))
				}
			}
			if r.g.archives != 0 {
				t.Errorf("git archive ran %d times for a refused view", r.g.archives)
			}
			if len(r.kinds(events.KindViewUnavailable)) == 0 {
				t.Error("a refused view emitted no event")
			}
		})
	}
}

// Control: ordinary names still build.
func TestBuildStillBuildsOrdinaryNames(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := r.session("seat", views...)
	r.k.Build(sess, views)
	if _, ok := r.curOf(sess, "alpha"); !ok {
		t.Error("an ordinary view was not built")
	}
	if strings.Contains(strings.Join(eventMessages(r.ring), "\n"), "refused") {
		t.Error("an ordinary view was refused")
	}
}

func eventMessages(r *events.Ring) []string {
	var out []string
	for _, e := range r.Snapshot(events.Filter{}, 0) {
		out = append(out, e.Message)
	}
	return out
}
