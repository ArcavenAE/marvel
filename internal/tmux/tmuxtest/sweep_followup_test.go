package tmuxtest

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// darwin takes a 32-bit pid at the syscall, so a wider integer acts on the
// process whose low 32 bits match (bash.md, "A pid above 2^32"). A name that
// carries such a value is not the pid it appears to be, and is read as no pid.
// The values are this binary's own pid and a dead one plus 2^32; neither has
// low 32 bits of 0 or all ones, which would reach every process.
func TestParsePidRejectsAValueAbove32Bits(t *testing.T) {
	const wide = 1 << 32
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"this binary", strconv.Itoa(os.Getpid()), true},
		{"this binary plus 2^32", strconv.Itoa(os.Getpid() + wide), false},
		{"dead pid plus 2^32", strconv.Itoa(deadPid(t) + wide), false},
		{"zero", "0", false},
		{"not a number", "12x", false},
	} {
		if _, ok := parsePid(tc.in); ok != tc.want {
			t.Errorf("%s: parsePid(%q) ok = %v, want %v", tc.name, tc.in, ok, tc.want)
		}
	}
}

// The sweep must not act on a name whose pid is wider than 32 bits: the dead
// pid inside it would read as dead and the file would go, on the strength of a
// number the kernel truncates.
func TestSweepStaleSocketsLeavesAWidePidName(t *testing.T) {
	dir, sockets := socketDir(t)
	wide := "marvel-test-team-" + strconv.Itoa(deadPid(t)+(1<<32))
	staleSocket(t, filepath.Join(sockets, wide))
	if got := sweepStaleSockets(dir); len(got) != 0 {
		t.Errorf("sweepStaleSockets = %v, want nothing for a pid above 32 bits", got)
	}
	if !exists(filepath.Join(sockets, wide)) {
		t.Errorf("%s was removed", wide)
	}
}

// Only a refused connection says nobody listens. A connect that fails another
// way (a sandbox that denies unix-connect gives EPERM) says nothing about the
// server, so the file stays.
func TestSweepStaleSocketsRemovesOnlyOnARefusedConnection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dial    func(string) (net.Conn, error)
		removed bool
	}{
		{"refused", func(string) (net.Conn, error) { return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED} }, true},
		{"refused, wrapped", func(string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
		}, true},
		{"denied by a sandbox", func(string) (net.Conn, error) { return nil, &net.OpError{Op: "dial", Err: syscall.EPERM} }, false},
		{"access denied", func(string) (net.Conn, error) { return nil, &net.OpError{Op: "dial", Err: syscall.EACCES} }, false},
		{"timed out", func(string) (net.Conn, error) { return nil, errors.New("i/o timeout") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := dialUnix
			dialUnix = tc.dial
			t.Cleanup(func() { dialUnix = old })
			dir, sockets := socketDir(t)
			name := "marvel-test-team-" + strconv.Itoa(deadPid(t))
			staleSocket(t, filepath.Join(sockets, name))

			got := sweepStaleSockets(dir)

			if removed := !exists(filepath.Join(sockets, name)); removed != tc.removed {
				t.Errorf("socket removed = %v, want %v (swept %v)", removed, tc.removed, got)
			}
		})
	}
}

// A test binary that finds TMUX_TMPDIR unset would sweep, and run its servers
// in, the operator's own /tmp/tmux-<uid>. It gets a directory of its own
// instead, the same one on every run, so the next run finds what a killed run
// left.
func TestScratchTmuxTmpDirIsPrivateAndStable(t *testing.T) {
	base := t.TempDir()
	dir := scratchTmuxTmpDir(base)
	if dir == "" || filepath.Dir(dir) != base {
		t.Fatalf("scratchTmuxTmpDir(%s) = %q, want a directory directly under it", base, dir)
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("scratch dir %s: %v, %v, want a directory with mode 0700", dir, err, fi)
	}
	if again := scratchTmuxTmpDir(base); again != dir {
		t.Errorf("a second call gave %q, want the same directory %q", again, dir)
	}
}

// /tmp is shared, and the name is predictable. A symlink planted at that name
// must not become the directory tmux's sockets and the sweep's removals use.
func TestScratchTmuxTmpDirRefusesAPlantedSymlink(t *testing.T) {
	base := t.TempDir()
	elsewhere := t.TempDir()
	first := scratchTmuxTmpDir(base)
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, first); err != nil {
		t.Fatal(err)
	}
	got := scratchTmuxTmpDir(base)
	if got == "" || got == first || got == elsewhere {
		t.Fatalf("scratchTmuxTmpDir with a symlink at %s = %q, want a different directory", first, got)
	}
	if fi, err := os.Lstat(got); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("fallback %s: %v, %v, want a real directory with mode 0700", got, err, fi)
	}
}

func TestUseScratchTmuxTmpDirSetsTheVariableOnlyWhenUnset(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", "/somewhere/else")
	if got := UseScratchTmuxTmpDir(); got != "" || os.Getenv("TMUX_TMPDIR") != "/somewhere/else" {
		t.Errorf("with a value set: returned %q, TMUX_TMPDIR = %q, want it untouched", got, os.Getenv("TMUX_TMPDIR"))
	}
	if err := os.Unsetenv("TMUX_TMPDIR"); err != nil {
		t.Fatal(err)
	}
	got := UseScratchTmuxTmpDir()
	if got == "" || os.Getenv("TMUX_TMPDIR") != got {
		t.Fatalf("with none set: returned %q, TMUX_TMPDIR = %q, want it set to the directory", got, os.Getenv("TMUX_TMPDIR"))
	}
	// A unix socket path must stay under 104 bytes on darwin.
	if n := len(filepath.Join(got, "tmux-"+strconv.Itoa(os.Getuid()), "marvel-test-session-4294967295")); n >= 104 {
		t.Errorf("%s leaves a socket path of %d bytes, over the 104-byte limit", got, n)
	}
}
