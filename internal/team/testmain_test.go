package team

import (
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/tmux/tmuxtest"
)

// One tmux server for this test binary; see tmuxtest.RunMain.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.RunMain(m, "team"))
}
