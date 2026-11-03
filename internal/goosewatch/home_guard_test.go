package goosewatch

import (
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
)

// TestMain gives every test in the package a scratch HOME and no MARVEL_*,
// TMUX or TMUX_PANE variables; see internal/homeguard. A re-exec of the test
// binary as a guard or wrapper child is handled first (reexec_test.go).
func TestMain(m *testing.M) {
	reexecMain()
	os.Exit(homeguard.Run(m))
}
