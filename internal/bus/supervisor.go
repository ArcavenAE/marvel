package bus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/backoff"
	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/pidfile"
	"github.com/arcavenae/marvel/internal/service"
	"github.com/arcavenae/marvel/internal/workload"
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

	// Env supplies the secrets the daemon minted for the child, read fresh at
	// every spawn. The child's environment is these plus the message-bus
	// class allowlist and nothing of the daemon's own (aae-orc-oo62t). The leaf seed rides here as DIRECTOR_LEAF_NKEY
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
	// writeProbeFn is the write probe's reader; nil means ProbeWrites.
	writeProbeFn func(ctx context.Context) (WriteReading, error)
	// headroomFn is the headroom reader; nil means readHeadroom.
	headroomFn func(ctx context.Context) (Headroom, error)
	// writeFailed is the failing set of the last write-probe reading.
	writeFailed  string
	writeSkipped string
	headroomLow  string
	// backoff maps a restart count to a wait; tests shorten it.
	backoff func(n int) time.Duration
	// dialTimeout bounds the listener wait at start.
	dialTimeout time.Duration
	// leafPoll is the /leafz cadence (R-56, 30s).
	leafPoll time.Duration
	// leafRepeatEvery is how long an enrolled leaf stays down before the
	// event is repeated, and how often after that (defaultLeafDownRepeat).
	leafRepeatEvery time.Duration
	// now is the clock the leaf record reads; tests replace it.
	now func() time.Time
	// prober proves a broker's identity and is the only way a signal reaches
	// one (docs/design/bus-pidfile-identity.md section 4.5). Production holds
	// childproof.New(); a test builds one with NewForTest.
	prober *childproof.Prober
	// grace is how long terminate waits between TERM and KILL; tests shorten it.
	grace time.Duration
	// adoptPoll is how often an adopted broker's identity is re-read; tests
	// shorten it.
	adoptPoll time.Duration
	// onSpawn, when set, sees the pid of every broker this supervisor starts,
	// before anything is signalled. It is the registration point a test
	// kill seam reads, so a test signals only brokers it started.
	onSpawn func(pid int)
	// envAllow is the message-bus class's child environment allowlist.
	envAllow []string

	// proc runs the child: the spawn, the watch, the crash respawn, the
	// requested restart and the stop (internal/workload). Start builds a fresh
	// one from the fields above, so a test sets them first. Snapshot is the
	// one reading of what it holds; the supervisor keeps no copy of the pid,
	// the proven child, ready, adopted, the restart count or the backoff.
	//
	// Lock order: a Snapshot is taken before s.mu, never under it, and
	// s.mu is never held across a Process call.
	proc atomic.Pointer[workload.Process]

	mu       sync.Mutex
	parent   context.Context
	pidState string // proven, stale, legacy or none, for bus status
	version  string // what `nats-server --version` on the resolved binary reports
	// lastPid is the pid of the broker the driver last saw, kept for the
	// crash message: Process has cleared its own copy by the time Exited runs.
	lastPid  int
	stopping bool
	// readyGen is the generation of the child whose first structural reading
	// Started has finished; structureGen is the generation the last reading was
	// taken from. Ready reads both against one Snapshot.
	readyGen     uint64
	structureGen uint64
	// readSeq numbers each structural read when it starts, and appliedSeq is
	// the newest one applied: two reads overlap (the ticker's and Reload's
	// delayed one), and the one that started first must not overwrite the
	// one that started later when it lands second.
	readSeq, appliedSeq uint64
	leafUp              *bool
	// leafSince is when the current leaf state was entered, and
	// leafReportedAt when a down event was last emitted for it.
	leafSince      time.Time
	leafReportedAt time.Time
	// leafObservedAt is when /leafz was last read successfully. A failed
	// read is no information and leaves it where it was, so the reading
	// ages instead of being refreshed by an error.
	leafObservedAt time.Time
	// leafSeedInEnv records whether the running broker was spawned with the
	// leaf seed in its environment. nats-server reads its environment once at
	// start, so this is what decides whether a newly enrolled leaf can come up
	// on a reload (seed already present) or needs a fresh process to read it
	// (booted unenrolled); see the daemon enrollment path (aae-orc-ct0l4).
	leafSeedInEnv bool
	// leafSeedFP fingerprints the leaf seed the running broker was spawned
	// with (empty when none). A seed ROTATION (a new seed value) also needs a
	// fresh process, since the environment is read once at start; the daemon
	// compares this to the currently stored seed to tell rotation from an
	// identical re-put.
	leafSeedFP string
	// leafStateKnown is false for an adopted broker whose sidecar predates
	// the leaf_seed_fp field (schema 1): it then cannot be said whether the
	// broker holds a leaf seed. True for a broker this process spawned.
	leafStateKnown bool
	// leafLossCause is what the daemon knows about why it starts without a
	// leaf credential the broker holds (see SetLeafLossCause). Empty reads as
	// "unknown".
	leafLossCause string

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
	// Pidfile is what the bus pidfile proved at the last start: proven, stale
	// (removed, nothing signalled), legacy (no identity record) or none.
	Pidfile string `json:"pidfile,omitempty"`
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
	// WriteFailed names, comma separated, the declared objects whose last
	// canary write failed. A string so Status stays comparable.
	WriteFailed string `json:"write_failed,omitempty"`
	// WriteSkipped names the declared objects the probe did not write to
	// because a real limit would have made the canary evict or be refused.
	WriteSkipped string `json:"write_skipped,omitempty"`
	// HeadroomLow names, comma separated, the stores near their cap: the
	// file store, the memory store, the store filesystem.
	HeadroomLow string `json:"headroom_low,omitempty"`
	// Leaf is "up", "down", "detached" (enrolled but the operator disconnected
	// it), "unenrolled" (hub with no seed), "n/a" (no hub), or "unknown" (not
	// polled yet).
	Leaf string `json:"leaf"`
	// LeafSince is when the up or down reading was entered (RFC3339) and
	// LeafFor how long ago that was, so "down" reads apart from "down for
	// two hours". Empty for the other leaf states.
	LeafSince string `json:"leaf_since,omitempty"`
	LeafFor   string `json:"leaf_for,omitempty"`
	// LeafObservedAt is when the last successful /leafz probe was read
	// (RFC3339), and LeafValidUntil when that reading stops being current.
	// They are the probe's age, which LeafSince is not: a leaf that has been
	// up for a day and was last read a second ago, or last read ten minutes
	// ago because the monitor stopped answering, has the same LeafSince.
	// Set only with an up or down reading.
	LeafObservedAt string `json:"leaf_observed_at,omitempty"`
	LeafValidUntil string `json:"leaf_valid_until,omitempty"`
	// LeafSource says where the broker's leaf credential stands, with a hub
	// configured: "store" (this daemon's store holds a seed), "dropped-on-adopt"
	// (the adopted broker was started with a seed and the store holds none), or
	// "unknown" (an adopted broker whose sidecar predates the record). Empty
	// when no hub is configured or no leaf seed was ever involved.
	LeafSource string `json:"leaf_source,omitempty"`
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

