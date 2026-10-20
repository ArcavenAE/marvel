package tmuxtest

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// staleSocket binds a unix socket at path and closes the listener without
// unlinking it, which is what a killed tmux server leaves behind: a socket
// file nobody answers on.
func staleSocket(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("fixture %s is not a socket file: %v", path, err)
	}
}

func socketDir(t *testing.T) (dir, sockets string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mxst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sockets = filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(sockets, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, sockets
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A killed test binary never reaches its own cleanup, and its server goes
// away with no one to unlink the socket. The next run removes that socket
// file when the name carries a dead pid, and only then.
func TestSweepStaleSocketsRemovesOnlyTheSocketOfADeadBinary(t *testing.T) {
	dir, sockets := socketDir(t)
	dead := "marvel-test-team-" + strconv.Itoa(deadPid(t))
	live := "marvel-test-team-" + strconv.Itoa(os.Getpid())
	odd := "other-team-" + strconv.Itoa(deadPid(t))
	tail := "marvel-test-team-" + strconv.Itoa(deadPid(t)) + "-x"
	for _, n := range []string{dead, live, odd, tail} {
		staleSocket(t, filepath.Join(sockets, n))
	}
	// A regular file in the dead binary's name is not a socket and stays.
	plain := "marvel-test-plain-" + strconv.Itoa(deadPid(t))
	if err := os.WriteFile(filepath.Join(sockets, plain), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := sweepStaleSockets(dir)

	if exists(filepath.Join(sockets, dead)) {
		t.Errorf("the socket of a dead test binary (%s) is still there", dead)
	}
	if len(got) != 1 || got[0] != dead {
		t.Errorf("sweepStaleSockets = %v, want [%s]", got, dead)
	}
	for _, kept := range []string{live, odd, tail, plain} {
		if !exists(filepath.Join(sockets, kept)) {
			t.Errorf("%s was removed, want it left alone", kept)
		}
	}
}

// A dead pid in the name is not enough: a server still listening on the
// socket is a server, and its socket file is not removed from under it.
func TestSweepStaleSocketsLeavesASocketSomeoneAnswersOn(t *testing.T) {
	dir, sockets := socketDir(t)
	name := "marvel-test-team-" + strconv.Itoa(deadPid(t))
	l, err := net.Listen("unix", filepath.Join(sockets, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	if got := sweepStaleSockets(dir); len(got) != 0 {
		t.Errorf("sweepStaleSockets = %v, want nothing: a server answers on %s", got, name)
	}
	if !exists(filepath.Join(sockets, name)) {
		t.Errorf("the socket %s a server listens on was removed", name)
	}
}

// A missing socket directory is the normal state of a host that has not run
// a test yet; the sweep reports nothing and does not create it.
func TestSweepStaleSocketsWithNoDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mxst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if got := sweepStaleSockets(dir); len(got) != 0 {
		t.Errorf("sweepStaleSockets = %v, want nothing", got)
	}
	if exists(filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()))) {
		t.Error("the sweep created the socket directory")
	}
}

// SweepDead is what every TestMain calls, so the file sweep must run from it,
// including when ps cannot answer: the file sweep reads a directory, not ps.
func TestSweepDeadRemovesTheStaleSocketsToo(t *testing.T) {
	dir, sockets := socketDir(t)
	t.Setenv("TMUX_TMPDIR", dir)
	dead := "marvel-test-daemon-" + strconv.Itoa(deadPid(t))
	staleSocket(t, filepath.Join(sockets, dead))

	swept := SweepDead()

	if exists(filepath.Join(sockets, dead)) {
		t.Errorf("SweepDead left the stale socket of a dead test binary (%s)", dead)
	}
	found := false
	for _, n := range swept {
		found = found || n == dead
	}
	if !found {
		t.Errorf("SweepDead returned %v, want it to include %s", swept, dead)
	}
}
