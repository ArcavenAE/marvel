package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A window one workspace's seat taught is served to that workspace only. The
// learned rung otherwise lets a single seat set the denominator other
// workspaces' sessions are measured against.
func TestLearnedWindowIsScopedToItsWorkspace(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{})
	r.Learn("alpha", "claude-haiku-4-5", 180_000, api.BackendDefault)

	if w, ok := r.Learned("alpha", "claude-haiku-4-5", api.BackendDefault); !ok || w != 180_000 {
		t.Fatalf("Learned(alpha) = %d, %v; want 180000, true", w, ok)
	}
	if _, ok := r.Learned("beta", "claude-haiku-4-5", api.BackendDefault); ok {
		t.Fatal("a window learned in alpha is visible in beta")
	}

	got, src, _ := r.Resolve(Request{Workspace: "alpha", Harness: "claude", StreamModel: "claude-haiku-4-5", Redirection: api.BackendDefault})
	if got != 180_000 || src != LimitLearned {
		t.Fatalf("alpha resolves %d from %q, want 180000 from learned", got, src)
	}
	got, src, _ = r.Resolve(Request{Workspace: "beta", Harness: "claude", StreamModel: "claude-haiku-4-5", Redirection: api.BackendDefault})
	if src == LimitLearned {
		t.Fatalf("beta resolved %d from the learned rung, want a window from outside alpha's entry", got)
	}
}

func TestLearnRefusesAWindowOutsideThePlausibleRange(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{})
	for name, w := range map[string]int{
		"one below the floor":   MinLearnedWindow - 1,
		"one above the ceiling": MaxLearnedWindow + 1,
	} {
		r.Learn("ws", "claude-haiku-4-5", w, api.BackendDefault)
		if _, ok := r.Learned("ws", "claude-haiku-4-5", api.BackendDefault); ok {
			t.Errorf("%s: a window of %d was learned", name, w)
		}
	}
	for name, w := range map[string]int{"at the floor": MinLearnedWindow, "at the ceiling": MaxLearnedWindow} {
		r.Learn("ws", "claude-haiku-4-5", w, api.BackendDefault)
		if got, ok := r.Learned("ws", "claude-haiku-4-5", api.BackendDefault); !ok || got != w {
			t.Errorf("%s: Learned = %d, %v; want %d, true", name, got, ok, w)
		}
	}
}

func TestClearDropsOnlyOneWorkspacesLearnedWindows(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{})
	r.Learn("alpha", "claude-haiku-4-5", 180_000, api.BackendDefault)
	r.Learn("alpha", "claude-sonnet-4-6", 190_000, api.BackendRedirected)
	r.Learn("beta", "claude-haiku-4-5", 170_000, api.BackendDefault)

	if n := r.Clear("alpha"); n != 2 {
		t.Fatalf("Clear(alpha) = %d, want 2", n)
	}
	if _, ok := r.Learned("alpha", "claude-haiku-4-5", api.BackendDefault); ok {
		t.Error("alpha's window survived Clear")
	}
	if w, ok := r.Learned("beta", "claude-haiku-4-5", api.BackendDefault); !ok || w != 170_000 {
		t.Errorf("Learned(beta) = %d, %v; want 170000, true: Clear(alpha) must not touch beta", w, ok)
	}
	if n := r.Clear("alpha"); n != 0 {
		t.Errorf("a second Clear(alpha) = %d, want 0", n)
	}
}
