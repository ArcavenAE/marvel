// Package suspend freezes the processes of a fleet in place and resumes them
// (docs/design/fleet-suspend-resume.md section 2). It holds the mechanism only:
// the record, the stop order, the twice-read stop check, the rollback and the
// resume. Collecting the pids, storing the record and the verbs that call this
// are later steps.
//
// The order is the measured one (finding-marvel-1kg8 section 1, and the Linux
// run in scripts/probes/linux-stop-state.sh): the tmux server resumes a pane
// process that is stopped while it runs, so the server is stopped first and
// every seat after it. A process counts as frozen only when its stop state is
// read twice.
//
// Every signal goes through childproof, which reads the process's identity
// again before it signals, so a recorded pid that now names another process is
// never signalled.
package suspend

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// State is where a suspend stands. A cluster with no record is running.
type State string

const (
	// StateSuspending is written before the first signal. A record left in
	// this state means a suspend was interrupted, and it is rolled back.
	StateSuspending State = "suspending"
	// StateSuspended means every recorded process read stopped twice.
	StateSuspended State = "suspended"
	// StateResuming is written before the first resume signal.
	StateResuming State = "resuming"
)

// checkPids refuses a record holding a pid childproof would refuse, before
// anything is saved or signalled. Without it, Freeze would drop such a seat as
// gone instead of refusing the record.
func checkPids(rec Record) error {
	if err := childproof.CheckPid(rec.Server.Pid); err != nil {
		return fmt.Errorf("suspend: the tmux server: %w", err)
	}
	for _, s := range rec.Seats {
		if err := childproof.CheckPid(s.Pid); err != nil {
			return fmt.Errorf("suspend: a seat: %w", err)
		}
	}
	return nil
}

// Proc is one recorded process: its pid and the identity proven when it was
// collected.
type Proc struct {
	Pid      int                 `json:"pid"`
	Identity childproof.Identity `json:"identity"`
}

// Record is what a suspend writes down before it signals anything, so a daemon
// that restarts mid-suspend knows what to roll back or resume.
type Record struct {
	State State `json:"state"`
	// Actor and Source say who asked: Source is cli, watch-key or mrvl.
	Actor  string    `json:"actor"`
	Source string    `json:"source"`
	Since  time.Time `json:"since"`
	// Server is the tmux server, stopped first and resumed last.
	Server Proc `json:"server"`
	// Seats are every other recorded process.
	Seats []Proc `json:"seats"`
}

// Store keeps the record. Save replaces it; Clear removes it. The daemon's
// store is a later step; tests use a fake.
type Store interface {
	Save(Record) error
	Clear() error
}

// Result says what a Freeze or a Thaw did beyond succeeding.
type Result struct {
	// Gone are recorded pids whose process exited, or whose pid now names a
	// different process. They are not failures and were not signalled after
	// they went.
	Gone []int
	// StillStopped are pids that a Thaw resumed and that still read stopped.
	StillStopped []int
}

// Freezer stops and resumes recorded processes.
type Freezer struct {
	prober *childproof.Prober
	// stopped reads whether pid is in the stop state now.
	stopped func(pid int) (bool, error)
	// settle is the pause between the two stop reads, and before the read
	// after a resume.
	settle time.Duration
}

// New returns the production Freezer: childproof's prober and the platform
// stop-state reader.
func New() *Freezer {
	return &Freezer{prober: childproof.New(), stopped: readStopped, settle: 200 * time.Millisecond}
}

// NewForTest returns a Freezer over prober and a stop-state reader, for a
// package that drives a Freezer against a fake process table. The pause
// between reads is zero.
func NewForTest(prober *childproof.Prober, stopped func(pid int) (bool, error)) *Freezer {
	return &Freezer{prober: prober, stopped: stopped}
}

// ServerGone reports whether rec's tmux server has exited, or its pid now
// names a different process: a reboot, or a server killed by hand. A pid whose
// identity cannot be read counts as gone only when the pid no longer exists;
// a live server that cannot be read right now is an error, as is any other
// failure to prove the server, and the caller keeps the record.
func (f *Freezer) ServerGone(rec Record) (bool, error) {
	_, err := f.prober.Prove(rec.Server.Pid, rec.Server.Identity)
	var mm *childproof.MismatchError
	switch {
	case err == nil:
		return false, nil
	case errors.As(err, &mm):
		if mm.Reason != "dead" {
			return true, nil
		}
		alive, perr := f.prober.Exists(rec.Server.Pid)
		if perr != nil {
			return false, perr
		}
		if alive {
			return false, fmt.Errorf("suspend: the tmux server %d exists but its identity cannot be read", rec.Server.Pid)
		}
		return true, nil
	}
	return false, err
}

