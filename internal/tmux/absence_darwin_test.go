//go:build darwin

package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
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

// fakeTmuxProc is a kinfo_proc entry the reader takes for a tmux process.
func fakeTmuxProc(pid int) unix.KinfoProc {
	var p unix.KinfoProc
	p.Proc.P_pid = int32(pid)
	copy(p.Proc.P_comm[:], "tmux")
	return p
}

func withKernelReads(t *testing.T, procs func(string, ...int) ([]unix.KinfoProc, error), args func(int) ([]string, error)) {
	t.Helper()
	oldProcs, oldArgs := kinfoProcs, procArgs
	t.Cleanup(func() { kinfoProcs, procArgs = oldProcs, oldArgs })
	if procs != nil {
		kinfoProcs = procs
	}
	if args != nil {
		procArgs = args
	}
}

// A refused read of the process table is an outage, never absence: reading it
// as "no server" would reap every session or start a second server. The refusal
// is reachable under a sandbox profile that denies sysctl-read on kern.proc.
func TestARefusedProcessTableIsNeverAbsence(t *testing.T) {
	d := &Driver{binary: "/nonexistent/tmux", socket: "mxrefused" + filepath.Base(t.TempDir())}
	withKernelReads(t, func(string, ...int) ([]unix.KinfoProc, error) { return nil, unix.EPERM }, nil)
	if _, err := defaultProcList(context.Background()); err == nil {
		t.Error("defaultProcList returned no error for a refused process table")
	}
	if d.serverAbsent(noSocketText) {
		t.Error("serverAbsent = true with the process table refused; that is an outage")
	}
}

// A tmux process that is still in the table but whose argument list cannot be
// read is unknown, not gone: skipping it would read a live server as absent.
func TestAnUnreadableArgumentListOfALiveTmuxIsNotAbsence(t *testing.T) {
	d := &Driver{binary: "/nonexistent/tmux", socket: "mxunread" + filepath.Base(t.TempDir())}
	withKernelReads(t,
		func(string, ...int) ([]unix.KinfoProc, error) {
			return []unix.KinfoProc{fakeTmuxProc(os.Getpid())}, nil
		},
		func(int) ([]string, error) { return nil, unix.EPERM })
	if _, err := defaultProcList(context.Background()); err == nil {
		t.Error("defaultProcList returned no error for a live tmux whose arguments cannot be read")
	}
	if d.serverAbsent(noSocketText) {
		t.Error("serverAbsent = true while a tmux process could not be read; that is unknown")
	}
}

// A tmux process that exited between the table read and its argument read is
// gone, and is skipped, so a real absence is still read as absence.
func TestATmuxThatExitedBetweenTheReadsIsSkipped(t *testing.T) {
	c := exec.Command("/usr/bin/true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	gone := c.Process.Pid
	d := &Driver{binary: "/nonexistent/tmux", socket: "mxgone" + filepath.Base(t.TempDir())}
	withKernelReads(t,
		func(string, ...int) ([]unix.KinfoProc, error) { return []unix.KinfoProc{fakeTmuxProc(gone)}, nil },
		func(int) ([]string, error) { return nil, unix.ESRCH })
	out, err := defaultProcList(context.Background())
	if err != nil || len(out) != 0 {
		t.Errorf("defaultProcList = %q, %v; want an empty list and no error for a process that exited", out, err)
	}
	if !d.serverAbsent(noSocketText) {
		t.Error("serverAbsent = false; a process that exited is absence")
	}
}

// A tmux process that has just forked has no readable argument list for a few
// milliseconds. That passes, so a server being started or stopped next to the
// read is not taken for an outage.
func TestAnArgumentListThatSettlesIsRead(t *testing.T) {
	calls := 0
	withKernelReads(t,
		func(string, ...int) ([]unix.KinfoProc, error) {
			return []unix.KinfoProc{fakeTmuxProc(os.Getpid())}, nil
		},
		func(int) ([]string, error) {
			calls++
			if calls < 3 {
				return nil, unix.EINVAL
			}
			return []string{"tmux", "-L", "mxsettle"}, nil
		})
	out, err := defaultProcList(context.Background())
	if err != nil || len(out) == 0 {
		t.Errorf("defaultProcList = %q, %v; want the process listed once its arguments settle", out, err)
	}
}
