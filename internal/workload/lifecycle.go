package workload

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
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

	mu          sync.Mutex
	parent      context.Context
	cmd         *exec.Cmd
	pid         int
	child       childproof.Child
	pidState    string
	adopted     bool
	ready       bool
	stopping    bool
	restarts    int
	backoffTill time.Time
	cancel      context.CancelFunc
	watchDone   chan struct{}
}

// NewProcess prepares a Process for a driver's Config.
func NewProcess(cfg Config) *Process {
	return &Process{cfg: cfg, pidState: PidNone}
}

// State snapshots the Process.
func (p *Process) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stateLocked()
}

func (p *Process) stateLocked() State {
	return State{
		Pid: p.pid, Child: p.child, PidState: p.pidState, Adopted: p.adopted,
		Ready: p.ready, Stopping: p.stopping, Restarts: p.restarts, BackoffTill: p.backoffTill,
	}
}

// Start adopts a child a previous daemon left at the pidfile, or starts a
// fresh one, then waits for the listener and marks it ready. A port answered
// by a process the pidfile does not prove ours refuses, and nothing is
// signalled: marvel never adopts a stranger.
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parent = ctx
	ctx, p.cancel = context.WithCancel(ctx)

	v := p.cfg.Classify()
	p.pidState = v.Kind
	switch v.Kind {
	case PidProven:
		if p.cfg.Listener(time.Second) {
			p.pid, p.adopted, p.child = v.Pid, true, v.Child
			log.Printf("%s: adopted running %s pid %d on %s", p.cfg.Name, p.cfg.Label, v.Pid, p.cfg.Listen)
			// The child still holds what the previous daemon gave it; this
			// daemon has just rendered fresh values. Reload before anything
			// connects. Connected clients are kept.
			if p.cfg.Reload != 0 {
				if err := p.cfg.Prober.Signal(v.Child, p.cfg.Reload, false); err != nil {
					log.Printf("%s: reload adopted %s pid %d: %v", p.cfg.Name, p.cfg.Label, v.Pid, err)
				}
			}
			return p.becomeReady(ctx, "adopted")
		}
		// Proven and not listening: it is ours and wedged.
		log.Printf("%s: pidfile names our %s pid %d, which is not listening on %s; stopping it", p.cfg.Name, p.cfg.Label, v.Pid, p.cfg.Listen)
		p.Terminate(v.Child)
		p.removeIdentity()
	case PidStale:
		p.removeIdentity()
		if p.cfg.OnStale != nil {
			p.cfg.OnStale(v)
		}
		if p.cfg.Listener(500 * time.Millisecond) {
			return p.cfg.Unproven(v.Pid, "the pidfile does not prove it is ours")
		}
	case PidLegacy:
		if done, err := p.startLegacy(v); done {
			return err
		}
	}
	if p.cfg.Listener(500 * time.Millisecond) {
		return p.cfg.Stranger()
	}
	if err := p.spawnLocked(); err != nil {
		return err
	}
	return p.becomeReady(ctx, "started")
}

func (p *Process) removeIdentity() {
	if p.cfg.RemoveIdentity != nil {
		p.cfg.RemoveIdentity()
	}
}

// startLegacy handles a pidfile with no identity record: the first start after
// the upgrade that writes one. With the listener silent it is a stale file.
// With the listener answering, the child is unproven and nothing is done about
// it. done is true when Start is over. Caller holds p.mu.
func (p *Process) startLegacy(v Verdict) (done bool, err error) {
	if !p.cfg.Listener(time.Second) {
		log.Printf("%s: pidfile names pid %d with no identity record and nothing is listening on %s; removing it", p.cfg.Name, v.Pid, p.cfg.Listen)
		if p.cfg.RemovePidFile != nil {
			p.cfg.RemovePidFile()
		}
		return false, nil
	}
	return true, p.cfg.Unproven(v.Pid, "its pidfile has no identity record")
}

