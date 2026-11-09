package company

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
)

// TestMain gives every test in the package a scratch HOME, no MARVEL_*, TMUX
// or TMUX_PANE variables, and a TMUX_TMPDIR that is the test binary's own
// directory, so no test reaches the operator's home or a live tmux server.
func TestMain(m *testing.M) {
	exe, err := os.Executable()
	if err == nil {
		_ = os.Setenv("TMUX_TMPDIR", filepath.Dir(exe))
	}
	os.Exit(homeguard.RunWith(m, nil))
}
