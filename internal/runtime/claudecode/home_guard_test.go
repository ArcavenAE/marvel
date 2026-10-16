package claudecode

import (
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
)

// TestMain gives every test in the package a scratch HOME and no MARVEL_*,
// TMUX or TMUX_PANE variables; see internal/homeguard.
func TestMain(m *testing.M) { os.Exit(homeguard.Run(m)) }
