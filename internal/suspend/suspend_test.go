package suspend

import (
	"errors"
	"fmt"
	"slices"
	"syscall"
	"testing"

	"github.com/arcavenae/marvel/internal/childproof"
)

// fakeProc is one process in the fake table.
type fakeProc struct {
	id      childproof.Identity
	dead    bool
	stopped bool
	// reads scripts what the stop-state reader answers, one entry per read;
	// once used up it answers the live stopped field.
	reads []bool
}

// fakeOS is a process table the Freezer reaches only through childproof's
// test seams and its own stopped reader. It logs every signal in order.
type fakeOS struct {
	procs map[int]*fakeProc
	log   []string
	// onSignal, when set, runs after each signal is applied.
	onSignal func(pid int, sig syscall.Signal)
}

func ident(pid int) childproof.Identity {
	return childproof.Identity{Start: fmt.Sprintf("test:%d", pid), Exe: "/bin/sleep", Argv: []string{"sleep", "600"}}
}

func newFakeOS(pids ...int) *fakeOS {
	o := &fakeOS{procs: map[int]*fakeProc{}}
	for _, p := range pids {
		o.procs[p] = &fakeProc{id: ident(p)}
	}
	return o
}

func (o *fakeOS) read(pid int) (childproof.Identity, error) {
	p, ok := o.procs[pid]
	if !ok || p.dead {
		return childproof.Identity{}, errors.New("no such process")
	}
	return p.id, nil
}

func (o *fakeOS) kill(pid int, sig syscall.Signal) error {
	p, ok := o.procs[pid]
	if !ok || p.dead {
		return syscall.ESRCH
	}
	switch sig {
	case syscall.SIGSTOP:
		o.log = append(o.log, fmt.Sprintf("STOP %d", pid))
		p.stopped = true
	case syscall.SIGCONT:
		o.log = append(o.log, fmt.Sprintf("CONT %d", pid))
		p.stopped = false
	}
	if o.onSignal != nil {
		o.onSignal(pid, sig)
	}
	return nil
}

func (o *fakeOS) stoppedRead(pid int) (bool, error) {
	p, ok := o.procs[pid]
	if !ok || p.dead {
		return false, errors.New("no such process")
	}
	if len(p.reads) > 0 {
		r := p.reads[0]
		p.reads = p.reads[1:]
		return r, nil
	}
	return p.stopped, nil
}

func (o *fakeOS) freezer(t *testing.T) *Freezer {
	t.Helper()
	pr, err := childproof.NewForTest(o.read, o.kill)
	if err != nil {
		t.Fatal(err)
	}
	return &Freezer{prober: pr, stopped: o.stoppedRead}
}

// memStore records every save's state and the signal log length at that save.
type memStore struct {
	os      *fakeOS
	saves   []State
	atSave  []int
	cleared bool
	rec     *Record
}

func (s *memStore) Save(r Record) error {
	s.saves = append(s.saves, r.State)
	s.atSave = append(s.atSave, len(s.os.log))
	s.rec = &r
	return nil
}

func (s *memStore) Clear() error { s.cleared = true; s.rec = nil; return nil }

func record(server int, seats ...int) Record {
	r := Record{Actor: "test", Source: "cli", Server: Proc{Pid: server, Identity: ident(server)}}
	for _, p := range seats {
		r.Seats = append(r.Seats, Proc{Pid: p, Identity: ident(p)})
	}
	return r
}

// The record is saved as suspending before any signal, the server is stopped
// before any seat, and a suspend that held is saved as suspended.
func TestFreezeSavesFirstAndStopsTheServerFirst(t *testing.T) {
	o := newFakeOS(100, 200, 201)
	st := &memStore{os: o}
	rec, res, err := o.freezer(t).Freeze(record(100, 200, 201), st)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if !slices.Equal(st.saves, []State{StateSuspending, StateSuspended}) {
		t.Errorf("saves = %v, want suspending then suspended", st.saves)
	}
	if len(st.atSave) == 0 || st.atSave[0] != 0 {
		t.Errorf("the first save came after %v signals, want before any", st.atSave)
	}
	if want := []string{"STOP 100", "STOP 200", "STOP 201"}; !slices.Equal(o.log, want) {
		t.Errorf("signals = %v, want %v (server first)", o.log, want)
	}
	if rec.State != StateSuspended || len(rec.Seats) != 2 || len(res.Gone) != 0 {
		t.Errorf("record %s with %d seats, gone %v; want suspended, 2 seats, none gone", rec.State, len(rec.Seats), res.Gone)
	}
}

