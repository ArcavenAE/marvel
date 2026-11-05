package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// rootLockPath makes a scratch root and returns its root, its run dir and
// the lock path the daemon command would pass for it.
func rootLockPath(t *testing.T) (root, lockPath string) {
	t.Helper()
	root = t.TempDir()
	run := filepath.Join(root, "run")
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatalf("make run dir: %v", err)
	}
	return root, filepath.Join(run, "daemon.lock")
}

// rootDaemon builds a daemon with its own pidfile and state file, so that
// only the root lock can tell it from another daemon under the same root.
func rootDaemon(t *testing.T, lockPath, name string) (d *Daemon, sock, pid string) {
	t.Helper()
	dir := t.TempDir()
	pid = filepath.Join(dir, name+".pid")
	d, err := NewWithOptions(Options{
		RootLock:  lockPath,
		PidFile:   pid,
		StateBolt: filepath.Join(dir, name+".bolt"),
	})
	if err != nil {
		t.Fatalf("new daemon %s: %v", name, err)
	}
	return d, testSocket(t, name), pid
}

// marvel#883: two daemons under one root, each with its own --socket,
// --pidfile and --state-bolt, shared the bus store and the broker, and the
// second one stopped the first one's broker. Nothing in the flags tied them
// together, so the lock has to come from the root alone.
func TestSecondDaemonUnderOneRootRefusesAtStart(t *testing.T) {
	skipIfNoTmux(t)
	root, lockPath := rootLockPath(t)

	first, sock1, _ := rootDaemon(t, lockPath, "first")
	if err := first.Start(sock1); err != nil {
		t.Fatalf("start first daemon: %v", err)
	}
	t.Cleanup(first.Stop)

	second, sock2, pid2 := rootDaemon(t, lockPath, "second")
	err := second.Start(sock2)
	if err == nil {
		second.Stop()
		t.Fatal("second daemon under the same root started; it must refuse")
	}
	for _, want := range []string{root, lockPath, "--marvel-home", "MARVEL_HOME"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}

	// Nothing past the lock ran.
	if second.listener != nil {
		t.Error("the refused daemon is listening")
	}
	if _, statErr := os.Stat(sock2); statErr == nil {
		t.Errorf("the refused daemon left a socket at %s", sock2)
	}
	if _, statErr := os.Stat(pid2); statErr == nil {
		t.Errorf("the refused daemon wrote its pidfile %s", pid2)
	}
	if second.socketLock != nil {
		t.Error("the refused daemon took the socket lock")
	}
}

// The supported shape: one HOME, two roots, each its own cluster.
func TestDaemonsUnderTwoRootsBothStart(t *testing.T) {
	skipIfNoTmux(t)
	_, lockA := rootLockPath(t)
	_, lockB := rootLockPath(t)

	a, sockA, _ := rootDaemon(t, lockA, "a")
	if err := a.Start(sockA); err != nil {
		t.Fatalf("start a: %v", err)
	}
	t.Cleanup(a.Stop)

	b, sockB, _ := rootDaemon(t, lockB, "b")
	if err := b.Start(sockB); err != nil {
		t.Fatalf("start b under another root: %v", err)
	}
	t.Cleanup(b.Stop)
}

// A daemon that stopped gives its root back.
func TestStopReleasesTheRootLock(t *testing.T) {
	skipIfNoTmux(t)
	_, lockPath := rootLockPath(t)

	first, sock1, _ := rootDaemon(t, lockPath, "first")
	if err := first.Start(sock1); err != nil {
		t.Fatalf("start first: %v", err)
	}
	first.Stop()

	again, sock2, _ := rootDaemon(t, lockPath, "again")
	if err := again.Start(sock2); err != nil {
		t.Fatalf("start under the same root after a stop: %v", err)
	}
	t.Cleanup(again.Stop)
}

// An empty RootLock means no lock, as an empty PidFile means no pidfile.
// Daemons built directly in other tests rely on it.
func TestEmptyRootLockTakesNoLock(t *testing.T) {
	skipIfNoTmux(t)

	a, sockA, _ := rootDaemon(t, "", "a")
	if err := a.Start(sockA); err != nil {
		t.Fatalf("start a: %v", err)
	}
	t.Cleanup(a.Stop)
	if a.rootLock != nil {
		t.Error("a daemon with no RootLock holds a root lock")
	}

	b, sockB, _ := rootDaemon(t, "", "b")
	if err := b.Start(sockB); err != nil {
		t.Fatalf("start b with no RootLock: %v", err)
	}
	t.Cleanup(b.Stop)
}

// A start that fails after taking the root lock gives it back, so one bad
// start does not lock the root until the process exits.
func TestFailedStartReleasesTheRootLock(t *testing.T) {
	_, lockPath := rootLockPath(t)

	d, sock, pid := rootDaemon(t, lockPath, "failing")
	// A pidfile naming a live process (this one) makes Start refuse after the
	// root lock was taken.
	if err := os.WriteFile(pid, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	if err := d.Start(sock); err == nil {
		d.Stop()
		t.Fatal("start with a live pidfile succeeded; the test needs it to fail")
	}
	if d.rootLock != nil {
		t.Error("a failed start still holds the root lock")
	}

	again, err := lockRoot(lockPath)
	if err != nil {
		t.Fatalf("the root is still locked after a failed start: %v", err)
	}
	again.release()
}
