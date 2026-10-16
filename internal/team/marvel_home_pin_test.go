package team

import (
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A handoff template's "~/" is the daemon user's home, not the layout root
// (docs/design/marvel-home.md). Not parallel: it sets HOME and MARVEL_HOME.
func TestMarvelHomeDoesNotMoveAHandoffTemplateTilde(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_HOME", other)
	got, err := handoffPath("~/h/{session}.md", api.Session{Name: "s"})
	if want := filepath.Join(home, "h", "s.md"); err != nil || got != want {
		t.Errorf("handoffPath = %q, %v; want %q", got, err, want)
	}
}
