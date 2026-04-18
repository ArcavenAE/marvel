package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// TestMain isolates this test binary on a dedicated tmux server
// (tmux -L <socket>). `go test ./...` forks one binary per package,
// and different binaries are independent processes — not production's
// one-binary-many-goroutines model. Without isolation each binary
// would churn the same system-wide tmux server, racing against siblings.
//
// Intra-binary concurrency (the actual prod pattern — many goroutines
// sharing one Driver) is exercised by TestDriverConcurrentUse.
//
// If tmux isn't installed, the individual tests skip themselves; no
// socket to set up and nothing to tear down.
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("tmux"); err != nil {
		os.Exit(m.Run())
	}
	socket := fmt.Sprintf("marvel-test-tmux-%d", os.Getpid())
	if err := os.Setenv("MARVEL_TMUX_SOCKET", socket); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set MARVEL_TMUX_SOCKET: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	os.Exit(code)
}