// A broker restarts on backoff.Restart, the schedule internal/team uses for
// roles, so a broker and a session that crash-loop read the same way to an
// operator.
const (
	defaultDialTimeout = 10 * time.Second
	defaultLeafPoll    = 30 * time.Second
	// leafValidPolls is how many poll intervals a /leafz reading stays
	// current. Two tolerates one missed poll before the reading reads as
	// expired.
	leafValidPolls = 2
	// defaultLeafDownRepeat is one fleet value, changeable as this constant:
	// operator ruling 2026-10-06 recorded under Q1 in
	// docs/design/wake-service.md.
	defaultLeafDownRepeat = 30 * time.Minute
	stopGrace             = 5 * time.Second
)

// restartBackoffMax is the cap of the restart schedule, kept for the
// supervisor's own table test.
var restartBackoffMax = backoff.Restart.Max

// computeBackoff is the restart wait before restart n: backoff.Restart.Wait.
// The supervisor's table test calls it by this name.
func computeBackoff(n int) time.Duration { return backoff.Restart.Wait(n) }

// NewSupervisor resolves the nats-server binary and prepares a supervisor
// for the manager's broker. An absent binary is an error the daemon should
// refuse to start over, naming the runtime dependency, the tmux posture.
func NewSupervisor(mgr *Manager, runDir, logDir string, ring *events.Ring) (*Supervisor, error) {
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		return nil, fmt.Errorf("nats-server not found on PATH; cluster %s declares a managed bus, install it (mise or brew) or set bus.managed: false: %w", mgr.Domain(), err)
	}
	class, err := service.Class(service.ClassMessageBus)
	if err != nil {
		return nil, fmt.Errorf("bus: %w", err)
	}
	return &Supervisor{
		mgr:             mgr,
		binary:          bin,
		envAllow:        class.ChildEnvAllow,
		version:         binaryVersion(bin),
		pidFile:         filepath.Join(runDir, "nats-server.pid"),
		logPath:         filepath.Join(logDir, "nats-server.log"),
		ring:            ring,
		backoff:         backoff.Restart.Wait,
		dialTimeout:     defaultDialTimeout,
		leafPoll:        defaultLeafPoll,
		leafRepeatEvery: defaultLeafDownRepeat,
		now:             time.Now,
		prober:          childproof.New(),
		grace:           stopGrace,
		pidState:        pidStateNone,
	}, nil
}

