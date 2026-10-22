package suspend

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// spawnSleep starts a sleep this test owns and proves it. Only these
// processes are ever signalled by the real-process tests.
func spawnSleep(t *testing.T, pr *childproof.Prober) Proc {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGCONT)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	c, err := pr.Spawned(cmd)
	if err != nil {
		t.Fatalf("prove the sleep: %v", err)
	}
	return Proc{Pid: c.Pid(), Identity: c.Identity()}
}

// waitStopped polls the reader for up to 2 s until it answers want.
func waitStopped(t *testing.T, pid int, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := readStopped(pid)
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d: stopped = %v (err %v), want %v", pid, got, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The platform reader follows a real process into and out of the stop state.
func TestReadStoppedFollowsARealProcess(t *testing.T) {
	p := spawnSleep(t, childproof.New())
	waitStopped(t, p.Pid, false)
	if err := syscall.Kill(p.Pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, p.Pid, true)
	if err := syscall.Kill(p.Pid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, p.Pid, false)
}

// Freeze and Thaw on real processes with no tmux above them: both stop, both
// resume, and the record goes suspending, suspended, resuming, cleared.
func TestFreezeAndThawRealProcesses(t *testing.T) {
	f := New()
	f.settle = 50 * time.Millisecond
	server, seat := spawnSleep(t, f.prober), spawnSleep(t, f.prober)
	st := &recStore{}
	rec, _, err := f.Freeze(Record{Actor: "test", Source: "cli", Server: server, Seats: []Proc{seat}}, st)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	waitStopped(t, server.Pid, true)
	waitStopped(t, seat.Pid, true)
	res, err := f.Thaw(rec, st)
	if err != nil || len(res.Gone) != 0 || len(res.StillStopped) != 0 {
		t.Fatalf("Thaw: %+v, %v", res, err)
	}
	waitStopped(t, server.Pid, false)
	waitStopped(t, seat.Pid, false)
	want := []State{StateSuspending, StateSuspended, StateResuming}
	if len(st.saves) != 3 || st.saves[0] != want[0] || st.saves[1] != want[1] || st.saves[2] != want[2] || !st.cleared {
		t.Errorf("saves %v, cleared %v; want %v then cleared", st.saves, st.cleared, want)
	}
}

type recStore struct {
	saves   []State
	cleared bool
}

func (s *recStore) Save(r Record) error { s.saves = append(s.saves, r.State); return nil }
func (s *recStore) Clear() error        { s.cleared = true; return nil }
