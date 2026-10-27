package bus

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/workload"
)

// The bus is the first driver of workload.Process (docs/design/code-restructure-plan.md,
// Move 2). The supervisor decides how a broker enters (a fresh spawn, or an
// adopt of one the pidfile proves) and Process runs it from there; busDriver
// is what Process calls back. Every method here runs on the Process loop
// goroutine, one at a time, with no Process lock held.

// waitFunc adapts a func(n int) time.Duration, the shape of the supervisor's
// backoff field, to workload.Schedule.
type waitFunc func(n int) time.Duration

func (f waitFunc) Wait(n int) time.Duration { return f(n) }

// busDriver is the Supervisor as a workload.Driver. Supervisor has its own
// Ready() bool, which the daemon calls, so the Driver methods sit on this
// type.
type busDriver struct{ s *Supervisor }

var _ workload.Driver = busDriver{}

// newProcess builds the Process for the broker from the supervisor's current
// fields: the binary, the conf, the pidfile and log paths, the grace and
// the schedule. The class contract names the environment it may inherit, and
// the leaf seed rides in MintedEnv; nothing of the daemon's own does
// (aae-orc-oo62t).
func (s *Supervisor) newProcess() *workload.Process {
	conf := s.mgr.ConfPath()
	spec := workload.ProcessSpec{
		Name:     "bus",
		Binary:   s.binary,
		Args:     []string{"-c", conf},
		EnvAllow: s.envAllow,
		MintedEnv: func() []string {
			if s.Env == nil {
				return nil
			}
			return s.Env()
		},
		// The conf is read at every spawn, so a restart sees what the manager
		// last rendered.
		Check: func(env []string) error {
			body, err := os.ReadFile(conf)
			if err != nil {
				return fmt.Errorf("bus: read %s: %w", conf, err)
			}
			if strings.Contains(string(body), "$"+LeafSeedEnv) && !hasEnv(env, LeafSeedEnv) {
				return fmt.Errorf("bus: %s references $%s but no %s credential is in the store; the broker would refuse to start", conf, LeafSeedEnv, "bus/leaf")
			}
			return nil
		},
		LogPath:     s.logPath,
		PidFile:     s.pidFile,
		RecordChild: s.recordIdentity,
		Grace:       s.grace,
		TickEvery:   s.leafPoll,
		AdoptPoll:   s.adoptPoll,
	}
	return workload.New(spec, busDriver{s}, s.prober, waitFunc(s.backoff))
}

// recordIdentity writes the identity record before the pidfile that names it,
// from what the kernel says about the child now (design section 3.1). Without
// an identity the child is stopped: a broker marvel cannot prove later is one
// it could never stop. Process proves the child from the handle that started
// it, never from a pid, and passes it here.
func (s *Supervisor) recordIdentity(cmd *exec.Cmd, c childproof.Child) error {
	if s.onSpawn != nil {
		s.onSpawn(cmd.Process.Pid)
	}
	err := os.MkdirAll(filepath.Dir(s.pidFile), 0o700)
	if err == nil {
		// The leaf seed's fingerprint is read from the environment this child
		// was given, never from the store (aae-orc-4h9xp).
		fp := ""
		if v, ok := envValue(cmd.Env, LeafSeedEnv); ok {
			fp = SeedFingerprint([]byte(v))
		}
		err = writeIdentityLeaf(sidecarPath(s.pidFile), c.Pid(), c.Identity(), s.now(), fp)
	}
	if err != nil {
		_ = os.Remove(sidecarPath(s.pidFile))
		s.emit(events.KindBusIdentityUnrecorded, events.SeverityWarning, fmt.Sprintf("nats-server pid %d started, but its identity record could not be written (%v); the next start reads its pidfile as legacy", c.Pid(), err))
	}
	return nil
}

