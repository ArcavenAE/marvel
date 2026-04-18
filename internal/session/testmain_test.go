package session

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// See tmux/testmain_test.go for the rationale — per-package tmux
// server isolation so parallel `go test ./...` binaries don't race on
// the system-wide tmux server. Prod-style intra-process concurrency
// is covered by tmux.TestDriverConcurrentUse.
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("tmux"); err != nil {
		os.Exit(m.Run())
	}
	socket := fmt.Sprintf("marvel-test-session-%d", os.Getpid())
	if err := os.Setenv("MARVEL_TMUX_SOCKET", socket); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set MARVEL_TMUX_SOCKET: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	os.Exit(code)
}