// A stop counts only when read twice: tmux undoes a pane stop, and the stop
// reads T for a moment first. A failed suspend resumes the seats it stopped,
// then the server, and clears the record.
func TestFreezeCountsAStopOnlyWhenReadTwice(t *testing.T) {
	o := newFakeOS(100, 200, 201)
	o.procs[200].reads = []bool{true, false}
	st := &memStore{os: o}
	_, _, err := o.freezer(t).Freeze(record(100, 200, 201), st)
	var fe *FreezeError
	if !errors.As(err, &fe) || fe.Pid != 200 {
		t.Fatalf("Freeze error = %v, want a FreezeError for pid 200", err)
	}
	want := []string{"STOP 100", "STOP 200", "STOP 201", "CONT 200", "CONT 201", "CONT 100"}
	if !slices.Equal(o.log, want) {
		t.Errorf("signals = %v, want %v (seats resumed, server last)", o.log, want)
	}
	if !st.cleared || !slices.Equal(st.saves, []State{StateSuspending}) {
		t.Errorf("saves %v, cleared %v; want suspending then cleared", st.saves, st.cleared)
	}
}

// A seat that tmux resumes (it never reads stopped) fails the suspend.
func TestFreezeRollsBackWhenAStopIsUndone(t *testing.T) {
	o := newFakeOS(100, 200)
	o.onSignal = func(pid int, sig syscall.Signal) {
		if pid == 200 && sig == syscall.SIGSTOP {
			o.procs[200].stopped = false
		}
	}
	st := &memStore{os: o}
	if _, _, err := o.freezer(t).Freeze(record(100, 200), st); err == nil {
		t.Fatal("Freeze succeeded with a seat that never stopped")
	}
	if !st.cleared || o.procs[100].stopped {
		t.Errorf("cleared %v, server stopped %v; want the record cleared and the server resumed", st.cleared, o.procs[100].stopped)
	}
}

// A seat that is gone before the stop, or that exits during it, is not a
// failure: it is dropped from the record and reported.
func TestFreezeDropsASeatThatExits(t *testing.T) {
	o := newFakeOS(100, 200, 201, 202)
	o.procs[201].dead = true
	o.onSignal = func(pid int, sig syscall.Signal) {
		if pid == 202 && sig == syscall.SIGSTOP {
			o.procs[202].dead = true
		}
	}
	st := &memStore{os: o}
	rec, res, err := o.freezer(t).Freeze(record(100, 200, 201, 202), st)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	slices.Sort(res.Gone)
	if !slices.Equal(res.Gone, []int{201, 202}) {
		t.Errorf("gone = %v, want [201 202]", res.Gone)
	}
	if len(rec.Seats) != 1 || rec.Seats[0].Pid != 200 {
		t.Errorf("recorded seats = %v, want only 200", rec.Seats)
	}
	if slices.Contains(o.log, "STOP 201") {
		t.Errorf("a seat gone before the stop was signalled: %v", o.log)
	}
}

// A server that is not the recorded process is refused before anything is
// saved or signalled, and so is a pid of 1 or below.
func TestFreezeRefusesAServerItCannotProve(t *testing.T) {
	for _, c := range []struct {
		name string
		rec  func(o *fakeOS) Record
	}{
		{"start changed", func(o *fakeOS) Record {
			o.procs[100].id.Start = "test:reused"
			return record(100, 200)
		}},
		{"pid 1", func(o *fakeOS) Record {
			o.procs[1] = &fakeProc{id: ident(1)}
			return record(1, 200)
		}},
		{"pid 0", func(*fakeOS) Record { return record(0, 200) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := newFakeOS(100, 200)
			st := &memStore{os: o}
			if _, _, err := o.freezer(t).Freeze(c.rec(o), st); err == nil {
				t.Fatal("Freeze accepted a server it cannot prove")
			}
			if len(st.saves) != 0 || len(o.log) != 0 {
				t.Errorf("saves %v, signals %v; want none", st.saves, o.log)
			}
		})
	}
}

