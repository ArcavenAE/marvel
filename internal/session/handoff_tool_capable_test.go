package session

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// TestCanOfferHandoffToolRole: the registry-backed predicate apply uses to
// refuse handoff_via = "tool". codex has a seeder that renders the marvel-handoff
// entry. opencode is refused until section 5 check 2 of the design note has run,
// as crush is; claude is not seeded with it, and an unknown command (crush among
// them) falls to the generic adapter, which has no way to be given it.
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
		{"opencode", false},
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

// The role's image names its harness when it has one, and the command only when
// it has no image: a role that declares both is asked of the image, as the
// manager resolves it for a launch.
func TestCanOfferHandoffToolRoleReadsTheImageBeforeTheCommand(t *testing.T) {
	skipIfNoTmux(t)
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(api.NewStore(), driver)
	for _, tt := range []struct {
		name           string
		image, command string
		want           bool
	}{
		{"image says codex, command is another binary", "codex", "/usr/local/bin/wrapper", true},
		{"no image, command says codex", "", "codex", true},
		{"image says claude, command says codex", "claude", "codex", false},
		{"image says codex, command says sleep", "codex", "sleep", true},
	} {
		r := api.ManifestRole{Name: "r", Replicas: 1, Runtime: api.ManifestRuntime{Image: tt.image, Command: tt.command}}
		if got := mgr.CanOfferHandoffToolRole(r); got != tt.want {
			t.Errorf("%s: CanOfferHandoffToolRole = %v, want %v", tt.name, got, tt.want)
		}
	}
}
