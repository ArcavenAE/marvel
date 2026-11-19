package tmuxtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Server is one test binary's tmux server, named for the package and the
// binary's pid. A nil *Server means tmux is not installed and there is nothing
// to set up or tear down.
type Server struct{ socket string }

// StartServer prepares a test binary for tests that run against tmux: it keeps
// the server and the sweep out of the operator's own /tmp/tmux-<uid>, stops the
// servers an earlier binary (one that panicked, timed out or was killed) left
// behind, and names this binary's server. A TestMain wires it to the home guard:
//
//	srv, err := tmuxtest.StartServer("team")
//	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
//	code := homeguard.RunWith(m, srv.Setup)
//	srv.Stop()
//	os.Exit(code)
//
// The TestMain calls the guard itself, because the check that every package
// with tests does so reads each TestMain's own body.
//
// `go test ./...` forks one binary per package, and different binaries are
// independent processes, not production's one-binary-many-goroutines model.
// Without a server each, every binary would churn the same system-wide tmux
// server and race its siblings. Intra-binary concurrency, the production
// pattern of many goroutines sharing one Driver, is exercised by
// tmux.TestDriverConcurrentUse. internal/tmux's own TestMain holds the full
// rationale and keeps its own copy of this helper, because its tests call
// its package-local killTestServer directly and socketpath_test.go pins the
// path derivation it uses.
func StartServer(pkg string) (*Server, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, nil
	}
	if _, err := UseScratchTmuxTmpDir(); err != nil {
		return nil, fmt.Errorf("TestMain: %v; stopping rather than run tmux in the operator's own directory", err)
	}
	SweepDead()
	return &Server{socket: fmt.Sprintf("marvel-test-%s-%d", pkg, os.Getpid())}, nil
}

// Setup points the test binary at its own server. It is the setup function the
// home guard runs after it has cleared MARVEL_* variables, and it is safe on a
// nil *Server.
func (s *Server) Setup() error {
	if s == nil {
		return nil
	}
	return os.Setenv("MARVEL_TMUX_SOCKET", s.socket)
}

// Stop kills the server and unlinks its socket; safe on a nil *Server.
func (s *Server) Stop() {
	if s != nil {
		KillTestServer(s.socket)
	}
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
