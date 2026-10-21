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

type reqKind int

const (
	reqSpawn reqKind = iota
	reqAdopt
	reqRestart
	reqStop
)

type request struct {
	kind   reqKind
	ctx    context.Context
	child  childproof.Child
	reason string
	keep   bool
	reply  chan error
}

// New returns a Process for spec, driven by d. p is the prober every signal
// goes through; sched is the restart schedule after a crash. Nothing runs
// until Spawn or Adopt.
func New(spec ProcessSpec, d Driver, p *childproof.Prober, sched backoff.Schedule) *Process {
	return &Process{
		spec:   spec,
		driver: d,
		prober: p,
		sched:  sched,
		now:    time.Now,
		after:  time.After,
		st:     State{PidState: PidStateNone},
		reqs:   make(chan request),
		done:   make(chan struct{}),
	}
}

// Snapshot returns a copy of the state, taken under one lock.
func (p *Process) Snapshot() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

// Spawn starts the child and blocks until the driver's Ready returns, and
// returns its error. On a Ready error the child keeps running, is watched and
// is not ready. The ctx of the first Spawn or Adopt is the parent of all the
// loop's work. When it ends the loop stops watching, ticking and respawning,
// and still serves Stop, so a daemon that cancels before it stops still ends
// its child.
func (p *Process) Spawn(ctx context.Context) error {
	return p.send(request{kind: reqSpawn, ctx: ctx})
}

// Adopt takes a child the driver has already proven, and blocks until the
// driver's Ready returns, as Spawn does.
func (p *Process) Adopt(ctx context.Context, c childproof.Child) error {
	return p.send(request{kind: reqAdopt, ctx: ctx, child: c})
}

// Restart ends the running child and starts a fresh one from the spec. It is
// not counted as a crash and is not subject to backoff. While the child is
// down, in the restart wait, it becomes the start. It is an error before
// Spawn or Adopt.
func (p *Process) Restart(reason string) error {
	return p.send(request{kind: reqRestart, reason: reason})
}

// Stop ends supervision. With keep the child is left running, with its
// pidfile, for a successor to adopt; otherwise it is terminated and Exited
// reports a requested exit. A second Stop, or one before any start, does
// nothing.
func (p *Process) Stop(keep bool) {
	if !p.looping.Load() {
		return
	}
	_ = p.send(request{kind: reqStop, keep: keep})
	<-p.done
}

// send hands r to the loop, starting the loop on the first Spawn or Adopt.
func (p *Process) send(r request) error {
	select {
	case <-p.done:
		return ErrStopped
	default:
	}
	switch {
	case r.kind == reqSpawn || r.kind == reqAdopt:
		if p.looping.CompareAndSwap(false, true) {
			parent := r.ctx
			if parent == nil {
				parent = context.Background()
			}
			go p.loop(parent)
		}
	case !p.looping.Load():
		return fmt.Errorf("%s: restart before start", p.spec.Name)
	}
	r.reply = make(chan error, 1)
	select {
	case p.reqs <- r:
	case <-p.done:
		return ErrStopped
	}
	select {
	case err := <-r.reply:
		return err
	case <-p.done:
		return ErrStopped
	}
}

// running is the child the loop watches.
type running struct {
	info   ChildInfo
	cmd    *exec.Cmd // nil for an adopted child
	exit   chan error
	cancel context.CancelFunc // ends an adopted child's identity poll
}

