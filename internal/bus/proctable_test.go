package bus

import (
	"errors"
	"sync"
	"syscall"
	"testing"

	"github.com/arcavenae/marvel/internal/childproof"
)

// tables maps a supervisor a test built to the process table it was given, so
// a test that needs the table does not widen newTestSupervisor's results.
var tables sync.Map // *Supervisor -> *procTable

func tableOf(s *Supervisor) *procTable {
	v, ok := tables.Load(s)
	if !ok {
		panic("tableOf: a supervisor that newTestSupervisor did not build")
	}
	return v.(*procTable)
}

// attach gives s a Prober over pt and registers every broker s spawns. It is
// the single registration point: newTestSupervisor calls it, and a test that
// builds a second supervisor over the same layout calls it with the table of
// the first, so the second can signal the broker the first spawned.
func attach(t *testing.T, s *Supervisor, pt *procTable) {
	t.Helper()
	p, err := childproof.NewForTest(pt.read, pt.kill)
	if err != nil {
		t.Fatal(err)
	}
	s.prober = p
	s.onSpawn = pt.register
	tables.Store(s, pt)
	t.Cleanup(func() { tables.Delete(s) })
}

// procTable is the one process table a supervisor test sees. It is the
// registration point of docs/design/bus-pidfile-identity.md section 7:
//
//   - a real broker the supervisor started is registered through the spawn
//     hook, and a signal to it is forwarded to the kernel;
//   - a fake process the test planted answers reads from a map, and a signal
//     to it is recorded and goes nowhere;
//   - any other pid is a process the test did not start, so a signal to it
//     fails the test and is not sent.
//
// No fixture names a literal pid that could belong to a live process: a fake
// is a pid the test reserved from a child it started and reaped, or a real
// child the test started and registered.
type procTable struct {
	t       *testing.T
	mu      sync.Mutex
	fake    map[int]childproof.Identity
	real    map[int]bool
	sent    []sentSignal
	readLog []int
	// failReads makes reads of these pids fail, as a process that vanished
	// between a proof and a signal would.
	failReads map[int]bool
	// onKill runs after a signal to a fake process is recorded.
	onKill func(pid int, sig syscall.Signal)
}

type sentSignal struct {
	pid int
	sig syscall.Signal
}

func newProcTable(t *testing.T) *procTable {
	t.Helper()
	return &procTable{t: t, fake: map[int]childproof.Identity{}, real: map[int]bool{}, failReads: map[int]bool{}}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// register marks a real process the test or the supervisor started.
func (p *procTable) register(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.real[pid] = true
}

func (p *procTable) plant(pid int, id childproof.Identity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fake[pid] = id
}

func (p *procTable) read(pid int) (childproof.Identity, error) {
	p.mu.Lock()
	p.readLog = append(p.readLog, pid)
	id, fake := p.fake[pid]
	fail := p.failReads[pid]
	p.mu.Unlock()
	if fail {
		return childproof.Identity{}, syscall.ESRCH
	}
	if fake {
		return id, nil
	}
	return childproof.ReadIdentity(pid)
}

func (p *procTable) kill(pid int, sig syscall.Signal) error {
	p.mu.Lock()
	p.sent = append(p.sent, sentSignal{pid, sig})
	_, fake := p.fake[abs(pid)]
	real := p.real[abs(pid)]
	hook := p.onKill
	p.mu.Unlock()
	switch {
	case fake:
		if hook != nil {
			hook(pid, sig)
		}
		return nil
	case real:
		return syscall.Kill(pid, sig)
	default:
		p.t.Errorf("the test signalled pid %d (signal %d), a process it did not start", pid, sig)
		return errors.New("refused: not a process this test started")
	}
}

func (p *procTable) signals() []sentSignal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sentSignal(nil), p.sent...)
}

func (p *procTable) signalsTo(pid int) []sentSignal {
	var out []sentSignal
	for _, s := range p.signals() {
		if abs(s.pid) == pid {
			out = append(out, s)
		}
	}
	return out
}

func (p *procTable) change(pid int, id childproof.Identity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fake[pid] = id
}

func (p *procTable) failReadsOf(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failReads[pid] = true
}

func (p *procTable) readPids() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.readLog...)
}
