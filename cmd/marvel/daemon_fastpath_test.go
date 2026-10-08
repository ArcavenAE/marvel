package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
)

// marvel#744: a second daemon is refused at once when the pidfile names a live
// marvel daemon, instead of after the five-second wait on the state lock. It is
// a fast path only. The lock stays the authority: anything short of an
// identified daemon (a dead pid, another user's process, a process that is not a
// daemon, an argv that cannot be read) falls through to it.

// refusalFor is what the fast path says: what the pidfile names, and nothing
// about the state lock, which it never opened.
func refusalFor(pidFile string) string {
	return fmt.Sprintf("another marvel daemon is running against pidfile %s (pid 4242, started as `/opt/bin/marvel daemon --mrvl`)", pidFile)
}

var liveMarvelDaemon = []string{"/opt/bin/marvel", "daemon", "--mrvl"}

func TestRefuseLiveDaemonNamesTheIdentifiedHolder(t *testing.T) {
	seams(t, alive, argvOf(liveMarvelDaemon...))
	pidFile := writePidfile(t, 4242)
	err := refuseLiveDaemon(pidFile, newRootCmd())
	if err == nil || err.Error() != refusalFor(pidFile) {
		t.Fatalf("want exactly %q, got %v", refusalFor(pidFile), err)
	}
	if strings.Contains(err.Error(), "state lock") {
		t.Errorf("the fast path never opened the state file and must not claim its lock: %v", err)
	}
	if errors.Is(err, api.ErrBoltLocked) {
		t.Errorf("the fast path never opened the bolt, so its error must not claim the lock timed out: %v", err)
	}
}

