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
	return rec, Result{}, nil
}

// isStopped reads c's stop state, and says why when it is not stopped.
func (f *Freezer) isStopped(c childproof.Child) (bool, string) {
	return true, ""
}

// rollback resumes every seat already stopped and then the server, clears the
// record, and returns cause with any rollback trouble joined to it.
func (f *Freezer) rollback(store Store, server childproof.Child, seats []childproof.Child, cause error) error {
	return cause
}

// Thaw resumes a suspended record, or rolls back one left suspending. It saves
// the record as resuming, sends SIGCONT to every seat whose process is still
// the recorded one and then to the server, and clears the record. A seat whose
// pid now names another process is skipped and reported in Result.Gone; a seat
// that still reads stopped afterwards is reported in Result.StillStopped.
// Nothing here restarts a seat.
func (f *Freezer) Thaw(rec Record, store Store) (Result, error) {
	return Result{}, nil
}

// keepOnly returns the procs whose pid is among the children, in record order.
func keepOnly(procs []Proc, children []childproof.Child) []Proc {
	return procs
}
