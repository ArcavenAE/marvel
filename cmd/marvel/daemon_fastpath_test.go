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
)

// marvel#744: a second daemon is refused at once when the pidfile names a live
// marvel daemon, instead of after the five-second wait on the state lock. It is
// a fast path only. The lock stays the authority: anything short of an
// identified daemon (a dead pid, another user's process, a process that is not a
// daemon, an argv that cannot be read) falls through to it.

const refusal = "another marvel daemon holds the state lock (pid 4242, started as `/opt/bin/marvel daemon --mrvl`); it is already running, and `marvel describe daemon` shows it"

var liveMarvelDaemon = []string{"/opt/bin/marvel", "daemon", "--mrvl"}

func TestRefuseLiveDaemonNamesTheIdentifiedHolder(t *testing.T) {
	seams(t, alive, argvOf(liveMarvelDaemon...))
	err := refuseLiveDaemon(writePidfile(t, 4242), newRootCmd())
	if err == nil || err.Error() != refusal {
		t.Fatalf("want exactly %q, got %v", refusal, err)
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
func runDaemonCmd(t *testing.T, pidFile string) (time.Duration, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sockDir, err := os.MkdirTemp("/tmp", "fp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	bolt := filepath.Join(home, "state", "marvel.bolt")
	holder := api.NewStore()
	if err := holder.OpenBolt(bolt); err != nil {
		t.Fatalf("hold the state file: %v", err)
	}
	t.Cleanup(func() { _ = holder.CloseBolt() })

	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"daemon", "--socket", filepath.Join(sockDir, "s.sock"), "--log-file=", "--pidfile", pidFile, "--state-bolt", bolt})
	start := time.Now()
	err = root.Execute()
	return time.Since(start), err
}

// The whole command, not just the helper: with the lock held and the pidfile
// naming a live daemon, the start is refused well under the bolt's wait.
func TestDaemonCommandRefusesAtOnceBeforeTheBolt(t *testing.T) {
	seams(t, alive, argvOf(liveMarvelDaemon...))
	took, err := runDaemonCmd(t, writePidfile(t, 4242))
	if err == nil || err.Error() != refusal {
		t.Fatalf("want the refusal %q, got %v", refusal, err)
	}
	if took > time.Second {
		t.Errorf("the refusal took %v, which means the bolt wait came first", took)
	}
}

// An argv that cannot be read is not an identification: the start goes on to the
// bolt, which is the authority, and the lock error carries the hedged wording.
func TestDaemonCommandFallsThroughToTheBoltWhenArgvIsUnreadable(t *testing.T) {
	seams(t, alive, func(int) ([]string, error) { return nil, errors.New("denied") })
	took, err := runDaemonCmd(t, writePidfile(t, 4242))
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
