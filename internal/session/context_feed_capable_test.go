package session

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// TestCanFeedContextRole: the registry-backed predicate the apply advisory
// uses. claude renders the statusline feed and reads a projected settings
// file; codex feeds CTX% through its seeded hooks and cannot honour the
// field; an unknown command falls to the generic adapter, which cannot.
func TestCanFeedContextRole(t *testing.T) {
	skipIfNoTmux(t)
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(api.NewStore(), driver)
	tests := []struct {
		image string
		want  bool
	}{
		{"claude", true},
		{"codex", false},
		{"opencode", false},
		{"sleep", false},
	}
	for _, tt := range tests {
		r := api.ManifestRole{Name: "r", Replicas: 1, Runtime: api.ManifestRuntime{Image: tt.image, Command: tt.image, ContextFeed: api.ContextFeedStatusline}}
		if got := mgr.CanFeedContextRole(r); got != tt.want {
			t.Errorf("CanFeedContextRole(%s) = %v, want %v", tt.image, got, tt.want)
		}
	}
}
