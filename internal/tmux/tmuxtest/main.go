package tmuxtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
)

// RunMain is the TestMain body for a package whose tests run against a tmux
// server. It gives the test binary a tmux server of its own (tmux -L <socket>,
// named for the package and the binary's pid), keeps that server and the sweep
// out of the operator's own tmux-<uid> directory, stops the servers an earlier
// binary left behind, and removes the socket on the way out. A TestMain calls
// os.Exit(tmuxtest.RunMain(m, "team")).
//
// `go test ./...` forks one binary per package, and different binaries are
// independent processes, not production's one-binary-many-goroutines model.
// Without a server each, every binary would churn the same system-wide tmux
// server and race its siblings. Intra-binary concurrency, the production
// pattern of many goroutines sharing one Driver, is exercised by
// tmux.TestDriverConcurrentUse. The rationale is the one in
// internal/tmux/testmain_test.go, which keeps its own copy because it cannot
// import this package's callers.
//
// If tmux is not installed the individual tests skip themselves; there is no
// socket to set up and nothing to tear down.
func RunMain(m *testing.M, pkg string) int {
	if _, err := exec.LookPath("tmux"); err != nil {
		return homeguard.Run(m)
	}
	// A binary that panicked, timed out or was killed never reached
	// KillTestServer; stop the servers it left behind. Keep the servers and the
	// sweep out of the operator's own /tmp/tmux-<uid>.
	if _, err := UseScratchTmuxTmpDir(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: %v; stopping rather than run tmux in the operator's own directory\n", err)
		return 1
	}
	SweepDead()
	socket := fmt.Sprintf("marvel-test-%s-%d", pkg, os.Getpid())
	code := homeguard.RunWith(m, func() error { return os.Setenv("MARVEL_TMUX_SOCKET", socket) })
	KillTestServer(socket)
	return code
}

// KillTestServer stops a test binary's tmux server and unlinks its socket.
// tmux leaves the socket behind when the server goes away, and the name carries
// the binary's pid, so without the unlink every run leaks one. internal/tmux's
// TestMain holds the full rationale and the test that keeps the path derivation
// honest.
func KillTestServer(socket string) {
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
