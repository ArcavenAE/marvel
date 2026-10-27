package team

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
	"github.com/arcavenae/marvel/internal/tmux/tmuxtest"
)

// See internal/tmux/testmain_test.go for the rationale.
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("tmux"); err != nil {
		os.Exit(homeguard.Run(m))
	}
	// A binary that panicked, timed out or was killed never reached
	// killTestServer; stop the servers it left behind.
	// Keep the servers and the sweep out of the operator's own /tmp/tmux-<uid>.
	if _, err := tmuxtest.UseScratchTmuxTmpDir(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: %v; stopping rather than run tmux in the operator's own directory\n", err)
		os.Exit(1)
	}
	tmuxtest.SweepDead()
	socket := fmt.Sprintf("marvel-test-team-%d", os.Getpid())
	code := homeguard.RunWith(m, func() error { return os.Setenv("MARVEL_TMUX_SOCKET", socket) })
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
