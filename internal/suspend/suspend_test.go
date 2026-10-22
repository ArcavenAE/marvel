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
	// killErr, when set, can refuse a signal before it is applied.
	killErr func(pid int, sig syscall.Signal) error
	// onRead, when set, runs before each stop-state read.
	onRead func(pid int)
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
	if o.killErr != nil {
		if err := o.killErr(pid, sig); err != nil {
			return err
		}
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
	if o.onRead != nil {
		o.onRead(pid)
	}
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
// saved or signalled, and so is a pid of 1 or below, or any recorded pid past
// 32 bits.
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
		// The fake holds a process at the wide pid, so only the range check
		// refuses it; on darwin the syscalls would act on pid 100.
		{"server pid 2^32+100", func(o *fakeOS) Record {
			o.procs[big] = &fakeProc{id: ident(big)}
			return record(big, 200)
		}},
		{"seat pid 2^32+100", func(o *fakeOS) Record {
			o.procs[big] = &fakeProc{id: ident(big)}
			return record(100, 200, big)
		}},
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

// big is a pid past 32 bits whose low 32 bits are 100.
const big = 1<<32 + 100

// Thaw refuses a record holding a pid past 32 bits before it saves or signals.
func TestThawRefusesAPidPast32Bits(t *testing.T) {
	for name, rec := range map[string]Record{"server": record(big, 200), "seat": record(100, 200, big)} {
		t.Run(name, func(t *testing.T) {
			o := newFakeOS(100, 200, big)
			rec.State = StateSuspended
			st := &memStore{os: o}
			if _, err := o.freezer(t).Thaw(rec, st); err == nil {
				t.Fatal("Thaw accepted a pid past 32 bits")
			}
			if len(st.saves) != 0 || st.cleared || len(o.log) != 0 {
				t.Errorf("saves %v, cleared %v, signals %v; want none", st.saves, st.cleared, o.log)
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
// clears the record. A record left resuming by a daemon that stopped partway
// is resumed the same way.
func TestThawResumesSeatsThenServerAndClears(t *testing.T) {
	for _, from := range []State{StateSuspended, StateSuspending, StateResuming} {
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

// ServerGone is true only when the recorded server exited or its pid names
// another process; a pid childproof refuses is an error, not a reboot.
func TestServerGone(t *testing.T) {
	o := newFakeOS(100)
	f := o.freezer(t)
	if gone, err := f.ServerGone(record(100)); gone || err != nil {
		t.Errorf("live server: gone %v err %v, want false, nil", gone, err)
	}
	o.procs[100].id.Start = "test:reused"
	if gone, err := f.ServerGone(record(100)); !gone || err != nil {
		t.Errorf("reused pid: gone %v err %v, want true, nil", gone, err)
	}
	if gone, err := f.ServerGone(record(300)); !gone || err != nil {
		t.Errorf("exited server: gone %v err %v, want true, nil", gone, err)
	}
	o.procs[big] = &fakeProc{id: ident(big)}
	if gone, err := f.ServerGone(record(big)); gone || err == nil {
		t.Errorf("pid past 32 bits: gone %v err %v, want false and an error", gone, err)
	}
	if len(o.log) != 0 {
		t.Errorf("signals = %v, want none", o.log)
	}
}

func TestThawNeedsASuspendedRecord(t *testing.T) {
	o := newFakeOS(100)
	for _, s := range []State{"", "frozen"} {
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

// A SIGCONT that fails leaves a process stopped, so the record that can retry
// it stays, as resuming, and a second Thaw finishes once the signal works.
func TestThawKeepsTheRecordWhenAResumeFails(t *testing.T) {
	o := newFakeOS(100, 200, 201)
	for _, p := range o.procs {
		p.stopped = true
	}
	refuse := true
	o.killErr = func(pid int, sig syscall.Signal) error {
		if refuse && pid == 200 && sig == syscall.SIGCONT {
			return errors.New("operation not permitted")
		}
		return nil
	}
	rec := record(100, 200, 201)
	rec.State = StateSuspended
	st := &memStore{os: o}
	if _, err := o.freezer(t).Thaw(rec, st); err == nil {
		t.Fatal("Thaw succeeded although a SIGCONT failed")
	}
	if st.cleared || st.rec == nil || st.rec.State != StateResuming {
		t.Fatalf("cleared %v, record %+v; want the record kept as resuming", st.cleared, st.rec)
	}
	refuse = false
	if _, err := o.freezer(t).Thaw(*st.rec, st); err != nil {
		t.Fatalf("second Thaw: %v", err)
	}
	if !st.cleared || o.procs[200].stopped {
		t.Errorf("cleared %v, seat 200 stopped %v; want the retry to resume it and clear", st.cleared, o.procs[200].stopped)
	}
}

// The server is proven again between its two stop reads, as the seats are: a
// pid reused by another stopped process after the first read must not read as
// a frozen server on the second.
func TestFreezeRollsBackWhenTheServerPidIsReusedBetweenReads(t *testing.T) {
	o := newFakeOS(100, 200)
	reads := 0
	o.onRead = func(pid int) {
		if pid != 100 {
			return
		}
		if reads++; reads == 1 {
			o.procs[100].id.Start = "test:reused"
		}
	}
	st := &memStore{os: o}
	_, _, err := o.freezer(t).Freeze(record(100, 200), st)
	if err == nil {
		t.Fatal("Freeze succeeded although the server pid named another process after the first read")
	}
	if slices.Contains(o.log, "CONT 100") {
		t.Errorf("signals = %v: the reused pid was signalled", o.log)
	}
}
