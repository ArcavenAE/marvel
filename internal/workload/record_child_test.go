package workload

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// RecordChild gets the child Process proved from the handle, before the
// pidfile that names it is written, and that child is the one Process holds.
func TestRecordChildSeesTheProvenChildBeforeThePidfile(t *testing.T) {
	spec := procSpec(t)
	var got childproof.Child
	var pidfileThere bool
	spec.RecordChild = func(cmd *exec.Cmd, c childproof.Child) error {
		got = c
		_, err := os.Stat(spec.PidFile)
		pidfileThere = err == nil
		if c.Pid() != cmd.Process.Pid {
			t.Errorf("RecordChild child pid %d, command pid %d", c.Pid(), cmd.Process.Pid)
		}
		return nil
	}
	p, _, _ := newProc(t, spec)
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if pidfileThere {
		t.Error("the pidfile existed when RecordChild ran")
	}
	if st := p.Snapshot(); st.Child.Pid() != got.Pid() || !p.prober.Same(got) {
		t.Errorf("RecordChild saw pid %d, Process holds pid %d", got.Pid(), st.Child.Pid())
	}
}

// A RecordChild error stops the child and fails the Spawn, with no pidfile.
func TestRecordChildErrorFailsTheSpawn(t *testing.T) {
	spec := procSpec(t)
	boom := errors.New("cannot record")
	spec.RecordChild = func(*exec.Cmd, childproof.Child) error { return boom }
	p, _, _ := newProc(t, spec)
	if err := p.Spawn(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Spawn = %v, want the RecordChild error", err)
	}
	if _, err := os.Stat(spec.PidFile); err == nil {
		t.Error("a pidfile was written for a child that was not recorded")
	}
	if st := p.Snapshot(); st.Pid != 0 || st.Ready {
		t.Errorf("snapshot = %+v, want no child", st)
	}
}

type fixedWait time.Duration

func (w fixedWait) Wait(int) time.Duration { return time.Duration(w) }

// Any Schedule works, not only backoff.Schedule: a driver's own func shortens
// the wait in a test.
func TestProcessTakesAnyScheduleForTheRestartWait(t *testing.T) {
	p, d, c := newProc(t, procSpec(t))
	p.sched = fixedWait(7 * time.Millisecond)
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	killPid(t, p.Snapshot().Pid)
	until(t, "the crash", func() bool { return len(d.exitsCopy()) == 1 })
	if got := c.waitsCopy(); len(got) != 1 || got[0] != 7*time.Millisecond {
		t.Errorf("waits = %v, want the one 7ms wait", got)
	}
}

// A signal that cannot be sent reaches the caller as a TerminateError naming
// the step, so a driver can word its log line.
func TestTerminateErrorNamesTheStep(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail syscall.Signal
		step string
	}{{"term", syscall.SIGTERM, "stopping"}, {"kill", syscall.SIGKILL, "killing"}} {
		t.Run(tc.name, func(t *testing.T) {
			bin, err := exec.LookPath("sleep")
			if err != nil {
				t.Skip("no sleep binary")
			}
			// A child that ignores SIGTERM outlives the grace, so the SIGKILL is sent.
			cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; echo ready; exec "+bin+" 60")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			if _, err := bufio.NewReader(out).ReadString('\n'); err != nil { // the trap is set
				t.Fatal(err)
			}
			kill := func(pid int, sig syscall.Signal) error {
				if sig == tc.fail {
					return errors.New("refused")
				}
				return syscall.Kill(pid, sig)
			}
			p, err := childproof.NewForTest(childproof.ReadIdentityForTest, kill)
			if err != nil {
				t.Fatal(err)
			}
			c, err := p.Spawned(cmd)
			if err != nil {
				t.Fatal(err)
			}
			err = Terminate(p, c, 20*time.Millisecond)
			var te *TerminateError
			if !errors.As(err, &te) || te.Step != tc.step || te.Pid != cmd.Process.Pid {
				t.Fatalf("Terminate = %v, want a TerminateError for %s of pid %d", err, tc.step, cmd.Process.Pid)
			}
		})
	}
}
