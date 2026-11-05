package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/daemon"
)

// runDaemonCmdForOptions runs the real daemon command with the constructor
// replaced by one that records its options and fails, so no daemon starts.
func runDaemonCmdForOptions(t *testing.T) daemon.Options {
	t.Helper()
	var got daemon.Options
	old := newDaemon
	newDaemon = func(o daemon.Options) (*daemon.Daemon, error) { got = o; return nil, errNotRefused }
	t.Cleanup(func() { newDaemon = old })

	sockDir, err := os.MkdirTemp("/tmp", "mv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"daemon", "--socket", filepath.Join(sockDir, "s.sock"), "--log-file=", "--pidfile", "", "--state-bolt", ""})
	if err := root.Execute(); err == nil {
		t.Fatal("the replaced constructor did not stop the start")
	}
	return got
}

// The root lock path comes from the resolved layout, so MARVEL_HOME moves it
// and nothing else does (marvel#883). The run dir is made before the daemon
// opens the lock file in it.
func TestDaemonCommandSetsTheRootLockFromTheLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_HOME", "")
	got := runDaemonCmdForOptions(t)
	want := filepath.Join(home, ".marvel", "run", "daemon.lock")
	if got.RootLock != want {
		t.Fatalf("RootLock = %q, want %q", got.RootLock, want)
	}
	if fi, err := os.Stat(filepath.Dir(want)); err != nil || !fi.IsDir() {
		t.Fatalf("the run dir was not made before the lock: %v", err)
	}

	named := filepath.Join(t.TempDir(), "other")
	t.Setenv("MARVEL_HOME", named)
	got = runDaemonCmdForOptions(t)
	if want := filepath.Join(named, "run", "daemon.lock"); got.RootLock != want {
		t.Fatalf("with MARVEL_HOME, RootLock = %q, want %q", got.RootLock, want)
	}
}

// One cluster per root is the rule the lock enforces, so there is no flag to
// move it.
func TestDaemonCommandHasNoRootLockFlag(t *testing.T) {
	cmd, _, err := newRootCmd().Find([]string{"daemon"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root-lock", "rootlock", "lock", "lock-file"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("the daemon command has a --%s flag", name)
		}
	}
}