// The values of the pidfile line in `bus status`.
const (
	pidStateNone   = "none"
	pidStateProven = "proven"
	pidStateStale  = "stale"
	pidStateLegacy = "legacy"
)

// SetDialTimeout sets how long Start and Restart wait for the listener. It
// exists for tests in other packages that start a real nats-server and must
// outlast a loaded host; the daemon uses the default.
func (s *Supervisor) SetDialTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dialTimeout = d
}

// Start adopts a broker a previous daemon left at the pidfile, or starts a
// fresh one, then waits for the listener and marks the bus ready. A port
// answered by a process marvel did not record refuses loudly, the same
// posture as the daemon pidfile guard: marvel never adopts a stranger.
// It holds no lock across the Process call, which runs the driver's Ready
// and Started (supervisor_driver.go).
func (s *Supervisor) Start(ctx context.Context) error {
	proc := s.newProcess()
	s.proc.Store(proc)
	s.mu.Lock()
	s.parent = ctx
	s.stopping = false
	s.mu.Unlock()

	v := s.classifyPidfile()
	s.mu.Lock()
	s.pidState = v.kind
	s.mu.Unlock()
	switch v.kind {
	case pidStateProven:
		if s.listenerAnswers(time.Second) {
			// What the broker was spawned with, from the sidecar the spawning
			// daemon wrote: not from this daemon's store, which a reexec or a
			// restart leaves empty, and not by a re-write of the sidecar.
			s.mu.Lock()
			s.leafStateKnown = v.rec.leafStateKnown()
			s.leafSeedFP = v.rec.LeafSeedFP
			s.leafSeedInEnv = v.rec.LeafSeedFP != ""
			s.mu.Unlock()
			log.Printf("bus: adopted running nats-server pid %d on %s", v.pid, s.mgr.bus.Listen)
			// The broker still holds the passwords the previous daemon minted;
			// this daemon has just rendered fresh ones. Reload before anything
			// connects, so the admin identity and the next session's credential
			// are the ones the broker knows. Connected clients are kept.
			if err := s.prober.Signal(v.child, syscall.SIGHUP, false); err != nil {
				log.Printf("bus: reload adopted nats-server pid %d: %v", v.pid, err)
			}
			return proc.Adopt(ctx, v.child)
		}
		// Proven and not listening: it is ours and wedged.
		log.Printf("bus: pidfile names our nats-server pid %d, which is not listening on %s; stopping it", v.pid, s.mgr.bus.Listen)
		s.terminate(v.child)
		s.removeIdentityFiles()
	case pidStateStale:
		s.removeIdentityFiles()
		s.emit(events.KindBusPidfileStale, events.SeverityWarning, fmt.Sprintf("pidfile names pid %d, which is not the broker marvel recorded (reason %s); the pidfile and identity record are removed and nothing was signalled", v.pid, v.reason))
		if s.listenerAnswers(500 * time.Millisecond) {
			return s.refuseUnproven(v.pid, "the pidfile does not prove it is ours")
		}
	case pidStateLegacy:
		if done, err := s.startLegacy(v); done {
			return err
		}
	}
	if s.listenerAnswers(500 * time.Millisecond) {
		return errors.New(strangerRefusal(s.mgr.bus.Listen))
	}
	return proc.Spawn(ctx)
}

// strangerRefusal is the refusal for a port answered by a process no pidfile
// of ours claims.
func strangerRefusal(listen string) string {
	return fmt.Sprintf("bus: %s is already answering and no marvel pidfile claims it; refusing to adopt a stranger (stop it, or set bus.managed: false and bus.url to adopt it explicitly)", listen)
}