func TestFreezeNeedsANewRecord(t *testing.T) {
	o := newFakeOS(100)
	rec := record(100)
	rec.State = StateSuspended
	if _, _, err := o.freezer(t).Freeze(rec, &memStore{os: o}); !errors.Is(err, ErrState) {
		t.Errorf("Freeze of a suspended record = %v, want ErrState", err)
	}
}

// Resume saves resuming first, resumes the seats and then the server, and
// clears the record.
func TestThawResumesSeatsThenServerAndClears(t *testing.T) {
	for _, from := range []State{StateSuspended, StateSuspending} {
		t.Run(string(from), func(t *testing.T) {
			o := newFakeOS(100, 200, 201)
			for _, p := range o.procs {
				p.stopped = true
			}
			rec := record(100, 200, 201)
			rec.State = from
			st := &memStore{os: o}
			res, err := o.freezer(t).Thaw(rec, st)
			if err != nil {
				t.Fatalf("Thaw: %v", err)
			}
			if !slices.Equal(st.saves, []State{StateResuming}) || st.atSave[0] != 0 || !st.cleared {
				t.Errorf("saves %v at %v, cleared %v; want resuming before any signal, then cleared", st.saves, st.atSave, st.cleared)
			}
			if want := []string{"CONT 200", "CONT 201", "CONT 100"}; !slices.Equal(o.log, want) {
				t.Errorf("signals = %v, want %v (server last)", o.log, want)
			}
			if len(res.Gone) != 0 || len(res.StillStopped) != 0 {
				t.Errorf("result = %+v, want nothing gone or still stopped", res)
			}
		})
	}
}

// A recorded pid whose process changed (a different start time) names another
// process now, so it is not signalled.
func TestThawSkipsAPidThatNamesAnotherProcess(t *testing.T) {
	o := newFakeOS(100, 200, 201)
	o.procs[201].id.Start = "test:reused"
	rec := record(100, 200, 201)
	rec.State = StateSuspended
	res, err := o.freezer(t).Thaw(rec, &memStore{os: o})
	if err != nil {
		t.Fatalf("Thaw: %v", err)
	}
	if slices.Contains(o.log, "CONT 201") {
		t.Errorf("a reused pid was signalled: %v", o.log)
	}
	if !slices.Equal(res.Gone, []int{201}) {
		t.Errorf("gone = %v, want [201]", res.Gone)
	}
}

// A seat that still reads stopped after the resume is reported, never retried
// or restarted here.
func TestThawReportsASeatStillStopped(t *testing.T) {
	o := newFakeOS(100, 200)
	o.onSignal = func(pid int, sig syscall.Signal) {
		if pid == 200 && sig == syscall.SIGCONT {
			o.procs[200].stopped = true
		}
	}
	rec := record(100, 200)
	rec.State = StateSuspended
	res, err := o.freezer(t).Thaw(rec, &memStore{os: o})
	if err != nil {
		t.Fatalf("Thaw: %v", err)
	}
	if !slices.Equal(res.StillStopped, []int{200}) {
		t.Errorf("still stopped = %v, want [200]", res.StillStopped)
	}
}

func TestThawNeedsASuspendedRecord(t *testing.T) {
	o := newFakeOS(100)
	for _, s := range []State{"", StateResuming} {
		rec := record(100)
		rec.State = s
		if _, err := o.freezer(t).Thaw(rec, &memStore{os: o}); !errors.Is(err, ErrState) {
			t.Errorf("Thaw from %q = %v, want ErrState", s, err)
		}
	}
	if len(o.log) != 0 {
		t.Errorf("signals = %v, want none", o.log)
	}
}