// becomeReady waits for the listener, runs AfterReady, marks ready, hands the
// driver its moment, and starts the watch loop. Caller holds p.mu; it is
// released around OnReady, so a slow driver read cannot hold State.
func (p *Process) becomeReady(ctx context.Context, how string) error {
	if !p.waitListener(ctx) {
		return fmt.Errorf("%s: %s pid %d did not answer on %s within %s (see %s)", p.cfg.Name, p.cfg.Label, p.pid, p.cfg.Listen, p.cfg.DialTimeout, p.cfg.LogPath)
	}
	if p.cfg.AfterReady != nil {
		if err := p.cfg.AfterReady(); err != nil {
			return fmt.Errorf("%s: listener up but provisioning failed: %w", p.cfg.Name, err)
		}
	}
	p.ready = true
	if p.cfg.OnReady != nil {
		p.mu.Unlock()
		p.cfg.OnReady(ctx, how)
		p.mu.Lock()
	}
	// Hand the child to the watcher here, under the lock the caller holds,
	// rather than letting it read p.cmd later: a Stop that lands first would
	// have zeroed the handle and the child would never be reaped.
	p.watchDone = make(chan struct{})
	go p.watch(ctx, p.watchDone, p.cmd, p.child)
	return nil
}

// spawnLocked starts the child through Start, the one spawn path: its
// environment is the class allowlist plus what the driver minted, never the
// daemon's own. Caller holds p.mu.
func (p *Process) spawnLocked() error {
	spec, err := p.cfg.Spec()
	if err != nil {
		return err
	}
	// The identity record is written before the pidfile that names it, from
	// what the kernel says about the child now. The child is proven from the
	// handle that started it, never from a pid.
	var spawned childproof.Child
	spec.PidFile = p.cfg.PidFile
	spec.BeforePidFile = func(cmd *exec.Cmd) error {
		if p.cfg.OnSpawnPid != nil {
			p.cfg.OnSpawnPid(cmd.Process.Pid)
		}
		c, err := p.cfg.Prober.Spawned(cmd)
		if err != nil {
			return fmt.Errorf("read the identity of the new %s (pid %d): %w", p.cfg.Label, cmd.Process.Pid, err)
		}
		spawned = c
		if p.cfg.RecordIdentity != nil {
			return p.cfg.RecordIdentity(c)
		}
		return nil
	}
	child, err := Start(spec)
	if err != nil {
		return err
	}
	if p.cfg.Spawned != nil {
		p.cfg.Spawned(child)
	}
	p.cmd, p.pid, p.adopted, p.child, p.pidState = child.Cmd, child.Pid, false, spawned, PidProven
	return nil
}

// watch follows the child: an exit nobody asked for is a crash and a restart
// under backoff. The driver's observers run on the same loop, on the
// PollEvery cadence. Sessions are never restarted for either.
func (p *Process) watch(ctx context.Context, done chan struct{}, cmd *exec.Cmd, c childproof.Child) {
	defer close(done)
	exit := make(chan error, 1)
	go func() {
		if cmd != nil {
			exit <- cmd.Wait()
			return
		}
		// Adopted: not our child, so poll its identity.
		every := p.cfg.AdoptPoll
		if every <= 0 {
			every = 2 * time.Second
		}
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				exit <- ctx.Err()
				return
			case <-t.C:
				// Same is an identity read: a pid handed to another process
				// reads as the child having exited.
				if !p.cfg.Prober.Same(c) {
					exit <- fmt.Errorf("adopted %s exited", p.cfg.Label)
					return
				}
			}
		}
	}()
	var tick <-chan time.Time
	if p.cfg.PollEvery > 0 {
		t := time.NewTicker(p.cfg.PollEvery)
		defer t.Stop()
		tick = t.C
	}
	if p.cfg.PollFirst != nil {
		p.cfg.PollFirst()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			if p.cfg.Poll != nil {
				p.cfg.Poll(ctx)
			}
		case err := <-exit:
			p.mu.Lock()
			stopping := p.stopping
			p.ready = false
			p.mu.Unlock()
			if stopping || ctx.Err() != nil {
				return
			}
			p.onCrash(ctx, err)
			return
		}
	}
}