// ErrState is returned when a record is in a state the operation cannot start
// from.
var ErrState = errors.New("suspend: record is in the wrong state for this")

// FreezeError says a suspend did not take, which process did not stop, and
// that everything was resumed.
type FreezeError struct {
	Pid    int
	Reason string
}

func (e *FreezeError) Error() string {
	return fmt.Sprintf("suspend: pid %d did not stop (%s); everything stopped was resumed and the record cleared", e.Pid, e.Reason)
}

// Freeze stops rec.Server and then every seat, and saves the record as
// suspending before the first signal and as suspended once every process has
// read stopped twice. A seat that exits during the stop is dropped from the
// record and reported in Result.Gone. Any other process that does not stop
// rolls the suspend back: every process already stopped is resumed, the seats
// first and the server last, and the record is cleared.
func (f *Freezer) Freeze(rec Record, store Store) (Record, Result, error) {
	var res Result
	if rec.State != "" {
		return rec, res, fmt.Errorf("%w: freeze needs a new record, this one is %s", ErrState, rec.State)
	}
	if err := checkPids(rec); err != nil {
		return rec, res, err
	}
	server, err := f.prober.Prove(rec.Server.Pid, rec.Server.Identity)
	if err != nil {
		return rec, res, fmt.Errorf("suspend: the tmux server is not the recorded process: %w", err)
	}
	seats := make([]childproof.Child, 0, len(rec.Seats))
	kept := make([]Proc, 0, len(rec.Seats))
	for _, s := range rec.Seats {
		c, err := f.prober.Prove(s.Pid, s.Identity)
		if err != nil {
			res.Gone = append(res.Gone, s.Pid)
			continue
		}
		seats = append(seats, c)
		kept = append(kept, s)
	}
	rec.Seats = kept

	rec.State = StateSuspending
	if err := store.Save(rec); err != nil {
		return rec, res, fmt.Errorf("suspend: save the record before signalling: %w", err)
	}

	var signalled []childproof.Child
	if err := f.prober.Signal(server, syscall.SIGSTOP, false); err != nil {
		return rec, res, f.rollback(store, server, signalled, &FreezeError{Pid: server.Pid(), Reason: err.Error()})
	}
	live := make([]childproof.Child, 0, len(seats))
	for _, c := range seats {
		err := f.prober.Signal(c, syscall.SIGSTOP, false)
		var mm *childproof.MismatchError
		switch {
		case errors.As(err, &mm):
			res.Gone = append(res.Gone, c.Pid())
		case err != nil:
			return rec, res, f.rollback(store, server, signalled, &FreezeError{Pid: c.Pid(), Reason: err.Error()})
		default:
			signalled = append(signalled, c)
			live = append(live, c)
		}
	}

	// Twice, with a pause between, because a stop that tmux undoes reads T
	// for a moment first.
	for read := 0; read < 2; read++ {
		time.Sleep(f.settle)
		if !f.prober.Same(server) {
			return rec, res, f.rollback(store, server, signalled, &FreezeError{Pid: server.Pid(), Reason: "its pid no longer names the recorded process"})
		}
		if ok, why := f.isStopped(server); !ok {
			return rec, res, f.rollback(store, server, signalled, &FreezeError{Pid: server.Pid(), Reason: why})
		}
		still := live[:0]
		for _, c := range live {
			if !f.prober.Same(c) {
				res.Gone = append(res.Gone, c.Pid())
				continue
			}
			if ok, why := f.isStopped(c); !ok {
				return rec, res, f.rollback(store, server, signalled, &FreezeError{Pid: c.Pid(), Reason: why})
			}
			still = append(still, c)
		}
		live = still
	}

	rec.Seats = keepOnly(rec.Seats, live)
	rec.State = StateSuspended
	if err := store.Save(rec); err != nil {
		return rec, res, fmt.Errorf("suspend: every process is stopped, but the record could not be saved as suspended: %w", err)
	}
	return rec, res, nil
}