// refuseUnproven reports a port held by a child the pidfile does not prove
// ours, once, and leaves the bus down with nothing signalled (design section
// 4.2).
func (s *Supervisor) refuseUnproven(pid int, why string) error {
	remedy := "stop it by hand, or set bus.managed: false and bus.url to adopt it explicitly"
	s.emit(events.KindBusPidfileUnproven, events.SeverityWarning, fmt.Sprintf("port held, child unproven: %s is held by pid %d, %s; the bus stays down and nothing was signalled; %s", s.mgr.bus.Listen, pid, why, remedy))
	return fmt.Errorf("bus: port held, child unproven: %s is answering and pid %d is not proven to be this daemon's broker (%s); nothing was signalled; %s", s.mgr.bus.Listen, pid, why, remedy)
}

// removeIdentityFiles removes the pidfile and its identity record.
func (s *Supervisor) removeIdentityFiles() {
	_ = os.Remove(s.pidFile)
	_ = os.Remove(sidecarPath(s.pidFile))
}

// startLegacy handles a pidfile with no identity record: the first start after
// the upgrade that writes one. With the listener silent it is a stale file.
// With the listener answering, the child is unproven and nothing is done about
// it: V11 was ruled (b) by the operator, so a process that looks like this
// daemon's broker is no more proven than any other, and the start refuses with
// "port held, child unproven". Better handling of this case is left to a
// separate design. done is true when Start is over.
func (s *Supervisor) startLegacy(v pidVerdict) (done bool, err error) {
	if !s.listenerAnswers(time.Second) {
		log.Printf("bus: pidfile names pid %d with no identity record and nothing is listening on %s; removing it", v.pid, s.mgr.bus.Listen)
		_ = os.Remove(s.pidFile)
		return false, nil
	}
	return true, s.refuseUnproven(v.pid, "its pidfile has no identity record")
}

// classifyPidfile reads the pidfile and its identity record and decides what
// the pidfile proves (docs/design/bus-pidfile-identity.md section 4.2). It
// reads a process's identity only after a record names it, and it signals
// nothing.
func (s *Supervisor) classifyPidfile() pidVerdict {
	pid := pidfile.Read(s.pidFile)
	if pid == 0 {
		return pidVerdict{kind: pidStateNone}
	}
	rec, err := readIdentityFile(sidecarPath(s.pidFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return pidVerdict{pid: pid, kind: pidStateLegacy}
	case err != nil:
		return pidVerdict{pid: pid, kind: pidStateStale, reason: "sidecar"}
	case rec.PID != pid:
		return pidVerdict{pid: pid, kind: pidStateStale, reason: "sidecar-pid"}
	}
	c, err := s.prober.Prove(pid, rec.identity())
	if err != nil {
		reason := "unproven"
		var mm *childproof.MismatchError
		if errors.As(err, &mm) {
			reason = mm.Reason
		}
		return pidVerdict{pid: pid, kind: pidStateStale, reason: reason}
	}
	return pidVerdict{pid: pid, kind: pidStateProven, child: c, rec: rec}
}

// binaryVersion runs `<bin> --version` and returns the version token
// ("v2.14.6" from "nats-server: v2.14.6"), or "unknown" when the binary
// does not answer. Read once at construction; the pin lives in mise.toml
// and this is how a disagreeing PATH shows up in marvel bus status. The
// probe child gets the message-bus class allowlist and no minted keys, not
// the daemon's own environment (aae-orc-433bj).
func binaryVersion(bin string) string {
	class, err := service.Class(service.ClassMessageBus)
	if err != nil {
		return "unknown"
	}
	cmd := exec.Command(bin, "--version")
	cmd.Env = workload.AllowedEnv(class.ChildEnvAllow)
	out, err := cmd.Output()
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

// envValue returns the value of key in a KEY=VALUE environment slice. The last
// match wins, mirroring how exec resolves a repeated variable.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return env[i][len(prefix):], true
		}
	}
	return "", false
}

// SeedFingerprint is a stable, non-reversible identifier for a leaf seed. It
// lets the daemon tell a seed rotation from an identical re-put without ever
// logging or comparing the seed itself.
func SeedFingerprint(seed []byte) string {
	sum := sha256.Sum256(seed)
	return hex.EncodeToString(sum[:8])
}

