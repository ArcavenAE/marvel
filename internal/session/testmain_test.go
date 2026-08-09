package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	killTestServer(socket)
	os.Exit(code)
}

// killTestServer stops this package's tmux server and unlinks its socket.
// tmux leaves the socket behind when the server goes away, and the name
// carries this binary's pid, so without the unlink every run leaks one.
// See tmux/testmain_test.go for the full rationale and the test that keeps
// the path derivation honest.
func killTestServer(socket string) {
	path := ""
	if out, err := exec.Command("tmux", "-L", socket, "display-message", "-p", "#{socket_path}").Output(); err == nil {
		path = strings.TrimSpace(string(out))
	}
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	if path == "" {
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		path = filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)
	}
	_ = os.Remove(path)
}
