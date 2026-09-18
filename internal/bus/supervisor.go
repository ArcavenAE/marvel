package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// Supervisor runs a managed broker as a child of the daemon (brief 10
// sections 2 and 6, aae-orc-xy1dh). It is not a Role, has no pane, and is
// not a Store resource: it predates every team, it must outlive the tmux
// server, and its health is listener-up, not pane liveness. It adopts a
// broker a previous daemon left running (reexec), restarts a crashed one
// under the crash-loop backoff schedule internal/team uses for roles, and
// reports the hub leaf link as an observable that never restarts anything.
type Supervisor struct {
	mgr     *Manager
	binary  string
	pidFile string
	logPath string
	ring    *events.Ring

	// Env supplies extra environment for the child, beyond the daemon's own,
	// read fresh at every spawn. The leaf seed rides here as DIRECTOR_LEAF_NKEY
	// (aae-orc-vqq2c): daemon memory to broker environment, nothing else. A
	// conf whose leaf block needs the variable while Env yields none is
	// refused at spawn rather than handed to a broker that cannot resolve it.
	Env func() []string
	// AfterReady runs once the listener answers and before the bus is marked
	// ready: provisioning (aae-orc-apeoc). Nil means ready at listener-up.
	// It is also what a structural miss re-runs, so it must stay
	// idempotent.
	AfterReady func() error
	// leafPoll is also the structural-check cadence; one ticker (section
	// 3.2). checkStructure reads the broker through this, so a test can
	// point it at a reader that fails on demand.
	checkStructureFn func(ctx context.Context) (Structure, error)
	// backoff maps a restart count to a wait; tests shorten it.
	backoff func(n int) time.Duration
	// dialTimeout bounds the listener wait at start.
	dialTimeout time.Duration
	// leafPoll is the /leafz cadence (R-56, 30s).
	leafPoll time.Duration

	mu          sync.Mutex
	parent      context.Context
	cmd         *exec.Cmd
	pid         int
	version     string // what `nats-server --version` on the resolved binary reports
	adopted     bool
	ready       bool
	stopping    bool
	restarts    int
	backoffTill time.Time
	leafUp      *bool
	// leafSeedInEnv records whether the running broker was spawned with the
	// leaf seed in its environment. nats-server reads its environment once at
	// start, so this is what decides whether a newly enrolled leaf can come up
	// on a reload (seed already present) or needs a fresh process to read it
	// (booted unenrolled); see the daemon enrollment path (aae-orc-ct0l4).
	leafSeedInEnv bool
	cancel        context.CancelFunc
	watchDone     chan struct{}

	// Structural health (aae-orc-vy6k7). structure is the last definite
	// reading; structureKnown says one exists; problems is the last
	// reading's Problems string, the once-per-change key for
	// bus.unprovisioned; authReloaded says the one SIGHUP an authorized
	// miss gets has been sent.
	structure      Structure
	structureKnown bool
	problems       string
	authReloaded   bool
}

