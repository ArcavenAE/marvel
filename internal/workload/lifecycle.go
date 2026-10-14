package workload

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// The values of Verdict.Kind: what a pidfile proved at start.
const (
	PidNone   = "none"
	PidProven = "proven"
	PidStale  = "stale"
	PidLegacy = "legacy"
)

// DefaultGrace is how long terminate waits between TERM and KILL.
const DefaultGrace = 5 * time.Second

// Verdict is what a pidfile proved at start. The driver reads the pidfile and
// whatever identity record it keeps beside it, and hands the answer back.
type Verdict struct {
	Pid    int
	Kind   string // PidNone, PidProven, PidStale or PidLegacy
	Reason string // for PidStale: dead, start, exe, argv, sidecar-pid or sidecar
	Child  childproof.Child
}

// Config is what a Process needs from its driver. A driver builds one with
// its own listener, identity record and events, and the Process supplies the
// supervision: adopt by pidfile, spawn, watch, restart under backoff, stop.
// Every hook is optional unless its comment says otherwise, and no hook may
// call back into the Process it was handed to, except where stated.
type Config struct {
	// Name labels the child in errors and the daemon log ("bus").
	Name string
	// Label is the binary's own name in messages ("nats-server").
	Label string
	// Listen is the address the child serves, for messages.
	Listen string
	// LogPath is named in the message for a child that never answered.
	LogPath string
	// PidFile is the child's pidfile; the Process removes only what the driver
	// says to (RemoveIdentity), and writes it through Spec.
	PidFile string
	// Prober proves a child's identity and is the only way a signal reaches
	// one. Required.
	Prober *childproof.Prober
	// Spec returns the spawn description for the next start, read fresh at
	// every spawn so a conf or a minted secret that changed is seen. Required.
	Spec func() (ProcessSpec, error)
	// Backoff maps a restart count to a wait. Required.
	Backoff func(n int) time.Duration
	// DialTimeout bounds the listener wait at start.
	DialTimeout time.Duration
	// AdoptPoll is how often the watcher re-reads an adopted child's identity.
	AdoptPoll time.Duration
	// Grace is how long terminate waits between TERM and KILL.
	Grace time.Duration
	// Reload is the signal that makes an adopted child re-read its
	// configuration before anything connects; 0 sends none.
	Reload syscall.Signal

	// Listener reports whether the child's listener answers within timeout.
	// Required.
	Listener func(timeout time.Duration) bool
	// Classify reads the pidfile and decides what it proves. Required.
	Classify func() Verdict
	// RemoveIdentity removes the pidfile and the driver's identity record.
	RemoveIdentity func()
	// RemovePidFile removes the pidfile alone (a legacy file with no record).
	RemovePidFile func()
	// Unproven reports a port held by a child the pidfile does not prove ours
	// and returns the refusal. Required.
	Unproven func(pid int, why string) error
	// Stranger is the refusal for a port answered by a process no pidfile of
	// ours claims. Required.
	Stranger func() error
	// OnStale reports a pidfile that names a process that is not the recorded
	// child; nothing was signalled.
	OnStale func(v Verdict)

	// OnSpawnPid sees the pid of every child this Process starts, before
	// anything is signalled.
	OnSpawnPid func(pid int)
	// RecordIdentity writes the identity record of a child just started,
	// before its pidfile. An error from the driver's own write is the driver's
	// to report; returning one here stops the child.
	RecordIdentity func(c childproof.Child) error
	// Spawned sees a child just started, under the Process lock.
	Spawned func(c *Child)

	// AfterReady runs once the listener answers and before the child is marked
	// ready. An error fails the start.
	AfterReady func() error
	// OnReady runs after the child is marked ready, with the Process lock
	// released, so the driver may read the child and call State. how is
	// "started", "adopted" or "restarted ...".
	OnReady func(ctx context.Context, how string)
	// OnCrash reports an exit nobody asked for, with the restart that follows.
	OnCrash func(pid int, err error, restarts int, wait time.Duration)
	// OnStopped reports a child stopped with the daemon.
	OnStopped func(pid int)

	// PollFirst runs once when a watch begins, and Poll every PollEvery after
	// that, on the watch goroutine. A driver's observers (a link poll, a
	// structure check) live here.
	PollFirst func()
	Poll      func(ctx context.Context)
	PollEvery time.Duration
}

// State is a snapshot of what a Process holds.
type State struct {
	Pid         int
	Child       childproof.Child // the proven child; zero when none runs
	PidState    string           // PidProven, PidStale, PidLegacy or PidNone
	Adopted     bool
	Ready       bool
	Stopping    bool
	Restarts    int
	BackoffTill time.Time
}

// Process supervises one managed child for a driver: it adopts a child a
// previous daemon left at the pidfile, spawns a fresh one through Start,
// restarts a crash under backoff, restarts on request, and stops with or
// without leaving the child for a successor.
type Process struct {
	cfg Config
}

// NewProcess prepares a Process for a driver's Config.
func NewProcess(cfg Config) *Process { return &Process{cfg: cfg} }

// State snapshots the Process.
func (p *Process) State() State { return State{PidState: PidNone} }

// Start adopts or spawns the child and waits for it to be ready.
func (p *Process) Start(ctx context.Context) error { return errors.New("not implemented") }

// Restart ends the running child and starts a fresh one.
func (p *Process) Restart(reason string) error { return errors.New("not implemented") }

// Terminate ends a proven child: TERM, a grace period, then KILL.
func (p *Process) Terminate(c childproof.Child) {}

// Signal sends sig to the running child, re-proving it first.
func (p *Process) Signal(sig syscall.Signal) (pid int, running bool, err error) {
	return 0, false, nil
}

// Stop ends supervision, leaving the child running when keep is set.
func (p *Process) Stop(keep bool) {}
