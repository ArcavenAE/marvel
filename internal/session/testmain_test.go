package session

import (
	"os"
	"syscall"
	"testing"

	"github.com/arcavenae/marvel/internal/tmux/tmuxtest"
)

// One tmux server for this test binary; see tmuxtest.RunMain.
func TestMain(m *testing.M) {
	// The projection, overlay and stream directories must be private to the
	// user before anything is written in them (aae-orc-bc107), and t.TempDir
	// makes its directories 0777 less the umask. A 0077 umask makes every
	// scratch directory these tests make 0700; the tests that need a wider or
	// foreign directory set that up themselves.
	syscall.Umask(0o077)
	os.Exit(tmuxtest.RunMain(m, "session"))
}