// Everything short of an identified, same-user marvel daemon falls through.
func TestRefuseLiveDaemonFallsThrough(t *testing.T) {
	own := os.Getpid()
	cases := []struct {
		name    string
		sig     func(int) error
		args    func(int) ([]string, error)
		content string // pidfile body; empty means the standard 4242
	}{
		{"a dead pid", func(int) error { return syscall.ESRCH }, argvOf(liveMarvelDaemon...), ""},
		{"another user's process", func(int) error { return syscall.EPERM }, argvOf(liveMarvelDaemon...), ""},
		{"a live process that is not a daemon", alive, argvOf("sleep", "30"), ""},
		{"marvel but not the daemon command", alive, argvOf("/opt/bin/marvel", "get", "sessions"), ""},
		{"an argv that cannot be read", alive, func(int) ([]string, error) { return nil, errors.New("denied") }, ""},
		{"a pidfile naming this process", alive, argvOf(liveMarvelDaemon...), fmt.Sprintf("%d\n", own)},
		{"a pid wider than 32 bits that wraps to this process", alive, argvOf(liveMarvelDaemon...), fmt.Sprintf("%d\n", int64(own)+1<<32)},
		{"a pid of zero", alive, argvOf(liveMarvelDaemon...), "0\n"},
		{"a pid followed by text", alive, argvOf(liveMarvelDaemon...), "4242xyz\n"},
		{"an empty pidfile", alive, argvOf(liveMarvelDaemon...), " \n"},
	}
	for _, tc := range cases {
		probed := 0
		sig := tc.sig
		seams(t, func(pid int) error { probed++; return sig(pid) }, tc.args)
		path := writePidfile(t, 4242)
		if tc.content != "" {
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := refuseLiveDaemon(path, newRootCmd()); err != nil {
			t.Errorf("%s: refused though the holder is not an identified daemon: %v", tc.name, err)
		}
		if tc.content != "" && tc.name != "a pidfile naming this process" && probed != 0 {
			t.Errorf("%s: a pid that names no process was probed %d time(s)", tc.name, probed)
		}
	}
	// A pidfile that is off or missing.
	seams(t, alive, argvOf(liveMarvelDaemon...))
	for name, path := range map[string]string{"pidfile off": "", "missing pidfile": filepath.Join(t.TempDir(), "absent.pid")} {
		if err := refuseLiveDaemon(path, newRootCmd()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A real unrelated live process, through the real probe and the real reader,
// is not a daemon, so a start goes on to the bolt.
func TestRefuseLiveDaemonLetsARealUnrelatedProcessThrough(t *testing.T) {
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skipf("no sleep(1): %v", err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	if err := refuseLiveDaemon(writePidfile(t, c.Process.Pid), newRootCmd()); err != nil {
		t.Fatalf("a live sleep in the pidfile refused the start: %v", err)
	}
}

// runDaemonCmd runs the real daemon command under a scratch HOME, socket,
// pidfile and state file, with the state file lock held by this process.
func runDaemonCmd(t *testing.T, pidFile string, hold bool) (time.Duration, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sockDir, err := os.MkdirTemp("/tmp", "fp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	bolt := filepath.Join(home, "state", "marvel.bolt")
	if hold {
		holder := api.NewStore()
		if err := holder.OpenBolt(bolt); err != nil {
			t.Fatalf("hold the state file: %v", err)
		}
		t.Cleanup(func() { _ = holder.CloseBolt() })
	}

	// When the state file is not held, a start that no guard refused would go on
	// to run a daemon inside the test process and wait for a signal. The
	// constructor is replaced so such a start fails at once instead. When the
	// file is held, the real constructor can only fail on the lock.
	if !hold {
		old := newDaemon
		newDaemon = func(daemon.Options) (*daemon.Daemon, error) { return nil, errNotRefused }
		t.Cleanup(func() { newDaemon = old })
	}

	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"daemon", "--socket", filepath.Join(sockDir, "s.sock"), "--log-file=", "--pidfile", pidFile, "--state-bolt", bolt})
	type outcome struct {
		took time.Duration
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		start := time.Now()
		err := root.Execute()
		done <- outcome{time.Since(start), err}
	}()
	select {
	case o := <-done:
		return o.took, o.err
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon command did not return in 30s: a guard did not refuse and a daemon may be running in this process")
		return 0, nil
	}
}

// errNotRefused is what the replaced constructor returns: no guard refused.
var errNotRefused = errors.New("test: no guard refused this start, and the test will not start a daemon")

// The whole command, not just the helper: with the lock held and the pidfile
// naming a live daemon, the start is refused well under the bolt's wait.
func TestDaemonCommandRefusesAtOnceBeforeTheBolt(t *testing.T) {
	seams(t, alive, argvOf(liveMarvelDaemon...))
	pidFile := writePidfile(t, 4242)
	took, err := runDaemonCmd(t, pidFile, true)
	if err == nil || err.Error() != refusalFor(pidFile) {
		t.Fatalf("want the refusal %q, got %v", refusalFor(pidFile), err)
	}
	if took > time.Second {
		t.Errorf("the refusal took %v, which means the bolt wait came first", took)
	}
}

// A start that shares a live daemon's pidfile but names its own state file is
// refused as well (main's guard in Start refuses it after opening that file),
// and the refusal says what is true of it: the pidfile names a running daemon,
// not that this state file's lock is held (review of #747).
func TestDaemonCommandWithItsOwnBoltIsRefusedForTheSharedPidfile(t *testing.T) {
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skipf("no sleep(1): %v", err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	pid := c.Process.Pid
	// The probe is the real one, so the pid is really alive; only its argv is
	// told to read as a marvel daemon.
	seams(t, liveSignal, func(p int) ([]string, error) {
		if p != pid {
			return nil, errors.New("not the test's process")
		}
		return liveMarvelDaemon, nil
	})
	pidFile := writePidfile(t, pid)
	took, err := runDaemonCmd(t, pidFile, false)
	want := fmt.Sprintf("another marvel daemon is running against pidfile %s (pid %d, started as `/opt/bin/marvel daemon --mrvl`)", pidFile, pid)
	if err == nil || err.Error() != want {
		t.Fatalf("want %q, got %v", want, err)
	}
	if strings.Contains(err.Error(), "state lock") {
		t.Errorf("the refusal claims a lock this start's own state file never had: %v", err)
	}
	if took > time.Second {
		t.Errorf("the refusal took %v", took)
	}
}

// Real contention on the state file with no live daemon in the pidfile is the
// bolt's to decide, and it says what it knows: the lock is held.
func TestDaemonCommandBoltContentionWithNoLiveHolderGetsTheLockWording(t *testing.T) {
	dead := deadPid(t)
	seams(t, liveSignal, daemonArgs)
	_, err := runDaemonCmd(t, writePidfile(t, dead), true)
	want := fmt.Sprintf("the state lock is held; the pidfile names pid %d, which may not be the holder", dead)
	if err == nil || !strings.Contains(err.Error(), want) || !errors.Is(err, api.ErrBoltLocked) {
		t.Fatalf("want the bolt's lock wording %q wrapping ErrBoltLocked, got %v", want, err)
	}
	if strings.Contains(err.Error(), "running against pidfile") {
		t.Errorf("a dead pid was reported as a running daemon: %v", err)
	}
}

// An argv that cannot be read is not an identification: the start goes on to the
// bolt, which is the authority, and the lock error carries the hedged wording.
func TestDaemonCommandFallsThroughToTheBoltWhenArgvIsUnreadable(t *testing.T) {
	seams(t, alive, func(int) ([]string, error) { return nil, errors.New("denied") })
	took, err := runDaemonCmd(t, writePidfile(t, 4242), true)
	if err == nil || !strings.Contains(err.Error(), "the state lock is held; the pidfile names pid 4242, which may not be the holder") {
		t.Fatalf("want the bolt's hedged error, got %v", err)
	}
	if !errors.Is(err, api.ErrBoltLocked) {
		t.Errorf("the error does not come from the bolt: %v", err)
	}
	if took < 3*time.Second {
		t.Errorf("returned after %v: the bolt's wait was skipped", took)
	}
}
