package runtime

import (
	"path/filepath"
	"testing"
)

// codex's own home is codex's, so a layout root does not move where a seat's
// codex session is seeded from. Not parallel: it sets HOME, CODEX_HOME and
// MARVEL_HOME.
func TestMarvelHomeDoesNotMoveTheCodexSource(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("MARVEL_HOME", other)
	spec, ok := (&Codex{}).SessionHome(testContext())
	if !ok {
		t.Fatal("codex declares no session home")
	}
	if want := filepath.Join(home, ".codex"); spec.Source != want {
		t.Errorf("codex source = %q, want %q", spec.Source, want)
	}
}
