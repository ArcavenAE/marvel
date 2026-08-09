package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	killTestServer(socket)
	os.Exit(code)
}

// killTestServer stops the package's dedicated tmux server and unlinks its
// socket file.
//
// tmux never removes the socket when its server goes away, and the socket
// name carries the test binary's pid, so it is never reused. Every `go test`
// run therefore left one dead socket per package behind: 695 stale
// marvel-test-* sockets had accumulated in one dev box's socket directory.
//
// The unlink cannot depend on a live server. A tmux server exits on its own
// once the last session is destroyed, so by the time this runs the server is
// usually already gone and both display-message and kill-server fail. The
// derived path is the load-bearing part; the query is only a cross-check for
// an unusual layout, and tmuxSocketPathMatchesTmux keeps the derivation
// honest against the installed tmux.
func killTestServer(socket string) {
	path := ""
	if out, err := exec.Command("tmux", "-L", socket, "display-message", "-p", "#{socket_path}").Output(); err == nil {
		path = strings.TrimSpace(string(out))
	}
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	if path == "" {
		path = tmuxSocketPath(socket)
	}
	_ = os.Remove(path)
}

// tmuxSocketPath mirrors tmux's socket layout: TMUX_TMPDIR (defaulting to
// /tmp), then a tmux-<uid> directory, then the -L name.
func tmuxSocketPath(socket string) string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)
}
