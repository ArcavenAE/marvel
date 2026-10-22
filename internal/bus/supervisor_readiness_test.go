package bus

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"
)

// These tests pin the readiness rule of restructure plan section 3: a
// structural reading counts for the child it was read from, and a respawned
// child is ready only after its own first reading (Started), not on its dead
// predecessor's.

var (
	healthyReading   = Structure{Authorized: true}
	unhealthyReading = Structure{Missing: []string{"stream AGENT_INBOX"}, Authorized: true}
)

// scriptedStructure answers the nth structural read from a script: a result,
// an error, or a gate that holds the read until the test opens it.
type scriptedStructure struct {
	mu    sync.Mutex
	calls int
	steps map[int]readingStep
	dflt  Structure
}

type readingStep struct {
	gate chan struct{} // closed by the test to let the read return
	got  chan struct{} // closed when the read has started, if non-nil
	st   Structure
	err  error
}

func (r *scriptedStructure) read(ctx context.Context) (Structure, error) {
	r.mu.Lock()
	r.calls++
	step, ok := r.steps[r.calls]
	r.mu.Unlock()
	if !ok {
		return r.dflt, nil
	}
	if step.got != nil {
		close(step.got)
	}
	if step.gate != nil {
		select {
		case <-step.gate:
		case <-ctx.Done():
			return Structure{}, ctx.Err()
		}
	}
	return step.st, step.err
}

// killAndAwaitRespawn kills the running broker and returns once Process has
// spawned the next one and marked it ready, which is before Started has run.
func killAndAwaitRespawn(t *testing.T, s *Supervisor) {
	t.Helper()
	before := s.snapshot()
	if err := syscall.Kill(before.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the respawned broker", func() bool {
		snap := s.snapshot()
		return snap.Gen > before.Gen && snap.Ready
	})
}

func quietTicker(s *Supervisor) {
	s.AfterReady = nil
	s.leafPoll = time.Hour
	s.writeProbeFn = func(context.Context) (WriteReading, error) { return WriteReading{}, nil }
	s.headroomFn = func(context.Context) (Headroom, error) { return Headroom{}, nil }
}

// A respawned child is not ready between Process marking it ready and its own
// first reading, even though the dead child's last reading was healthy.
func TestARespawnedBrokerIsNotReadyBeforeItsOwnFirstReading(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	quietTicker(s)
	gate, got := make(chan struct{}), make(chan struct{})
	r := &scriptedStructure{dflt: healthyReading, steps: map[int]readingStep{2: {gate: gate, got: got, st: healthyReading}}}
	s.checkStructureFn = r.read
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Ready() {
		t.Fatal("not ready after the first start")
	}
	killAndAwaitRespawn(t, s)
	<-got // Started is inside the new child's first reading
	if s.Ready() || s.Status().Ready {
		t.Error("Ready is true for the respawned child before its first reading")
	}
	close(gate)
	waitUntil(t, "ready after the first reading", s.Ready)
}

// A failed first reading of the new child leaves nothing of the dead child's
// unhealthy reading in effect: there is no reading for this child, which reads
// as ready, as a first start does.
func TestAFailedFirstReadingKeepsNothingFromTheDeadChild(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	quietTicker(s)
	r := &scriptedStructure{dflt: healthyReading, steps: map[int]readingStep{
		1: {st: unhealthyReading},
		2: {err: errors.New("the monitor did not answer")},
	}}
	s.checkStructureFn = r.read
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Ready() {
		t.Fatal("ready on an unhealthy first reading")
	}
	killAndAwaitRespawn(t, s)
	waitUntil(t, "the respawned broker to settle", s.Ready)
	if st := s.Status(); st.Structure != nil {
		t.Errorf("structure = %+v, want none: the only reading is the dead child's", st.Structure)
	}
}

// A reading whose read began before a respawn is dropped when it lands after
// it, and does not change Ready.
func TestAReadingStartedBeforeARespawnIsDropped(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	quietTicker(s)
	gate, got := make(chan struct{}), make(chan struct{})
	r := &scriptedStructure{dflt: healthyReading, steps: map[int]readingStep{2: {gate: gate, got: got, st: unhealthyReading}}}
	s.checkStructureFn = r.read
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.checkStructure(context.Background()) }() // the delayed reading from Reload
	<-got
	killAndAwaitRespawn(t, s)
	waitUntil(t, "ready on the new child's own reading", s.Ready)
	close(gate)
	wg.Wait()
	if !s.Ready() {
		t.Error("a reading from the dead child made the new one not ready")
	}
	if st := s.Status(); st.Structure == nil || !st.Structure.Provisioned {
		t.Errorf("structure = %+v, want the new child's healthy reading", st.Structure)
	}
}

// A reading whose read began after the respawn is kept: its generation is
// taken when the read starts, not when it lands.
func TestAReadingStartedAfterARespawnIsKept(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	quietTicker(s)
	r := &scriptedStructure{dflt: healthyReading, steps: map[int]readingStep{3: {st: unhealthyReading}}}
	s.checkStructureFn = r.read
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	killAndAwaitRespawn(t, s)
	waitUntil(t, "ready on the new child's own reading", s.Ready)
	s.checkStructure(context.Background()) // the third read, after the respawn
	if s.Ready() {
		t.Error("an unhealthy reading of the current child was dropped")
	}
	if st := s.Status(); st.Structure == nil || st.Structure.Provisioned {
		t.Errorf("structure = %+v, want the unhealthy reading kept", st.Structure)
	}
}

// Two readings overlap (the ticker's and Reload's delayed one). The one that
// started first must not overwrite the one that started later when it lands
// second, whichever child they are of.
func TestAnEarlierReadingDoesNotOverwriteALaterOne(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	quietTicker(s)
	gate, got := make(chan struct{}), make(chan struct{})
	r := &scriptedStructure{dflt: unhealthyReading, steps: map[int]readingStep{
		1: {st: healthyReading},
		2: {gate: gate, got: got, st: healthyReading},
	}}
	s.checkStructureFn = r.read
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.checkStructure(context.Background()) }() // read 2, held open
	<-got
	s.checkStructure(context.Background()) // read 3 starts later and lands first: unhealthy
	if s.Ready() {
		t.Fatal("ready on an unhealthy reading")
	}
	close(gate)
	wg.Wait()
	if s.Ready() {
		t.Error("a reading that started earlier overwrote the later one when it landed")
	}
}
