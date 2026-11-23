//go:build darwin

package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

// Where `ps` cannot run (a sandboxed seat, a run under sandbox-exec), the
// process list is still readable from the kernel, so a missing socket with no
// server alive is still absence and a live server is still seen. The tests take
// `ps` away by pointing PATH at an empty directory, so they do not depend on
// being run inside a sandbox.

func withoutPS(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// No tmux server for the name is alive and ps cannot run: the answer is absence.
func TestAbsenceIsReadWithoutPS(t *testing.T) {
	d := &Driver{binary: "/nonexistent/tmux", socket: "mxnops" + filepath.Base(t.TempDir())}
	withoutPS(t)
	if !d.serverAbsent(noSocketText) {
		t.Error("serverAbsent = false with no server alive and no ps; a cold start would fail")
	}
}

// A live server for the name is seen without ps, so a missing socket file
// behind it is still an outage and never absence.
func TestALiveServerIsSeenWithoutPS(t *testing.T) {
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxnops")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("MARVEL_TMUX_SOCKET", "mxnops"+filepath.Base(dir))
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.KillServer() })
	if err := d.NewSession("probe"); err != nil {
		t.Fatalf("start server: %v", err)
	}
	withoutPS(t)
	alive, err := d.serverProcessAlive()
	if err != nil || !alive {
		t.Errorf("serverProcessAlive = %v, %v; want true and no error with a server alive and no ps", alive, err)
	}
	if d.serverAbsent(noSocketText) {
		t.Error("serverAbsent = true with a live server; a missing socket behind it is an outage")
	}
}