// loop is the one goroutine that runs the child: every request, the watch,
// the crash respawn and the ticker.
func (p *Process) loop(parent context.Context) {
	defer close(p.done)
	var (
		cur    *running
		retry  <-chan time.Time // the restart wait after a crash
		ticker *time.Ticker
		tick   <-chan time.Time
		gen    uint64
		// quiet is set once the parent has ended: from then on the loop only
		// serves requests.
		quiet      bool
		parentDone = parent.Done()
	)
	if p.spec.TickEvery > 0 {
		ticker = time.NewTicker(p.spec.TickEvery)
		defer ticker.Stop()
		tick = ticker.C
	}
	exitOf := func() <-chan error {
		if cur == nil || quiet {
			return nil
		}
		return cur.exit
	}

	// begin marks c ready after the driver's Ready, then reports Started.
	begin := func(c *running, s StartInfo) error {
		cur = c
		p.mu.Lock()
		p.st.Pid, p.st.Child, p.st.Gen = c.info.Child.Pid(), c.info.Child, c.info.Gen
		p.st.Adopted = s.How == Adopted
		p.st.Ready = false
		p.st.PidState = PidStateProven
		p.st.BackoffUntil = time.Time{}
		s.Restarts = p.st.Restarts
		p.mu.Unlock()
		if err := p.driver.Ready(parent, c.info); err != nil {
			return err
		}
		p.mu.Lock()
		p.st.Ready = true
		p.mu.Unlock()
		p.driver.Started(parent, s)
		return nil
	}

	spawn := func() (*running, error) {
		c, child, err := p.spawn()
		if err != nil {
			return nil, err
		}
		gen++
		r := &running{info: ChildInfo{Child: child, Env: c.Env, Gen: gen}, cmd: c.Cmd, exit: make(chan error, 1)}
		go func() { r.exit <- c.Cmd.Wait() }()
		return r, nil
	}

	adopt := func(child childproof.Child) *running {
		gen++
		ctx, cancel := context.WithCancel(parent)
		r := &running{info: ChildInfo{Child: child, Gen: gen}, exit: make(chan error, 1), cancel: cancel}
		go p.pollAdopted(ctx, child, r.exit)
		return r
	}

	// end stops watching the current child and, unless keep, terminates it.
	end := func(keep bool) {
		if cur == nil {
			return
		}
		if cur.cancel != nil {
			cur.cancel()
		}
		if !keep {
			if err := Terminate(p.prober, cur.info.Child, p.spec.Grace); err != nil {
				log.Printf("%s: %v", p.spec.Name, err)
			}
		}
		cur = nil
		p.mu.Lock()
		p.st.Ready, p.st.Pid, p.st.Child = false, 0, childproof.Child{}
		restarts := p.st.Restarts
		p.mu.Unlock()
		if !keep {
			p.driver.Exited(Exit{Requested: true, Restarts: restarts})
		}
	}

	// crashed records an exit nobody asked for and starts the restart wait.
	crashed := func(err error) {
		cur = nil
		p.mu.Lock()
		p.st.Ready, p.st.Pid, p.st.Child = false, 0, childproof.Child{}
		p.st.Restarts++
		n := p.st.Restarts
		wait := p.sched.Wait(n)
		p.st.BackoffUntil = p.now().UTC().Add(wait)
		p.mu.Unlock()
		p.driver.Exited(Exit{Err: err, Restarts: n, RetryIn: wait})
		retry = p.after(wait)
	}

	for {
		select {
		case <-parentDone:
			quiet, parentDone, retry, tick = true, nil, nil, nil

		case r := <-p.reqs:
			switch r.kind {
			case reqSpawn, reqAdopt:
				if cur != nil {
					r.reply <- fmt.Errorf("%s: a child is already running (pid %d)", p.spec.Name, cur.info.Child.Pid())
					continue
				}
				// A start during the wait after a crash replaces the respawn.
				retry = nil
				var c *running
				how := Spawned
				if r.kind == reqAdopt {
					c, how = adopt(r.child), Adopted
				} else {
					var err error
					if c, err = spawn(); err != nil {
						r.reply <- err
						continue
					}
				}
				r.reply <- begin(c, StartInfo{How: how, Child: c.info})
			case reqRestart:
				retry = nil
				end(false)
				c, err := spawn()
				if err != nil {
					r.reply <- err
					continue
				}
				r.reply <- begin(c, StartInfo{How: Restarted, Reason: r.reason, Child: c.info})
			case reqStop:
				retry = nil
				end(r.keep)
				r.reply <- nil
				return
			}

		case err := <-exitOf():
			crashed(err)

		case <-retry:
			retry = nil
			if cur != nil {
				continue // a request already started a child
			}
			c, err := spawn()
			if err != nil {
				log.Printf("%s: restart failed: %v", p.spec.Name, err)
				crashed(err)
				continue
			}
			if rerr := begin(c, StartInfo{How: Restarted, Child: c.info}); rerr != nil {
				log.Printf("%s: %v", p.spec.Name, rerr)
			}

		case <-tick:
			if cur == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(parent, p.spec.TickEvery*9/10)
			p.driver.Tick(ctx)
			cancel()
		}
	}
}

// spawn starts the child through Start and proves it from the handle before
// the pidfile is written, so the identity a driver records in BeforePidFile is
// the one Process holds.
func (p *Process) spawn() (*Child, childproof.Child, error) {
	spec := p.spec
	var proven childproof.Child
	before := spec.BeforePidFile
	spec.BeforePidFile = func(cmd *exec.Cmd) error {
		c, err := p.prober.Spawned(cmd)
		if err != nil {
			return fmt.Errorf("read the identity of the new child (pid %d): %w", cmd.Process.Pid, err)
		}
		proven = c
		if before != nil {
			return before(cmd)
		}
		return nil
	}
	c, err := Start(spec)
	if err != nil {
		return nil, childproof.Child{}, err
	}
	return c, proven, nil
}

// pollAdopted reports an adopted child's exit: it is not this process's
// child, so it cannot be waited on, and a pid handed to another process reads
// as the child having exited.
func (p *Process) pollAdopted(ctx context.Context, c childproof.Child, exit chan<- error) {
	every := p.spec.AdoptPoll
	if every <= 0 {
		every = defaultAdoptPoll
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !p.prober.Same(c) {
				exit <- fmt.Errorf("adopted %s exited", p.spec.Name)
				return
			}
		}
	}
}

// Terminate ends a proven child: SIGTERM to its group, a grace period polled
// by identity so a pid handed to another process counts as exited, then
// SIGKILL. Every signal re-proves the child first (Prober.Signal), so a change
// at any point stops the rest, and a child that is gone or no longer the one
// proven is left alone. grace <= 0 means DefaultGrace. It is stateless: a
// driver ending a child before any child reaches Process uses it too.
func Terminate(p *childproof.Prober, c childproof.Child, grace time.Duration) error {
	if err := p.Signal(c, syscall.SIGTERM, true); err != nil {
		return fmt.Errorf("not stopping pid %d: %w", c.Pid(), err)
	}
	if grace <= 0 {
		grace = DefaultGrace
	}
	step := min(100*time.Millisecond, grace/4)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && p.Same(c) {
		time.Sleep(step)
	}
	if p.Same(c) {
		if err := p.Signal(c, syscall.SIGKILL, true); err != nil {
			return fmt.Errorf("not killing pid %d: %w", c.Pid(), err)
		}
	}
	return nil
}
