package session

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// TestCanOfferHandoffToolRole: the registry-backed predicate apply uses to
// refuse handoff_via = "tool". codex has a seeder that renders the marvel-handoff
// entry and opencode takes it from its role's environment; claude is not
// seeded with it, and an unknown command (crush among them) falls to the
// generic adapter, which has no way to be given it.
func TestCanOfferHandoffToolRole(t *testing.T) {
	skipIfNoTmux(t)
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(api.NewStore(), driver)
	for _, tt := range []struct {
		image string
		want  bool
	}{
		{"codex", true},
		{"opencode", true},
		{"claude", false},
		{"crush", false},
		{"sleep", false},
	} {
		r := api.ManifestRole{Name: "r", Replicas: 1, Runtime: api.ManifestRuntime{Image: tt.image, Command: tt.image}}
		if got := mgr.CanOfferHandoffToolRole(r); got != tt.want {
			t.Errorf("CanOfferHandoffToolRole(%s) = %v, want %v", tt.image, got, tt.want)
		}
	}
}
