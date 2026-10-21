package workload

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/backoff"
	"github.com/arcavenae/marvel/internal/childproof"
)

// The supervised lifecycle of a managed child (docs/design/code-restructure-plan.md,
// Move 2). The driver decides how a child enters (a fresh spawn, or an adopt of
// a child it has already proven), and Process runs it from there: the watch,
// crash detection, the restart wait, a requested restart and the stop. Nothing
// calls this yet; bus.Supervisor becomes its first driver in PR 2c.

// Driver is what a managed child's driver supplies to the generic loop.
// Ready, Started, Tick and Exited run on the Process loop goroutine, one at
// a time, with no Process lock held.
type Driver interface {
	// Ready blocks until the child serves.
	Ready(ctx context.Context, c ChildInfo) error
	// Started runs once the loop has marked the child ready.
	Started(ctx context.Context, s StartInfo)
	// Tick runs on the loop's one ticker, under a deadline below its period.
	Tick(ctx context.Context)
	// Exited runs when the child exits.
	Exited(e Exit)
}

// ChildInfo is the child a Driver method is about.
type ChildInfo struct {
	Child childproof.Child // the proven child; never a bare pid
	Env   []string         // the start environment; nil for an adopted child
	Gen   uint64           // bumped by Process on every spawn or adopt
}

// StartKind says how a child came to run.
type StartKind int

const (
	// Spawned is a child Spawn started.
	Spawned StartKind = iota
	// Adopted is a proven child a driver handed to Adopt.
	Adopted
	// Restarted is a child started after a crash or by Restart.
	Restarted
)

// StartInfo is what Started reports. (The plan names it Start, which is
// already this package's spawn function.)
type StartInfo struct {
	How      StartKind
	Restarts int
	Reason   string // why a requested restart happened; "" otherwise
	Child    ChildInfo
}

// Exit is what Exited reports.
type Exit struct {
	Err       error
	Requested bool // Stop or Restart asked for it
	Restarts  int
	RetryIn   time.Duration // the backoff wait before the respawn
}

// PidState values: what Process knows of the pidfile of the child it runs.
const (
	PidStateNone   = "none"
	PidStateProven = "proven"
)

// State is a copy of what Process holds, taken under one lock.
type State struct {
	Pid      int
	Child    childproof.Child
	Gen      uint64
	Ready    bool
	Adopted  bool
	Restarts int
	// PidState is proven once a child is spawned or adopted, and none before.
	// A driver that read the pidfile and found it stale or legacy reports that
	// itself.
	PidState string
	// BackoffUntil is when the respawn after a crash is due; zero when none
	// is pending.
	BackoffUntil time.Time
}

// DefaultGrace is the wait between SIGTERM and SIGKILL when a spec names none.
const DefaultGrace = 5 * time.Second

// defaultAdoptPoll is how often an adopted child's identity is re-read when a
// spec names no period.
const defaultAdoptPoll = 2 * time.Second

// ErrStopped is returned for a request made after Stop.
var ErrStopped = errors.New("workload: process stopped")

// Process supervises one managed child. Spawn, Adopt, Restart and Stop are
// requests to its loop goroutine, which serializes them with the crash
// respawn; p.mu guards only the state Snapshot copies. Process calls no
// Driver method and no ProcessSpec func while it holds p.mu.
type Process struct {
	spec   ProcessSpec
	driver Driver
	prober *childproof.Prober
	sched  backoff.Schedule

	// now and after are the clock; tests replace them.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu sync.Mutex
	st State

	looping atomic.Bool // set by the first Spawn or Adopt, which starts the loop
	reqs    chan request
	done    chan struct{} // closed when the loop ends
}

// New returns a Process for spec, driven by d. p is the prober every signal
// goes through; sched is the restart schedule after a crash. Nothing runs
// until Spawn or Adopt.
func New(spec ProcessSpec, d Driver, p *childproof.Prober, sched backoff.Schedule) *Process {
	return &Process{spec: spec, driver: d, prober: p, sched: sched, now: time.Now, after: time.After}
}

// Snapshot returns a copy of the state, taken under one lock.
func (p *Process) Snapshot() State { return State{} }

// Spawn starts the child.
func (p *Process) Spawn(ctx context.Context) error { return nil }

// Adopt takes a child the driver has already proven.
func (p *Process) Adopt(ctx context.Context, c childproof.Child) error { return nil }

// Restart ends the running child and starts a fresh one.
func (p *Process) Restart(reason string) error { return nil }

// Stop ends supervision.
func (p *Process) Stop(keep bool) {}

// Terminate ends a proven child.
func Terminate(p *childproof.Prober, c childproof.Child, grace time.Duration) error { return nil }

var (
	_ = errors.New
	_ = fmt.Errorf
	_ = log.Printf
	_ exec.Cmd
	_ = syscall.SIGTERM
	_ atomic.Bool
	_ = defaultAdoptPoll
	_ = reqSpawn
	_ = reqAdopt
	_ = reqRestart
	_ = reqStop
	_ request
	_ running
)

type reqKind int

const (
	reqSpawn reqKind = iota
	reqAdopt
	reqRestart
	reqStop
)

type request struct{ kind reqKind }

type running struct{}