// Ready waits for the listener and runs AfterReady (provisioning). A child
// this process spawned also records what it was given: whether the leaf seed
// was in its environment, and the seed's fingerprint, so a later reload or
// rotation is told apart (aae-orc-ct0l4).
func (d busDriver) Ready(ctx context.Context, c workload.ChildInfo) error {
	s := d.s
	s.mu.Lock()
	s.lastPid = c.Child.Pid()
	if c.Env != nil {
		s.leafSeedInEnv = hasEnv(c.Env, LeafSeedEnv)
		s.leafStateKnown = true
		s.leafSeedFP = ""
		if v, ok := envValue(c.Env, LeafSeedEnv); ok {
			s.leafSeedFP = SeedFingerprint([]byte(v))
		}
	}
	timeout := s.dialTimeout
	s.mu.Unlock()
	// Gone means the pid no longer exists. Identity is not the test: a child
	// that execs into another program (a shim in front of nats-server) is
	// still running. A zombie exists until Cmd.Wait reaps it, and a probe that
	// fails reads as alive, so both only delay the answer to the next poll.
	alive := func() bool {
		ok, err := s.prober.Exists(c.Child.Pid())
		return ok || err != nil
	}
	ok, gone := s.waitListener(ctx, timeout, alive)
	if gone {
		return fmt.Errorf("bus: nats-server pid %d exited before it answered on %s: %s (see %s)", c.Child.Pid(), s.mgr.bus.Listen, logTail(s.logPath, 3), s.logPath)
	}
	if !ok {
		return fmt.Errorf("bus: nats-server pid %d did not answer on %s within %s (see %s)", c.Child.Pid(), s.mgr.bus.Listen, timeout, s.logPath)
	}
	if s.AfterReady != nil {
		if err := s.AfterReady(); err != nil {
			return fmt.Errorf("bus: listener up but provisioning failed: %w", err)
		}
	}
	return nil
}

// Started runs once Process has marked the broker ready: the first structural
// reading, the start event, and the first leaf poll. The reading is cleared
// first and tagged with this child's generation, and readyGen is set when it
// is done, so Ready is false until the child has its own reading.
func (d busDriver) Started(ctx context.Context, si workload.StartInfo) {
	s := d.s
	pid := si.Child.Child.Pid()
	if si.How != workload.StartAdopted {
		log.Printf("bus: started nats-server %s pid %d (%s -c %s), log %s", s.version, pid, s.binary, s.mgr.ConfPath(), s.logPath)
	}
	// The broker now has the file it started or reloaded with: ask whether it
	// accepts the per-role users, off this path.
	s.mgr.confirmAsync()
	snap := s.snapshot()
	s.mu.Lock()
	s.structureKnown, s.structureGen = false, si.Child.Gen
	s.checkStructureLocked(ctx, snap)
	s.readyGen = si.Child.Gen
	how := "started"
	switch {
	case si.How == workload.StartAdopted:
		how = "adopted"
	case si.How == workload.StartRestarted && si.Reason != "":
		how = "restarted (" + si.Reason + ")"
	case si.How == workload.StartRestarted:
		how = fmt.Sprintf("restarted (#%d)", si.Restarts)
	}
	s.emit(events.KindBusStarted, events.SeverityInfo, fmt.Sprintf("nats-server %s: pid %d on %s, domain %s", how, pid, s.mgr.bus.Listen, s.mgr.Domain()))
	if s.mgr.bus.HubURL != "" && !s.mgr.leafEnrolled() {
		s.emit(events.KindBusLeafUnenrolled, events.SeverityWarning, fmt.Sprintf("hub %s is configured but no %s credential is in the store; running local-only until one is pushed", s.mgr.bus.HubURL, "bus/leaf"))
	}
	if si.How == workload.StartAdopted {
		s.reportLeafOnAdopt(pid)
	}
	s.mu.Unlock()
	s.pollLeaf()
}

// Tick is one /leafz poll and one structural reading, on the one ticker. Its
// context carries a deadline below the period, so a slow broker cannot hold
// the loop past its next tick.
func (d busDriver) Tick(ctx context.Context) {
	d.s.pollLeaf()
	d.s.checkStructure(ctx)
}

// Exited reports a crash. A requested exit (Stop or Restart) is the caller's:
// Stop emits bus.stopped, and Restart's start event carries the reason. The
// hold (bus.unavailable per role) is the controller's; ready drops with the
// Process state.
func (d busDriver) Exited(e workload.Exit) {
	if e.Requested {
		return
	}
	d.s.mu.Lock()
	pid := d.s.lastPid
	d.s.mu.Unlock()
	d.s.emit(events.KindBusCrashed, events.SeverityWarning, fmt.Sprintf("nats-server pid %d exited (%v); restart #%d in %s, sessions hold until it is back", pid, e.Err, e.Restarts, e.RetryIn))
}
