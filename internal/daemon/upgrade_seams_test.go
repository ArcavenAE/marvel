package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The seams the zero-downtime upgrade chain needs under its fallback: the
// bare exit after a failed exec, the daemon's own path, the hook after the
// root lock, and the marker that crosses an exec. None of them changes what a
// daemon does today.

// A reexec whose exec fails after the detach ends the process through the exit
// seam, not through os.Exit, so a test can see it (and so the fallback chain can
// put a recovery in front of it). Before the seam this test could not exist: the
// bare os.Exit(1) would end the test binary.
func TestFailedReexecExitsThroughTheExitSeam(t *testing.T) {
	d, _ := reexecHarness(t)
	d.reexec = func(string, []string, []string) error { return errors.New("exec refused") }
	code := make(chan int, 1)
	d.exit = func(c int) { code <- c }

	if resp := d.dispatchAs(daemonrpc.Request{Method: "reexec"}, localCaller()); resp.Error != "" {
		t.Fatalf("reexec: %s", resp.Error)
	}
	select {
	case c := <-code:
		if c != 1 {
			t.Errorf("exit code = %d, want 1", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the exit seam was not called after the failed exec")
	}
}

// The control: an exec that succeeds does not exit.
func TestSuccessfulReexecDoesNotExit(t *testing.T) {
	d, calls := reexecHarness(t)
	called := make(chan int, 1)
	d.exit = func(c int) { called <- c }
	if resp := d.dispatchAs(daemonrpc.Request{Method: "reexec"}, localCaller()); resp.Error != "" {
		t.Fatalf("reexec: %s", resp.Error)
	}
	select {
	case <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("the exec primitive was not invoked")
	}
	select {
	case c := <-called:
		t.Errorf("a successful exec called the exit seam with %d", c)
	case <-time.After(300 * time.Millisecond):
	}
}

// The daemon's own path comes through the selfExec seam when a test sets one,
// and a failure there is reported without detaching.
func TestReexecResolvesItsOwnPathThroughTheSelfExecSeam(t *testing.T) {
	d, calls := reexecHarness(t)
	d.selfExec = func() (string, error) { return "/kept/marvel", nil }
	if resp := d.dispatchAs(daemonrpc.Request{Method: "reexec"}, localCaller()); resp.Error != "" {
		t.Fatalf("reexec: %s", resp.Error)
	}
	select {
	case got := <-calls:
		if got != "/kept/marvel" {
			t.Errorf("exec'd %q, want the path the seam named", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the exec primitive was not invoked")
	}

	d2, calls2 := reexecHarness(t)
	d2.selfExec = func() (string, error) { return "", errors.New("no self") }
	resp := d2.dispatchAs(daemonrpc.Request{Method: "reexec"}, localCaller())
	if !strings.Contains(resp.Error, "no self") {
		t.Errorf("response error = %q, want the seam's failure", resp.Error)
	}
	expectNoExec(t, calls2)
}

// Without a seam the daemon resolves its path as before.
func TestResolveSelfWithoutASeamIsSelfExecPath(t *testing.T) {
	d := &Daemon{}
	got, err := d.resolveSelf()
	want, werr := selfExecPath()
	if got != want || (err == nil) != (werr == nil) {
		t.Errorf("resolveSelf = %q, %v; want %q, %v", got, err, want, werr)
	}
}

// AfterRootLock runs once, with the root lock held, and what it returns is kept
// on the daemon.
func TestAfterRootLockRunsOnceWithTheLockHeld(t *testing.T) {
	skipIfNoTmux(t)
	_, lockPath := rootLockPath(t)
	var calls int
	var lockHeld bool
	var d *Daemon
	dir := t.TempDir()
	d, err := NewWithOptions(Options{
		RootLock:  lockPath,
		PidFile:   filepath.Join(dir, "a.pid"),
		StateBolt: filepath.Join(dir, "a.bolt"),
		AfterRootLock: func() (string, error) {
			calls++
			lockHeld = d.rootLock != nil
			return "/kept/marvel", nil
		},
	})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	if err := d.Start(testSocket(t, "a")); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(d.Stop)
	if calls != 1 || !lockHeld {
		t.Errorf("hook calls = %d, lock held at the call = %v; want 1 and true", calls, lockHeld)
	}
	if d.fallback != "/kept/marvel" {
		t.Errorf("fallback = %q, want what the hook returned", d.fallback)
	}
}

// A daemon that did not get the lock never calls the hook, and a daemon with
// no hook starts as it always has.
func TestAfterRootLockIsNotCalledWithoutTheLock(t *testing.T) {
	skipIfNoTmux(t)
	_, lockPath := rootLockPath(t)

	first, sock1, _ := rootDaemon(t, lockPath, "first")
	if err := first.Start(sock1); err != nil {
		t.Fatalf("start first: %v", err)
	}
	t.Cleanup(first.Stop)

	var calls int
	dir := t.TempDir()
	second, err := NewWithOptions(Options{
		RootLock:      lockPath,
		PidFile:       filepath.Join(dir, "b.pid"),
		StateBolt:     filepath.Join(dir, "b.bolt"),
		AfterRootLock: func() (string, error) { calls++; return "", nil },
	})
	if err != nil {
		t.Fatalf("new second: %v", err)
	}
	if err := second.Start(testSocket(t, "second")); err == nil {
		second.Stop()
		t.Fatal("the second daemon started; the test needs the lock refused")
	}
	if calls != 0 {
		t.Errorf("the hook ran %d times for a daemon that lost the lock", calls)
	}
}

// A hook that fails is logged and does not stop the start.
func TestAfterRootLockFailureDoesNotStopTheStart(t *testing.T) {
	skipIfNoTmux(t)
	_, lockPath := rootLockPath(t)
	dir := t.TempDir()
	d, err := NewWithOptions(Options{
		RootLock:      lockPath,
		PidFile:       filepath.Join(dir, "c.pid"),
		StateBolt:     filepath.Join(dir, "c.bolt"),
		AfterRootLock: func() (string, error) { return "", errors.New("keep failed") },
	})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	if err := d.Start(testSocket(t, "c")); err != nil {
		t.Fatalf("start with a failing hook: %v", err)
	}
	t.Cleanup(d.Stop)
	if d.fallback != "" {
		t.Errorf("fallback = %q after a failed hook, want empty", d.fallback)
	}
}

// The fallback marker crosses an exec. The parent re-runs this test binary as a
// child, the child sets the marker and calls execWithHandoff with the real
// syscall.Exec onto a stub script, and the stub writes the environment it
// received. The stub is a shell script in a scratch directory; no marvel binary
// runs and nothing listens.
const (
	childStubVar = "EXEC_CROSSING_STUB"
	childMarker  = "EXEC_CROSSING_MARKER"
)

func TestFallbackMarkerCrossesAnExec(t *testing.T) {
	if stub := os.Getenv(childStubVar); stub != "" {
		// The child: set the marker and a stale handoff variable, then exec.
		if err := os.Setenv(api.FallbackEnv, os.Getenv(childMarker)); err != nil {
			t.Fatal(err)
		}
		if err := os.Setenv(handoffEnvVar, "9"); err != nil {
			t.Fatal(err)
		}
		d := &Daemon{reexec: syscall.Exec}
		err := d.execWithHandoff(stub, &reexecHandoff{outcome: handoffNone})
		t.Fatalf("exec returned: %v", err)
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "env.out")
	stub := filepath.Join(dir, "stub.sh")
	script := "#!/bin/sh\nenv > " + out + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	const kept = "/scratch/versions/0.9.0/marvel"
	cmd := exec.Command(os.Args[0], "-test.run=^TestFallbackMarkerCrossesAnExec$")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, childStubVar + "=" + stub, childMarker + "=" + kept}
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the stub wrote nothing: %v", err)
	}
	env := string(data)
	if !strings.Contains(env, api.FallbackEnv+"="+kept+"\n") {
		t.Errorf("the marker did not cross the exec; stub environment:\n%s", env)
	}
	if strings.Contains(env, handoffEnvVar+"=") {
		t.Errorf("a stale handoff variable crossed the exec; stub environment:\n%s", env)
	}
}

func TestFallbackEnvName(t *testing.T) {
	if api.FallbackEnv != "MARVEL_UPGRADE_FALLBACK" {
		t.Errorf("FallbackEnv = %q", api.FallbackEnv)
	}
}