// Restart ends the running broker and starts a fresh one from the current
// conf and environment. Reload cannot do this: the leaf seed rides in the
// child's environment, which the server reads once at start, so a later
// `credential put bus/leaf` (aae-orc-vqq2c) needs a new process. Clients
// reconnect; the restart is counted separately from crashes and is not
// subject to backoff. Safe while the broker is down: it becomes the start.
func (s *Supervisor) Restart(reason string) error {
	proc := s.proc.Load()
	if proc == nil {
		return errors.New("bus: restart before start")
	}
	if pid := proc.Snapshot().Pid; pid != 0 {
		log.Printf("bus: stopping nats-server pid %d to restart (%s)", pid, reason)
	}
	return proc.Restart(reason)
}

// terminate ends a proven broker through workload.Terminate, the one
// TERM, wait and KILL. A broker that is gone or no longer the one proven is
// left alone.
func (s *Supervisor) terminate(c childproof.Child) {
	if err := workload.Terminate(s.prober, c, s.grace); err != nil {
		var te *workload.TerminateError
		if errors.As(err, &te) {
			log.Printf("bus: not %s nats-server pid %d: %v", te.Step, te.Pid, te.Err)
			return
		}
		log.Printf("bus: %v", err)
	}
}

// Reload sends SIGHUP so the broker re-reads authorization without dropping
// unaffected clients. The Manager calls it after a rewrite. With no broker
// running (not started yet, or down in backoff) there is nothing to signal
// and nothing lost: the next start reads the rewritten files. It reads the
// child from Snapshot and signals it; Process has no Reload, so the Manager's
// reload never re-enters the driver through Process.
func (s *Supervisor) Reload() error {
	snap := s.snapshot()
	if snap.Pid == 0 || !snap.Ready {
		log.Printf("bus: conf rewritten while no broker is running; the next start reads it")
		return nil
	}
	if err := s.prober.Signal(snap.Child, syscall.SIGHUP, false); err != nil {
		return fmt.Errorf("bus: SIGHUP pid %d: %w", snap.Pid, err)
	}
	s.emit(events.KindBusReloaded, events.SeverityInfo, fmt.Sprintf("nats-server pid %d reloaded authorization", snap.Pid))
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
	s.mu.Unlock()
	proc := s.proc.Load()
	if proc == nil {
		return
	}
	pid := proc.Snapshot().Pid
	proc.Stop(keep)
	if pid == 0 { // a second Stop, or none to stop
		return
	}
	if keep {
		log.Printf("bus: leaving nats-server pid %d running for the next daemon to adopt", pid)
		return
	}
	s.removeIdentityFiles()
	s.emit(events.KindBusStopped, events.SeverityInfo, fmt.Sprintf("nats-server pid %d stopped with the daemon", pid))
}

// snapshot is one reading of what Process holds, the first step of every
// method that also takes s.mu. A Process nobody has started reads as zero.
func (s *Supervisor) snapshot() workload.State {
	if p := s.proc.Load(); p != nil {
		return p.Snapshot()
	}
	return workload.State{PidState: workload.PidStateNone}
}

// process is the Process, built from the current fields on first use. A test
// that stages state before Start reaches it here; Start builds a fresh one.
func (s *Supervisor) process() *workload.Process {
	if p := s.proc.Load(); p != nil {
		return p
	}
	s.proc.CompareAndSwap(nil, s.newProcess())
	return s.proc.Load()
}

// Ready reports whether sessions may spawn against the bus.
func (s *Supervisor) Ready() bool {
	snap := s.snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readyLocked(snap)
}

// readyLocked is the ready predicate: the child is ready and Started has
// finished its first structural reading (readyGen), and the last reading, when
// one exists, is this child's and healthy. A reading taken from another child
// does not count, so a restarted broker is not ready on its dead
// predecessor's reading. Caller holds s.mu and passes a Snapshot taken before
// it.
func (s *Supervisor) readyLocked(snap workload.State) bool {
	return snap.Ready && snap.Gen == s.readyGen &&
		(!s.structureKnown || (s.structureGen == snap.Gen && s.structure.Healthy()))
}

