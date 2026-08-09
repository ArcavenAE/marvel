package tmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// tmuxSocketPathMatchesTmux guards the socket-path derivation that
// killTestServer relies on to unlink a dead server's socket.
//
// The derivation is a copy of tmux's own layout rule, so it can silently
// drift from the installed tmux. A wrong path fails safe (nothing is
// removed) and therefore fails silently: the leak would come back with no
// test turning red. Asking a live server where its socket is closes that
// gap.
func TestTmuxSocketPathMatchesTmux(t *testing.T) {
	skipIfNoTmux(t)

	// A short TMUX_TMPDIR on purpose. A unix socket path is capped near
	// 104 bytes, and t.TempDir() under a long working path pushes tmux
	// past it: every connect then fails with "File name too long", which
	// looks exactly like a clean run.
	dir, err := os.MkdirTemp("/tmp", "mxsp")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)

	socket := "mxsockpath"
	cmd := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", "probe", "sleep 30")
	cmd.Env = append(os.Environ(), "TMUX_TMPDIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start tmux server: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })

	query := exec.Command("tmux", "-L", socket, "display-message", "-p", "#{socket_path}")
	query.Env = append(os.Environ(), "TMUX_TMPDIR="+dir)
	out, err := query.Output()
	if err != nil {
		t.Fatalf("ask tmux for its socket path: %v", err)
	}
	reported := strings.TrimSpace(string(out))

	derived := tmuxSocketPath(socket)
	if !sameFile(t, derived, reported) {
		t.Errorf("derived socket path %q is not tmux's %q", derived, reported)
	}
}

// sameFile compares two paths by identity rather than by string, because
// macOS reports /private/tmp where the caller passed /tmp.
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		t.Logf("stat %s: %v", a, err)
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Logf("stat %s: %v", b, err)
		return false
	}
	return os.SameFile(fa, fb)
}