// Status is what `marvel bus status` prints.
type Status struct {
	Managed bool `json:"managed"`
	// The Services record's common fields (docs/design/services-list.md).
	Class          string `json:"class,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Mode           string `json:"mode,omitempty"`
	CallerIdentity string `json:"caller_identity,omitempty"`
	// Version is what the resolved nats-server binary reports, so a host
	// whose PATH disagrees with the mise pin is visible here rather than
	// discovered at an upgrade.
	Version string `json:"version,omitempty"`
	// Seat is "<workspace>/<team>" when a director seat is declared; the
	// user is always director and its password sits at SeatPassFile.
	Seat         string `json:"seat,omitempty"`
	SeatPassFile string `json:"seat_pass_file,omitempty"`
	Listen       string `json:"listen,omitempty"`
	URL          string `json:"url,omitempty"`
	Domain       string `json:"domain,omitempty"`
	ConfPath     string `json:"conf_path,omitempty"`
	PID          int    `json:"pid,omitempty"`
	Adopted      bool   `json:"adopted"`
	Ready        bool   `json:"ready"`
	// Leaf is "up", "down", "detached" (enrolled but the operator disconnected
	// it), "unenrolled" (hub with no seed), "n/a" (no hub), or "unknown" (not
	// polled yet).
	Leaf string `json:"leaf"`
	// Structure is the structural-health reading on a managed broker
	// (provisioned, authorized, tls, what is missing); nil before the first
	// reading and on an adopted or external bus, where marvel holds no
	// admin credential and does not guess.
	Structure *StructureStatus `json:"structure,omitempty"`
	Restarts  int              `json:"restarts"`
	// BackoffUntil is set while a crashed broker waits to restart.
	BackoffUntil string `json:"backoff_until,omitempty"`
}

// StructureStatus is Structure as marvel bus status prints it.
type StructureStatus struct {
	Provisioned bool     `json:"provisioned"`
	Authorized  bool     `json:"authorized"`
	TLS         bool     `json:"tls"`
	Missing     []string `json:"missing,omitempty"`
}

// These mirror internal/team's crash-loop schedule for roles, so a broker
// and a session that crash-loop read the same way to an operator.
const (
	restartBackoffInitial = 30 * time.Second
	restartBackoffMax     = 5 * time.Minute
	defaultDialTimeout    = 10 * time.Second
	defaultLeafPoll       = 30 * time.Second
	stopGrace             = 5 * time.Second
)

func computeBackoff(n int) time.Duration {
	if n <= 1 {
		return restartBackoffInitial
	}
	d := restartBackoffInitial << (n - 1)
	if d <= 0 || d > restartBackoffMax {
		return restartBackoffMax
	}
	return d
}

// NewSupervisor resolves the nats-server binary and prepares a supervisor
// for the manager's broker. An absent binary is an error the daemon should
// refuse to start over, naming the runtime dependency, the tmux posture.
func NewSupervisor(mgr *Manager, runDir, logDir string, ring *events.Ring) (*Supervisor, error) {
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		return nil, fmt.Errorf("nats-server not found on PATH; cluster %s declares a managed bus, install it (mise or brew) or set bus.managed: false: %w", mgr.Domain(), err)
	}
	return &Supervisor{
		mgr:         mgr,
		binary:      bin,
		version:     binaryVersion(bin),
		pidFile:     filepath.Join(runDir, "nats-server.pid"),
		logPath:     filepath.Join(logDir, "nats-server.log"),
		ring:        ring,
		backoff:     computeBackoff,
		dialTimeout: defaultDialTimeout,
		leafPoll:    defaultLeafPoll,
	}, nil
}

// Start adopts a broker a previous daemon left at the pidfile, or starts a
// fresh one, then waits for the listener and marks the bus ready. A port
// answered by a process marvel did not record refuses loudly, the same
// posture as the daemon pidfile guard: marvel never adopts a stranger.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parent = ctx
	ctx, s.cancel = context.WithCancel(ctx)

	if pid, alive := s.pidFileAlive(); alive {
		if s.listenerAnswers(time.Second) {
			s.pid, s.adopted = pid, true
			log.Printf("bus: adopted running nats-server pid %d on %s", pid, s.mgr.bus.Listen)
			// The broker still holds the passwords the previous daemon minted;
			// this daemon has just rendered fresh ones. Reload before anything
			// connects, so the admin identity and the next session's credential
			// are the ones the broker knows. Connected clients are kept.
			if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
				log.Printf("bus: reload adopted nats-server pid %d: %v", pid, err)
			}
			return s.becomeReady(ctx, "adopted")
		}
		// Recorded pid is alive but not listening: it is ours and wedged.
		log.Printf("bus: pidfile names live pid %d that is not listening on %s; stopping it", pid, s.mgr.bus.Listen)
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		time.Sleep(time.Second)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	if s.listenerAnswers(500 * time.Millisecond) {
		return fmt.Errorf("bus: %s is already answering and no marvel pidfile claims it; refusing to adopt a stranger (stop it, or set bus.managed: false and bus.url to adopt it explicitly)", s.mgr.bus.Listen)
	}
	if err := s.spawnLocked(); err != nil {
		return err
	}
	return s.becomeReady(ctx, "started")
}

// becomeReady waits for the listener, runs AfterReady, marks ready, and
// starts the watch loop. Caller holds s.mu.
func (s *Supervisor) becomeReady(ctx context.Context, how string) error {
	if !s.waitListener(ctx) {
		return fmt.Errorf("bus: nats-server pid %d did not answer on %s within %s (see %s)", s.pid, s.mgr.bus.Listen, s.dialTimeout, s.logPath)
	}
	if s.AfterReady != nil {
		if err := s.AfterReady(); err != nil {
			return fmt.Errorf("bus: listener up but provisioning failed: %w", err)
		}
	}
	s.ready = true
	// First structural reading, under the lock the caller holds, so ready
	// means listener, pid, provisioned, and authorized from the first
	// moment anything asks.
	s.checkStructureLocked(ctx)
	s.emit(events.KindBusStarted, events.SeverityInfo, fmt.Sprintf("nats-server %s: pid %d on %s, domain %s", how, s.pid, s.mgr.bus.Listen, s.mgr.Domain()))
	if s.mgr.bus.HubURL != "" && !s.mgr.leafEnrolled() {
		s.emit(events.KindBusLeafUnenrolled, events.SeverityWarning, fmt.Sprintf("hub %s is configured but no %s credential is in the store; running local-only until one is pushed", s.mgr.bus.HubURL, "bus/leaf"))
	}
	// Hand the child to the watcher here, under the lock the caller holds,
	// rather than letting it read s.cmd later: a Stop that lands first would
	// have zeroed the handle and the child would never be reaped.
	s.watchDone = make(chan struct{})
	go s.watch(ctx, s.watchDone, s.cmd, s.pid)
	return nil
}

// spawnLocked starts the child in its own process group with its output in
// the broker log. Caller holds s.mu.
func (s *Supervisor) spawnLocked() error {
	conf := s.mgr.ConfPath()
	body, err := os.ReadFile(conf)
	if err != nil {
		return fmt.Errorf("bus: read %s: %w", conf, err)
	}
	env := os.Environ()
	if s.Env != nil {
		env = append(env, s.Env()...)
	}
	if strings.Contains(string(body), "$"+LeafSeedEnv) && !hasEnv(env, LeafSeedEnv) {
		return fmt.Errorf("bus: %s references $%s but no %s credential is in the store; the broker would refuse to start", conf, LeafSeedEnv, "bus/leaf")
	}
	// Record whether this process can resolve a leaf remote's seed on a later
	// reload. A broker spawned with the seed already present picks up a
	// connect on SIGHUP; one spawned without it needs a fresh process when a
	// seed is enrolled (aae-orc-ct0l4).
	s.leafSeedInEnv = hasEnv(env, LeafSeedEnv)
	if err := os.MkdirAll(filepath.Dir(s.logPath), 0o700); err != nil {
		return fmt.Errorf("bus: create log dir: %w", err)
	}
	logf, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("bus: open %s: %w", s.logPath, err)
	}
	cmd := exec.Command(s.binary, "-c", conf)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return fmt.Errorf("bus: start nats-server: %w", err)
	}
	_ = logf.Close() // the child holds its own descriptor
	s.cmd, s.pid, s.adopted = cmd, cmd.Process.Pid, false
	if err := os.MkdirAll(filepath.Dir(s.pidFile), 0o700); err == nil {
		_ = os.WriteFile(s.pidFile, []byte(strconv.Itoa(s.pid)+"\n"), 0o644)
	}
	log.Printf("bus: started nats-server %s pid %d (%s -c %s), log %s", s.version, s.pid, s.binary, conf, s.logPath)
	return nil
}

// binaryVersion runs `<bin> --version` and returns the version token
// ("v2.14.6" from "nats-server: v2.14.6"), or "unknown" when the binary
// does not answer. Read once at construction; the pin lives in mise.toml
// and this is how a disagreeing PATH shows up in marvel bus status.
func binaryVersion(bin string) string {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "unknown"
	}
	return fields[len(fields)-1]
}

func hasEnv(env []string, key string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return true
		}
	}
	return false
}

// watch follows the child: a pid exit marvel did not ask for is
// bus.crashed and a restart under backoff; the hub leaf link is polled
// on the 30s cadence and reported on transition. Sessions are never
// restarted for either: their shims reconnect.
func (s *Supervisor) watch(ctx context.Context, done chan struct{}, cmd *exec.Cmd, pid int) {
	defer close(done)
	exit := make(chan error, 1)
	go func() {
		if cmd != nil {
			exit <- cmd.Wait()
			return
		}
		// Adopted: not our child, so poll the pid.
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				exit <- ctx.Err()
				return
			case <-t.C:
				if syscall.Kill(pid, 0) != nil {
					exit <- errors.New("adopted nats-server exited")
					return
				}
			}
		}
	}()
	leaf := time.NewTicker(s.leafPoll)
	defer leaf.Stop()
	s.pollLeaf()
	for {
		select {
		case <-ctx.Done():
			return
		case <-leaf.C:
			s.pollLeaf()
			s.checkStructure(ctx)
		case err := <-exit:
			s.mu.Lock()
			stopping := s.stopping
			s.ready = false
			s.mu.Unlock()
			if stopping || ctx.Err() != nil {
				return
			}
			s.onCrash(ctx, err)
			return
		}
	}
}

// onCrash records the exit, waits out the backoff, and restarts. The hold
// (bus.unavailable per role) is the controller's; this only flips ready.
func (s *Supervisor) onCrash(ctx context.Context, err error) {
	s.mu.Lock()
	s.restarts++
	wait := s.backoff(s.restarts)
	s.backoffTill = time.Now().UTC().Add(wait)
	pid := s.pid
	s.mu.Unlock()
	s.emit(events.KindBusCrashed, events.SeverityWarning, fmt.Sprintf("nats-server pid %d exited (%v); restart #%d in %s, sessions hold until it is back", pid, err, s.restarts, wait))
	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return
	}
	if serr := s.spawnLocked(); serr != nil {
		log.Printf("bus: restart failed: %v", serr)
		go func() { s.onCrash(ctx, serr) }()
		return
	}
	if rerr := s.becomeReady(ctx, fmt.Sprintf("restarted (#%d)", s.restarts)); rerr != nil {
		log.Printf("bus: %v", rerr)
	}
}

// Restart ends the running broker and starts a fresh one from the current
// conf and environment. Reload cannot do this: the leaf seed rides in the
// child's environment, which the server reads once at start, so a later
// `credential put bus/leaf` (aae-orc-vqq2c) needs a new process. Clients
// reconnect; the restart is counted separately from crashes and is not
// subject to backoff. Safe while the broker is down: it becomes the start.
func (s *Supervisor) Restart(reason string) error {
	s.mu.Lock()
	if s.parent == nil {
		s.mu.Unlock()
		return errors.New("bus: restart before start")
	}
	s.stopping = true // the watcher must not read this exit as a crash
	if s.cancel != nil {
		s.cancel()
	}
	pid, done := s.pid, s.watchDone
	s.ready, s.pid, s.cmd, s.watchDone = false, 0, nil, nil
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	if pid != 0 {
		s.terminate(pid)
		log.Printf("bus: nats-server pid %d stopped to restart (%s)", pid, reason)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopping = false
	var ctx context.Context
	ctx, s.cancel = context.WithCancel(s.parent)
	if err := s.spawnLocked(); err != nil {
		return err
	}
	return s.becomeReady(ctx, "restarted ("+reason+")")
}

// terminate ends a broker process group with a bounded grace period.
func (s *Supervisor) terminate(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(stopGrace)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(100 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// Reload sends SIGHUP so the broker re-reads authorization without dropping
// unaffected clients. The Manager calls it after a rewrite. With no broker
// running (not started yet, or down in backoff) there is nothing to signal
// and nothing lost: the next start reads the rewritten files.
func (s *Supervisor) Reload() error {
	s.mu.Lock()
	pid, ready := s.pid, s.ready
	s.mu.Unlock()
	if pid == 0 || !ready {
		log.Printf("bus: conf rewritten while no broker is running; the next start reads it")
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		return fmt.Errorf("bus: SIGHUP pid %d: %w", pid, err)
	}
	s.emit(events.KindBusReloaded, events.SeverityInfo, fmt.Sprintf("nats-server pid %d reloaded authorization", pid))
	// A reload is asynchronous in the broker; read the structure once it
	// has had the auth retry window to land, off the caller's path (the
	// caller is the apply RPC under the manager lock).
	s.mu.Lock()
	ctx := s.parent
	s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(authRetryWindow):
		}
		s.checkStructure(ctx)
	}()
	return nil
}

// Stop ends supervision. With keep the broker is left running for the next
// daemon to adopt (reexec, or `marvel stop --keep-bus`); otherwise the
// broker's lifetime is the daemon's and it is terminated. The pidfile stays
// when kept, since it is how the successor adopts.
func (s *Supervisor) Stop(keep bool) {
	s.mu.Lock()
	s.stopping = true
	if s.cancel != nil {
		s.cancel()
	}
	pid, done := s.pid, s.watchDone
	s.ready = false
	s.pid, s.cmd, s.watchDone = 0, nil, nil // a second Stop is a no-op
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	if pid == 0 {
		return
	}
	if keep {
		log.Printf("bus: leaving nats-server pid %d running for the next daemon to adopt", pid)
		return
	}
	// Our own child was already reaped by cmd.Wait inside watch; an adopted
	// process is not ours to wait on, so both are given the grace by polling.
	s.terminate(pid)
	_ = os.Remove(s.pidFile)
	s.emit(events.KindBusStopped, events.SeverityInfo, fmt.Sprintf("nats-server pid %d stopped with the daemon", pid))
}

// Ready reports whether sessions may spawn against the bus.
func (s *Supervisor) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readyLocked()
}

// readyLocked is the ready predicate: listener and pid (ready), and the
// last structural reading healthy when one exists. Caller holds s.mu.
func (s *Supervisor) readyLocked() bool {
	return s.ready && (!s.structureKnown || s.structure.Healthy())
}

// checkStructure takes one structural reading and acts on it: a miss
// re-provisions through AfterReady, an authorization miss gets one SIGHUP,
// ready drops while the problem stands so BusGate holds new spawns, and
// bus.unprovisioned is emitted once per change in the problem set. A
// read error is no information and changes nothing. The broker is never
// restarted here: a missing stream is metadata, and a restart would drop
// every live connection to fix a lookup.
func (s *Supervisor) checkStructure(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkStructureLocked(ctx)
}

// checkStructureLocked is checkStructure with s.mu held. It releases the
// lock around the broker reads so a slow broker cannot hold Status.
func (s *Supervisor) checkStructureLocked(ctx context.Context) {
	if !s.ready || s.stopping {
		return
	}
	read := s.checkStructureFn
	if read == nil {
		read = s.readStructure
	}
	pid := s.pid
	s.mu.Unlock()
	st, err := read(ctx)
	if err == nil && !st.Provisioned() && s.AfterReady != nil {
		// Re-provision now, idempotently, and read again so the common
		// case clears inside the same tick.
		if perr := s.AfterReady(); perr != nil {
			log.Printf("bus: re-provision after a structural miss (%s): %v", st.Problems(), perr)
		} else if again, aerr := read(ctx); aerr == nil {
			st = again
		}
	}
	s.mu.Lock()
	if err != nil {
		log.Printf("bus: structural check: no reading (%v); keeping the last one", err)
		return
	}
	if !st.Authorized && !s.authReloaded && pid != 0 {
		// One reload per authorized miss; then hold. Never a restart.
		s.authReloaded = true
		if kerr := syscall.Kill(pid, syscall.SIGHUP); kerr != nil {
			log.Printf("bus: SIGHUP pid %d after an authorization miss: %v", pid, kerr)
		} else {
			log.Printf("bus: authorization not loaded on pid %d; sent one SIGHUP, re-checking on the next tick", pid)
		}
	}
	if st.Authorized {
		s.authReloaded = false
	}
	prev, prevKnown := s.problems, s.structureKnown
	s.structure, s.structureKnown = st, true
	s.problems = st.Problems()
	switch {
	case s.problems != "" && s.problems != prev:
		s.emit(events.KindBusUnprovisioned, events.SeverityWarning, fmt.Sprintf("broker %s: %s; new spawns hold until it is restored, nothing is restarted", s.mgr.bus.Listen, s.problems))
	case s.problems == "" && prevKnown && prev != "":
		log.Printf("bus: structure restored on %s; releasing the spawn hold", s.mgr.bus.Listen)
	}
}

// readStructure is the production reader: the admin identity against the
// broker URL and the loopback monitor.
func (s *Supervisor) readStructure(ctx context.Context) (Structure, error) {
	mon, err := MonitorAddr(s.mgr.bus.Listen)
	if err != nil {
		return Structure{}, err
	}
	admin := s.mgr.Admin()
	return CheckStructure(ctx, s.mgr.URL(), admin.Name, admin.Password, mon)
}

// LeafSeedInEnv reports whether the running broker was spawned with the leaf
// seed in its environment. The daemon's enrollment path reads it to decide
// whether a newly enrolled leaf comes up on a reload or needs a fresh process.
func (s *Supervisor) LeafSeedInEnv() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leafSeedInEnv
}

// Status snapshots the broker for `marvel bus status`.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Managed: true, Listen: s.mgr.bus.Listen, URL: s.mgr.bus.URL, Domain: s.mgr.Domain(),
		Class: s.mgr.bus.Class, Provider: s.mgr.bus.Provider, Mode: string(s.mgr.bus.Mode), CallerIdentity: s.mgr.bus.CallerIdentity,
		Version:  s.version,
		ConfPath: s.mgr.ConfPath(), PID: s.pid, Adopted: s.adopted, Ready: s.readyLocked(), Restarts: s.restarts,
	}
	if s.structureKnown {
		st.Structure = &StructureStatus{
			Provisioned: s.structure.Provisioned(), Authorized: s.structure.Authorized, TLS: s.structure.TLS,
			Missing: append([]string(nil), s.structure.Missing...),
		}
	}
	if seat := s.mgr.bus.Seat; seat != nil {
		st.Seat = seat.Workspace + "/" + seat.Team
		st.SeatPassFile = s.mgr.SeatPassPath()
	}
	switch {
	case s.mgr.bus.HubURL == "":
		st.Leaf = "n/a"
	case !s.mgr.leafEnrolled():
		st.Leaf = "unenrolled"
	case !s.mgr.LeafAttached():
		st.Leaf = "detached"
	case s.leafUp == nil:
		st.Leaf = "unknown"
	case *s.leafUp:
		st.Leaf = "up"
	default:
		st.Leaf = "down"
	}
	if !s.ready && s.backoffTill.After(time.Now()) {
		st.BackoffUntil = s.backoffTill.Format(time.RFC3339)
	}
	return st
}

// pollLeaf reads /leafz on the loopback monitor and reports a transition.
// Errors reading the endpoint count as no information, not as down.
func (s *Supervisor) pollLeaf() {
	if s.mgr.bus.HubURL == "" {
		return
	}
	mon, err := MonitorAddr(s.mgr.bus.Listen)
	if err != nil {
		return
	}
	cli := &http.Client{Timeout: 3 * time.Second}
	resp, err := cli.Get("http://" + mon + "/leafz")
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Leafnodes int `json:"leafnodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return
	}
	up := body.Leafnodes > 0
	s.mu.Lock()
	prev := s.leafUp
	s.leafUp = &up
	s.mu.Unlock()
	if prev != nil && *prev == up {
		return
	}
	if up {
		s.emit(events.KindBusLeafUp, events.SeverityInfo, fmt.Sprintf("leaf link to %s is up (%d leafnode connection(s))", s.mgr.bus.HubURL, body.Leafnodes))
	} else if prev != nil {
		s.emit(events.KindBusLeafDown, events.SeverityWarning, fmt.Sprintf("leaf link to %s is down; local bus keeps serving, nothing is restarted", s.mgr.bus.HubURL))
	}
}

func (s *Supervisor) pidFileAlive() (int, bool) {
	data, err := os.ReadFile(s.pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if syscall.Kill(pid, 0) != nil {
		return 0, false
	}
	return pid, true
}

func (s *Supervisor) listenerAnswers(timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", s.mgr.bus.Listen, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (s *Supervisor) waitListener(ctx context.Context) bool {
	deadline := time.Now().Add(s.dialTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		if s.listenerAnswers(300 * time.Millisecond) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

func (s *Supervisor) emit(kind events.Kind, sev events.Severity, msg string) {
	log.Printf("%s: %s", kind, msg)
	if s.ring != nil {
		events.Emit(s.ring, events.Event{Kind: kind, Severity: sev, Message: msg})
	}
}