// onCrash records the exit, waits out the backoff, and restarts.
func (p *Process) onCrash(ctx context.Context, err error) {
	p.mu.Lock()
	p.restarts++
	wait := p.cfg.Backoff(p.restarts)
	p.backoffTill = time.Now().UTC().Add(wait)
	pid, restarts := p.pid, p.restarts
	p.mu.Unlock()
	if p.cfg.OnCrash != nil {
		p.cfg.OnCrash(pid, err, restarts, wait)
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping {
		return
	}
	if serr := p.spawnLocked(); serr != nil {
		log.Printf("%s: restart failed: %v", p.cfg.Name, serr)
		go func() { p.onCrash(ctx, serr) }()
		return
	}
	if rerr := p.becomeReady(ctx, fmt.Sprintf("restarted (#%d)", p.restarts)); rerr != nil {
		log.Printf("%s: %v", p.cfg.Name, rerr)
	}
}

// Restart ends the running child and starts a fresh one from the current
// configuration and environment. Clients reconnect; the restart is counted
// separately from crashes and is not subject to backoff. Safe while the child
// is down: it becomes the start.
func (p *Process) Restart(reason string) error {
	p.mu.Lock()
	if p.parent == nil {
		p.mu.Unlock()
		return fmt.Errorf("%s: restart before start", p.cfg.Name)
	}
	p.stopping = true // the watcher must not read this exit as a crash
	if p.cancel != nil {
		p.cancel()
	}
	pid, child, done := p.pid, p.child, p.watchDone
	p.ready, p.pid, p.cmd, p.child, p.watchDone = false, 0, nil, childproof.Child{}, nil
	p.mu.Unlock()
	if done != nil {
		<-done
	}
	if pid != 0 {
		p.Terminate(child)
		log.Printf("%s: %s pid %d stopped to restart (%s)", p.cfg.Name, p.cfg.Label, pid, reason)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopping = false
	var ctx context.Context
	ctx, p.cancel = context.WithCancel(p.parent)
	if err := p.spawnLocked(); err != nil {
		return err
	}
	return p.becomeReady(ctx, "restarted ("+reason+")")
}

// Terminate ends a proven child: TERM to its group, a grace period polled by
// identity so a pid handed to another process counts as exited, then KILL.
// Every signal re-proves the child first (Prober.Signal), so a change at any
// point stops the rest. A child that is gone or no longer the one proven is
// left alone.
func (p *Process) Terminate(c childproof.Child) {
	if err := p.cfg.Prober.Signal(c, syscall.SIGTERM, true); err != nil {
		log.Printf("%s: not stopping %s pid %d: %v", p.cfg.Name, p.cfg.Label, c.Pid(), err)
		return
	}
	grace := p.cfg.Grace
	if grace <= 0 {
		grace = DefaultGrace
	}
	step := min(100*time.Millisecond, grace/4)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && p.cfg.Prober.Same(c) {
		time.Sleep(step)
	}
	if p.cfg.Prober.Same(c) {
		if err := p.cfg.Prober.Signal(c, syscall.SIGKILL, true); err != nil {
			log.Printf("%s: not killing %s pid %d: %v", p.cfg.Name, p.cfg.Label, c.Pid(), err)
		}
	}
}

// Signal sends sig to the running child, re-proving it first. running is
// false, and nothing is sent, when no child is up and ready.
func (p *Process) Signal(sig syscall.Signal) (pid int, running bool, err error) {
	p.mu.Lock()
	pid, child, ready := p.pid, p.child, p.ready
	p.mu.Unlock()
	if pid == 0 || !ready {
		return pid, false, nil
	}
	return pid, true, p.cfg.Prober.Signal(child, sig, false)
}

// Stop ends supervision. With keep the child is left running for the next
// daemon to adopt; otherwise the child's lifetime is the daemon's and it is
// terminated. The pidfile stays when kept, since it is how the successor
// adopts.
func (p *Process) Stop(keep bool) {
	p.mu.Lock()
	p.stopping = true
	if p.cancel != nil {
		p.cancel()
	}
	pid, child, done := p.pid, p.child, p.watchDone
	p.ready = false
	p.pid, p.cmd, p.child, p.watchDone = 0, nil, childproof.Child{}, nil // a second Stop is a no-op
	p.mu.Unlock()
	if done != nil {
		<-done
	}
	if pid == 0 {
		return
	}
	if keep {
		log.Printf("%s: leaving %s pid %d running for the next daemon to adopt", p.cfg.Name, p.cfg.Label, pid)
		return
	}
	// Our own child was already reaped by cmd.Wait inside watch; an adopted
	// process is not ours to wait on, so both are given the grace by polling.
	p.Terminate(child)
	p.removeIdentity()
	if p.cfg.OnStopped != nil {
		p.cfg.OnStopped(pid)
	}
}

func (p *Process) waitListener(ctx context.Context) bool {
	deadline := time.Now().Add(p.cfg.DialTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		if p.cfg.Listener(300 * time.Millisecond) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}