// isStopped reads c's stop state, and says why when it is not stopped.
func (f *Freezer) isStopped(c childproof.Child) (bool, string) {
	stopped, err := f.stopped(c.Pid())
	switch {
	case err != nil:
		return false, "its state could not be read: " + err.Error()
	case !stopped:
		return false, "it did not read stopped"
	}
	return true, ""
}

// rollback resumes every seat already stopped and then the server, clears the
// record, and returns cause with any rollback trouble joined to it.
func (f *Freezer) rollback(store Store, server childproof.Child, seats []childproof.Child, cause error) error {
	errs := []error{cause}
	for _, c := range seats {
		if err := f.prober.Signal(c, syscall.SIGCONT, false); err != nil {
			errs = append(errs, fmt.Errorf("rollback: resume pid %d: %w", c.Pid(), err))
		}
	}
	if err := f.prober.Signal(server, syscall.SIGCONT, false); err != nil {
		errs = append(errs, fmt.Errorf("rollback: resume the tmux server %d: %w", server.Pid(), err))
	}
	if err := store.Clear(); err != nil {
		errs = append(errs, fmt.Errorf("rollback: clear the record: %w", err))
	}
	return errors.Join(errs...)
}

// Thaw resumes a suspended record, rolls back one left suspending, or
// finishes a resume left resuming by a daemon that stopped partway. It saves
// the record as resuming, sends SIGCONT to every seat whose process is still
// the recorded one and then to the server, and clears the record. A seat whose
// pid now names another process is skipped and reported in Result.Gone; a seat
// that still reads stopped afterwards is reported in Result.StillStopped.
// Nothing here restarts a seat. When a SIGCONT fails for a reason other than a
// changed process, the record stays, as resuming, and the error is returned, so
// a second Thaw can retry: a resume is safe to repeat.
func (f *Freezer) Thaw(rec Record, store Store) (Result, error) {
	var res Result
	if rec.State != StateSuspended && rec.State != StateSuspending && rec.State != StateResuming {
		return res, fmt.Errorf("%w: thaw needs a suspended, suspending or resuming record, this one is %q", ErrState, rec.State)
	}
	if err := checkPids(rec); err != nil {
		return res, err
	}
	rec.State = StateResuming
	if err := store.Save(rec); err != nil {
		return res, fmt.Errorf("suspend: save the record before resuming: %w", err)
	}

	var (
		resumed []childproof.Child
		errs    []error
	)
	for _, s := range rec.Seats {
		c, err := f.prober.Prove(s.Pid, s.Identity)
		if err != nil {
			res.Gone = append(res.Gone, s.Pid)
			continue
		}
		err = f.prober.Signal(c, syscall.SIGCONT, false)
		var mm *childproof.MismatchError
		switch {
		case errors.As(err, &mm):
			res.Gone = append(res.Gone, s.Pid)
		case err != nil:
			errs = append(errs, fmt.Errorf("suspend: resume pid %d: %w", s.Pid, err))
		default:
			resumed = append(resumed, c)
		}
	}
	server, err := f.prober.Prove(rec.Server.Pid, rec.Server.Identity)
	if err != nil {
		res.Gone = append(res.Gone, rec.Server.Pid)
	} else if err := f.prober.Signal(server, syscall.SIGCONT, false); err != nil {
		errs = append(errs, fmt.Errorf("suspend: resume the tmux server %d: %w", server.Pid(), err))
	} else {
		resumed = append(resumed, server)
	}

	time.Sleep(f.settle)
	for _, c := range resumed {
		if stopped, err := f.stopped(c.Pid()); err == nil && stopped {
			res.StillStopped = append(res.StillStopped, c.Pid())
		}
	}
	if len(errs) > 0 {
		return res, errors.Join(errs...)
	}
	if err := store.Clear(); err != nil {
		return res, fmt.Errorf("suspend: clear the record after resuming: %w", err)
	}
	return res, nil
}

// keepOnly returns the procs whose pid is among the children, in record order.
func keepOnly(procs []Proc, children []childproof.Child) []Proc {
	in := make(map[int]bool, len(children))
	for _, c := range children {
		in[c.Pid()] = true
	}
	out := make([]Proc, 0, len(children))
	for _, p := range procs {
		if in[p.Pid] {
			out = append(out, p)
		}
	}
	return out
}