// checkStructure takes one structural reading and acts on it: a miss
// re-provisions through AfterReady, an authorization miss gets one SIGHUP,
// ready drops while the problem stands so BusGate holds new spawns, and
// bus.unprovisioned is emitted once per change in the problem set. A
// read error is no information and changes nothing. The broker is never
// restarted here: a missing stream is metadata, and a restart would drop
// every live connection to fix a lookup.
func (s *Supervisor) checkStructure(ctx context.Context) {
	snap := s.snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkStructureLocked(ctx, snap)
	// The structural tick also retries a per-role user the broker had not yet
	// accepted, so a confirmation that missed its window heals on its own.
	s.mgr.confirmAsync()
}

// checkStructureLocked is checkStructure with s.mu held and a Snapshot taken
// before it. It releases the lock around the broker reads so a slow broker
// cannot hold Status. The reading is tagged with the snapshot's generation,
// and one that lands after a respawn (Started has moved structureGen on) is
// dropped, so no child's reading is read as another's. A reading that started
// before one already applied is dropped too (readSeq).
func (s *Supervisor) checkStructureLocked(ctx context.Context, snap workload.State) {
	if !snap.Ready || s.stopping {
		return
	}
	read := s.checkStructureFn
	if read == nil {
		read = s.readStructure
	}
	child := snap.Child
	s.readSeq++
	seq := s.readSeq
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
	if snap.Gen != s.structureGen {
		log.Printf("bus: structural reading from child generation %d dropped: generation %d is current", snap.Gen, s.structureGen)
		return
	}
	if seq < s.appliedSeq {
		log.Printf("bus: structural reading %d dropped: reading %d, which started later, is already in effect", seq, s.appliedSeq)
		return
	}
	s.appliedSeq = seq
	if !st.Authorized && !s.authReloaded && child.Pid() != 0 {
		// One reload per authorized miss; then hold. Never a restart.
		s.authReloaded = true
		if kerr := s.prober.Signal(child, syscall.SIGHUP, false); kerr != nil {
			log.Printf("bus: SIGHUP pid %d after an authorization miss: %v", child.Pid(), kerr)
		} else {
			log.Printf("bus: authorization not loaded on pid %d; sent one SIGHUP, re-checking on the next tick", child.Pid())
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
	s.probeWritesLocked(ctx)
	s.checkHeadroomLocked(ctx)
}

// probeWritesLocked takes one write-probe reading and reports it: one
// bus.store.write-failed per change in the failing set, a log line when it
// clears. It never holds spawns and never restarts the broker. A read error
// is no information and changes nothing. It releases s.mu around the broker
// calls, as checkStructureLocked does.
func (s *Supervisor) probeWritesLocked(ctx context.Context) {
	probe := s.writeProbeFn
	if probe == nil {
		probe = s.probeWrites
	}
	s.mu.Unlock()
	w, err := probe(ctx)
	s.mu.Lock()
	if s.stopping {
		return
	}
	if err != nil {
		log.Printf("bus: write probe: no reading (%v); keeping the last one", err)
		return
	}
	prevSkipped := s.writeSkipped
	s.writeSkipped = w.SkippedNames()
	if s.writeSkipped != "" && s.writeSkipped != prevSkipped {
		log.Printf("bus: write probe skipped %s on %s: a real limit would make the canary evict or be refused", s.writeSkipped, s.mgr.bus.Listen)
	}
	prev := s.writeFailed
	s.writeFailed = w.Problems()
	switch {
	case s.writeFailed != "" && s.writeFailed != prev:
		s.emit(events.KindBusStoreWriteFailed, events.SeverityError, fmt.Sprintf("broker %s refuses canary writes: %s; the broker is not restarted and spawns are not held, free the store and the next tick re-reads", s.mgr.bus.Listen, w.detail()))
	case s.writeFailed == "" && prev != "" && s.writeSkipped != "":
		log.Printf("bus: canary writes on %s are no longer failing, but %s is skipped (a real limit), so its writes are not being tested", s.mgr.bus.Listen, s.writeSkipped)
	case s.writeFailed == "" && prev != "":
		log.Printf("bus: canary writes succeed again on %s", s.mgr.bus.Listen)
	}
}

// checkHeadroomLocked takes one headroom reading and reports it: one
// bus.store.low per change in the low set, a log line when it clears. It
// never holds spawns and never restarts the broker. A read error is no
// information and changes nothing. It releases s.mu around the reads.
func (s *Supervisor) checkHeadroomLocked(ctx context.Context) {
	read := s.headroomFn
	if read == nil {
		read = s.readHeadroom
	}
	s.mu.Unlock()
	h, err := read(ctx)
	s.mu.Lock()
	if s.stopping {
		return
	}
	if err != nil {
		log.Printf("bus: headroom check: no reading (%v); keeping the last one", err)
		return
	}
	prev := s.headroomLow
	s.headroomLow = h.Problems()
	switch {
	case s.headroomLow != "" && s.headroomLow != prev:
		s.emit(events.KindBusStoreLow, events.SeverityWarning, fmt.Sprintf("broker %s is short of room: %s; JetStream refuses writes when a store fills, free space or raise the cap, nothing is restarted", s.mgr.bus.Listen, strings.Join(h.Low(), "; ")))
	case s.headroomLow == "" && prev != "":
		log.Printf("bus: store headroom is back above the warning levels on %s", s.mgr.bus.Listen)
	}
}

// readHeadroom is the production reader: the loopback monitor and the store
// directory.
func (s *Supervisor) readHeadroom(ctx context.Context) (Headroom, error) {
	mon, err := MonitorAddr(s.mgr.bus.Listen)
	if err != nil {
		return Headroom{}, err
	}
	return ReadHeadroom(ctx, mon, s.mgr.bus.StoreDir)
}

// probeWrites is the production reader: the admin identity against the
// broker URL.
func (s *Supervisor) probeWrites(ctx context.Context) (WriteReading, error) {
	admin := s.mgr.Admin()
	return ProbeWrites(ctx, s.mgr.URL(), admin.Name, admin.Password)
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

// LeafSeedFingerprint returns a stable, non-reversible identifier for the leaf
// seed the running broker was spawned with, or "" when it booted without one.
// The daemon compares it to the currently stored seed to decide whether a
// newly stored seed is a rotation (needs a restart) or an identical re-put.
func (s *Supervisor) LeafSeedFingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leafSeedFP
}

// SetLeafLossCause records what this daemon knows about why a leaf credential
// the broker holds is missing from its store, for the event Start raises on an
// adopt. A successor that never received a handoff cannot tell a declined
// handoff from a plain restart, so the cause it can name is what it observed:
// "handoff unread" when the handoff was announced and could not be read.
// Empty reads as "unknown".
func (s *Supervisor) SetLeafLossCause(cause string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leafLossCause = cause
}

// leafSourceLocked is Status.LeafSource. Caller holds s.mu.
func (s *Supervisor) leafSourceLocked(snap workload.State) string {
	switch {
	case s.mgr.bus.HubURL == "":
		return ""
	case snap.Adopted && !s.leafStateKnown:
		return "unknown"
	case s.mgr.leafEnrolled():
		return "store"
	case s.leafSeedFP != "":
		return "dropped-on-adopt"
	}
	return ""
}

// reportLeafOnAdopt raises bus.leaf.dropped when the broker this daemon just
// adopted holds a leaf seed this daemon's store does not, or holds a different
// one. The adopt's SIGHUP has already re-read the conf, and a conf rendered
// from an empty store carries no leafnodes block, so the hub link is gone. It
// names the cause and the command, never the seed. Caller holds s.mu.
func (s *Supervisor) reportLeafOnAdopt(pid int) {
	if s.mgr.bus.HubURL == "" || !s.leafStateKnown || s.leafSeedFP == "" {
		return
	}
	cause := ""
	switch {
	case !s.mgr.leafEnrolled():
		cause = s.leafLossCause
		if cause == "" {
			cause = "unknown"
		}
	case s.Env != nil:
		if v, ok := envValue(s.Env(), LeafSeedEnv); ok && SeedFingerprint([]byte(v)) != s.leafSeedFP {
			cause = "seed differs from the broker's"
		}
	}
	if cause == "" {
		return
	}
	s.emit(events.KindBusLeafDropped, events.SeverityError, fmt.Sprintf(
		"the hub leaf credential is not carried over: the adopted broker pid %d was started with a leaf seed and this daemon's store does not hold that one (cause: %s); the link to %s is not up until the seed is pushed again: marvel credential put bus/leaf --value-file <seed-file>",
		pid, cause, s.mgr.bus.HubURL))
}

// Status snapshots the broker for `marvel bus status`.
func (s *Supervisor) Status() Status {
	snap := s.snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	pidState := s.pidState
	if snap.PidState == workload.PidStateProven {
		pidState = pidStateProven
	}
	st := Status{
		Managed: true, Listen: s.mgr.bus.Listen, URL: s.mgr.bus.URL, Domain: s.mgr.Domain(),
		Class: s.mgr.bus.Class, Provider: s.mgr.bus.Provider, Mode: string(s.mgr.bus.Mode), CallerIdentity: s.mgr.bus.CallerIdentity,
		Version:  s.version,
		ConfPath: s.mgr.ConfPath(), PID: snap.Pid, Adopted: snap.Adopted, Ready: s.readyLocked(snap), Restarts: snap.Restarts,
		Pidfile: pidState, WriteFailed: s.writeFailed, WriteSkipped: s.writeSkipped, HeadroomLow: s.headroomLow,
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
	st.LeafSource = s.leafSourceLocked(snap)
	if (st.Leaf == "up" || st.Leaf == "down") && !s.leafSince.IsZero() {
		st.LeafSince = s.leafSince.Format(time.RFC3339)
		st.LeafFor = leafDuration(s.now().Sub(s.leafSince))
	}
	if (st.Leaf == "up" || st.Leaf == "down") && !s.leafObservedAt.IsZero() {
		st.LeafObservedAt = s.leafObservedAt.Format(time.RFC3339)
		st.LeafValidUntil = s.leafObservedAt.Add(leafValidPolls * s.leafPoll).Format(time.RFC3339)
	}
	if !snap.Ready && snap.BackoffUntil.After(time.Now()) {
		st.BackoffUntil = snap.BackoffUntil.Format(time.RFC3339)
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
	s.observeLeaf(body.Leafnodes, s.now())
}

// observeLeaf records one /leafz reading taken at now and reports it. It
// reports when the state changes (a drop is reported once, whatever the
// enrollment), when an enrolled, attached leaf is found down at the first poll
// (before this, silent until it came up), when a leaf already down becomes
// enrolled (its seed is stored) or attached (the operator connects it) and has
// not been reported yet, and again at every leafRepeatEvery while an enrolled,
// attached leaf stays down. A hub with no seed and a leaf the operator
// disconnected promised no link, so neither is repeated, and a hub with no seed
// is never reported as down.
func (s *Supervisor) observeLeaf(leafnodes int, now time.Time) {
	up := leafnodes > 0
	promised := s.mgr.leafEnrolled() && s.mgr.LeafAttached()
	s.mu.Lock()
	prev := s.leafUp
	s.leafUp = &up
	changed := prev == nil || *prev != up
	s.leafObservedAt = now
	if changed {
		s.leafSince = now
		s.leafReportedAt = time.Time{}
	}
	since := s.leafSince
	repeat := !changed && !up && promised && !s.leafReportedAt.IsZero() && now.Sub(s.leafReportedAt) >= s.leafRepeatEvery
	report := (changed && !up && prev != nil) || (!up && promised && s.leafReportedAt.IsZero())
	if report || repeat {
		s.leafReportedAt = now
	}
	s.mu.Unlock()
	hub := s.mgr.bus.HubURL
	switch {
	case changed && up:
		s.emit(events.KindBusLeafUp, events.SeverityInfo, fmt.Sprintf("leaf link to %s is up (%d leafnode connection(s))", hub, leafnodes))
	case report:
		s.emit(events.KindBusLeafDown, events.SeverityWarning, fmt.Sprintf("leaf link to %s is down; local bus keeps serving, nothing is restarted", hub))
	case repeat:
		s.emit(events.KindBusLeafDown, events.SeverityWarning, fmt.Sprintf("leaf link to %s is still down: down for %s; local bus keeps serving, nothing is restarted", hub, leafDuration(now.Sub(since))))
	}
}

// leafDuration renders how long a leaf state has lasted: whole seconds under a
// minute, whole minutes after ("45s", "2h9m").
func leafDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
}

func (s *Supervisor) listenerAnswers(timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", s.mgr.bus.Listen, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (s *Supervisor) waitListener(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
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

// pidVerdict is what a pidfile proved at start.
type pidVerdict struct {
	rec    identityRecord // the sidecar, set for a proven verdict
	pid    int
	kind   string // none, proven, stale or legacy
	reason string // for stale: dead, start, exe, argv, sidecar-pid or sidecar
	child  childproof.Child
}
