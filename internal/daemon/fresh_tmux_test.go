package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// useFreshTmuxServer points this test at its own tmux server, named socket,
// and stops that server when the test ends. The socket file must go too:
// tmux leaves it behind, and the name carries the pid, so a server stopped
// without the unlink leaks one file per run (marvel#409).
func useFreshTmuxServer(t *testing.T, socket string) {
	t.Helper()
	t.Setenv("MARVEL_TMUX_SOCKET", socket)
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
}

// TestFreshTmuxServerLeavesNoSocket starts a server under the helper in a
// subtest, so the helper's cleanup has run by the time the subtest returns,
// then checks the socket file is gone.
func TestFreshTmuxServerLeavesNoSocket(t *testing.T) {
	skipIfNoTmux(t)
	socket := fmt.Sprintf("marvel-test-fresh-%d", os.Getpid())
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	path := filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)

	t.Run("server", func(t *testing.T) {
		useFreshTmuxServer(t, socket)
		if out, err := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", "probe", "sleep 30").CombinedOutput(); err != nil {
			t.Fatalf("start tmux server: %v: %s", err, out)
		}
		// Positive control: the socket exists while the server runs.
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("socket %s not present while the server runs: %v", path, err)
		}
	})

	_, err := os.Stat(path)
	if !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(path)
		t.Errorf("socket %s still present after the test's cleanup (stat err: %v)", path, err)
	}
}
