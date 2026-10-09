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
	"syscall"
	"time"

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
	// legacy decides a pidfile with no identity record (V11). It starts as
	// legacyPidfile and a test sets each value.
	legacy legacyMode
	// grace is how long terminate waits between TERM and KILL; tests shorten it.
	grace time.Duration
	// adoptPoll is how often the watcher re-reads an adopted broker's identity;
	// tests shorten it.
	adoptPoll time.Duration
	// onSpawn, when set, sees the pid of every broker this supervisor starts,
	// before anything is signalled. It is the registration point a test
	// kill seam reads, so a test signals only brokers it started.
	onSpawn func(pid int)

	mu          sync.Mutex
	parent      context.Context
	cmd         *exec.Cmd
	pid         int
	child       childproof.Child // the proven broker; zero when none runs
	pidState    string           // proven, stale, legacy or none, for bus status
	version     string           // what `nats-server --version` on the resolved binary reports
	adopted     bool
	ready       bool
	stopping    bool
	restarts    int
	backoffTill time.Time
	leafUp      *bool
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
	cancel     context.CancelFunc
	watchDone  chan struct{}

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
		mgr:             mgr,
		binary:          bin,
		version:         binaryVersion(bin),
		pidFile:         filepath.Join(runDir, "nats-server.pid"),
		logPath:         filepath.Join(logDir, "nats-server.log"),
		ring:            ring,
		backoff:         computeBackoff,
		dialTimeout:     defaultDialTimeout,
		leafPoll:        defaultLeafPoll,
		leafRepeatEvery: defaultLeafDownRepeat,
		now:             time.Now,
		prober:          childproof.New(),
		legacy:          legacyPidfile,
		grace:           stopGrace,
		pidState:        pidStateNone,
	}, nil
}

// legacyMode is how a pidfile with no identity record is treated (V11).
type legacyMode int

const (
	// legacyUnruled is the placeholder until the operator rules V11 (the open
	// split in docs/design/services-shape-requirements.md section 6). It is
	// neither option: it signals nothing and adopts nothing, and a port held
	// under such a pidfile refuses with a message naming the ruling. A draft
	// carrying it cannot ship by accident, and TestLegacyPidfileIsRuled fails
	// under MARVEL_REQUIRE_V11=1 until the constant is set.
	legacyUnruled legacyMode = iota
	// legacyAdoptOnExecMatch (V11 option a) adopts a broker whose executable is
	// nats-server and whose arguments carry this daemon's conf.
	legacyAdoptOnExecMatch
	// legacyRefuseUnproven (V11 option b) neither adopts nor signals it.
	legacyRefuseUnproven
)

// legacyPidfile is set in code by the ruling, never by config: an operator
// cannot widen what marvel signals.
const legacyPidfile = legacyUnruled

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
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parent = ctx
	ctx, s.cancel = context.WithCancel(ctx)

	v := s.classifyPidfile()
	s.pidState = v.kind
	switch v.kind {
	case pidStateProven:
		if s.listenerAnswers(time.Second) {
			s.pid, s.adopted, s.child = v.pid, true, v.child
			log.Printf("bus: adopted running nats-server pid %d on %s", v.pid, s.mgr.bus.Listen)
			// The broker still holds the passwords the previous daemon minted;
			// this daemon has just rendered fresh ones. Reload before anything
			// connects, so the admin identity and the next session's credential
			// are the ones the broker knows. Connected clients are kept.
			if err := s.prober.Signal(v.child, syscall.SIGHUP, false); err != nil {
				log.Printf("bus: reload adopted nats-server pid %d: %v", v.pid, err)
			}
			return s.becomeReady(ctx, "adopted")
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
		if done, err := s.startLegacy(ctx, v); done {
			return err
		}
	}
	if s.listenerAnswers(500 * time.Millisecond) {
		return errors.New(strangerRefusal(s.mgr.bus.Listen))
	}
	if err := s.spawnLocked(); err != nil {
		return err
	}
	return s.becomeReady(ctx, "started")
}

// strangerRefusal is the refusal for a port answered by a process no pidfile
// of ours claims.
func strangerRefusal(listen string) string {
	return fmt.Sprintf("bus: %s is already answering and no marvel pidfile claims it; refusing to adopt a stranger (stop it, or set bus.managed: false and bus.url to adopt it explicitly)", listen)
}

// refuseUnproven reports a port held by a child the pidfile does not prove
// ours, once, and leaves the bus down with nothing signalled (design section
// 4.2). Caller holds s.mu.
func (s *Supervisor) refuseUnproven(pid int, why string) error {
	remedy := "stop it by hand, or set bus.managed: false and bus.url to adopt it explicitly"
	s.emit(events.KindBusPidfileUnproven, events.SeverityWarning, fmt.Sprintf("%s is held by a child (pid %d) that is unproven: %s; the bus stays down and nothing was signalled; %s", s.mgr.bus.Listen, pid, why, remedy))
	return fmt.Errorf("bus: %s is answering and its child (pid %d) is unproven: %s; nothing was signalled; %s", s.mgr.bus.Listen, pid, why, remedy)
}

// removeIdentityFiles removes the pidfile and its identity record.
func (s *Supervisor) removeIdentityFiles() {
	_ = os.Remove(s.pidFile)
	_ = os.Remove(sidecarPath(s.pidFile))
}

// startLegacy handles a pidfile with no identity record: the first start after
// the upgrade that writes one (V11). With the listener silent it is a stale
// file under every ruling. With the listener answering, the ruling decides.
// done is true when Start is over. Caller holds s.mu.
func (s *Supervisor) startLegacy(ctx context.Context, v pidVerdict) (done bool, err error) {
	if !s.listenerAnswers(time.Second) {
		log.Printf("bus: pidfile names pid %d with no identity record and nothing is listening on %s; removing it", v.pid, s.mgr.bus.Listen)
		_ = os.Remove(s.pidFile)
		return false, nil
	}
	switch s.legacy {
	case legacyAdoptOnExecMatch:
		c, aerr := s.prober.AdoptLegacy(v.pid, s.mgr.ConfPath())
		if aerr != nil {
			_ = os.Remove(s.pidFile)
			s.pidState = pidStateStale
			log.Printf("bus: pidfile with no identity record names pid %d, which is not a nats-server running %s: %v", v.pid, s.mgr.ConfPath(), aerr)
			return true, errors.New(strangerRefusal(s.mgr.bus.Listen))
		}
		s.pid, s.adopted, s.child = v.pid, true, c
		log.Printf("bus: adopted legacy nats-server pid %d on %s (executable and conf match, no start time recorded)", v.pid, s.mgr.bus.Listen)
		if err := s.prober.Signal(c, syscall.SIGHUP, false); err != nil {
			log.Printf("bus: reload adopted nats-server pid %d: %v", v.pid, err)
		}
		if err := os.MkdirAll(filepath.Dir(s.pidFile), 0o700); err == nil {
			if err := writeIdentity(sidecarPath(s.pidFile), v.pid, c.Identity(), s.now()); err != nil {
				log.Printf("bus: record the identity of adopted pid %d: %v", v.pid, err)
			}
		}
		s.pidState = pidStateProven
		return true, s.becomeReady(ctx, "adopted")
	case legacyRefuseUnproven:
		return true, s.refuseUnproven(v.pid, "its pidfile has no identity record (V11, refuse)")
	default:
		return true, s.refuseUnproven(v.pid, "its pidfile has no identity record and the legacy rule (V11) is not ruled")
	}
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
	return pidVerdict{pid: pid, kind: pidStateProven, child: c}
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
	// The broker now has the file it started or reloaded with: ask whether it
	// accepts the per-role users, off this path.
	s.mgr.confirmAsync()
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
	go s.watch(ctx, s.watchDone, s.cmd, s.child)
	return nil
}

// spawnLocked starts the broker through workload.Start, the one spawn path
// for a managed child: its environment is the message-bus class allowlist
// plus the minted leaf seed, never the daemon's own (aae-orc-oo62t).
// Caller holds s.mu.
func (s *Supervisor) spawnLocked() error {
	conf := s.mgr.ConfPath()
	body, err := os.ReadFile(conf)
	if err != nil {
		return fmt.Errorf("bus: read %s: %w", conf, err)
	}
	class, err := service.Class(service.ClassMessageBus)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}
	// The identity record is written before the pidfile that names it, from
	// what the kernel says about the child now (design section 3.1). Without
	// an identity the child is stopped: a broker marvel cannot prove later is
	// one it could never stop.
	var spawned childproof.Identity
	child, err := workload.Start(workload.ProcessSpec{
		Name:      "bus",
		Binary:    s.binary,
		Args:      []string{"-c", conf},
		EnvAllow:  class.ChildEnvAllow,
		MintedEnv: s.Env,
		Check: func(env []string) error {
			if strings.Contains(string(body), "$"+LeafSeedEnv) && !hasEnv(env, LeafSeedEnv) {
				return fmt.Errorf("bus: %s references $%s but no %s credential is in the store; the broker would refuse to start", conf, LeafSeedEnv, "bus/leaf")
			}
			return nil
		},
		LogPath: s.logPath,
		PidFile: s.pidFile,
		BeforePidFile: func(pid int) error {
			if s.onSpawn != nil {
				s.onSpawn(pid)
			}
			id, err := s.prober.Identify(pid)
			if err != nil {
				return fmt.Errorf("read the identity of the new broker (pid %d): %w", pid, err)
			}
			spawned = id
			err = os.MkdirAll(filepath.Dir(s.pidFile), 0o700)
			if err == nil {
				err = writeIdentity(sidecarPath(s.pidFile), pid, id, s.now())
			}
			if err != nil {
				_ = os.Remove(sidecarPath(s.pidFile))
				log.Printf("bus: record the identity of nats-server pid %d: %v", pid, err)
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	proven, err := s.prober.Prove(child.Pid, spawned)
	if err != nil {
		// The child is ours and not yet reaped, so its pid cannot have been
		// reused: stop it through the handle.
		_ = child.Cmd.Process.Kill()
		_ = child.Cmd.Wait()
		return fmt.Errorf("bus: the new broker (pid %d) does not match the identity read at start: %w", child.Pid, err)
	}
	// Record whether this process can resolve a leaf remote's seed on a later
	// reload. A broker spawned with the seed already present picks up a
	// connect on SIGHUP; one spawned without it needs a fresh process when a
	// seed is enrolled (aae-orc-ct0l4). Also fingerprint the spawned seed so a
	// later rotation (a different seed value) is distinguished from an
	// identical re-put.
	s.leafSeedInEnv = hasEnv(child.Env, LeafSeedEnv)
	if v, ok := envValue(child.Env, LeafSeedEnv); ok {
		s.leafSeedFP = SeedFingerprint([]byte(v))
	} else {
		s.leafSeedFP = ""
	}
	s.cmd, s.pid, s.adopted, s.child, s.pidState = child.Cmd, child.Pid, false, proven, pidStateProven
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

// watch follows the child: a pid exit marvel did not ask for is
// bus.crashed and a restart under backoff; the hub leaf link is polled
// on the 30s cadence and reported on transition. Sessions are never
// restarted for either: their shims reconnect.
func (s *Supervisor) watch(ctx context.Context, done chan struct{}, cmd *exec.Cmd, c childproof.Child) {
	defer close(done)
	exit := make(chan error, 1)
	go func() {
		if cmd != nil {
			exit <- cmd.Wait()
			return
		}
		// Adopted: not our child, so poll its identity.
		every := s.adoptPoll
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
				// reads as the broker having exited.
				if !s.prober.Same(c) {
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
	pid, child, done := s.pid, s.child, s.watchDone
	s.ready, s.pid, s.cmd, s.child, s.watchDone = false, 0, nil, childproof.Child{}, nil
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	if pid != 0 {
		s.terminate(child)
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

// terminate ends a proven broker: TERM to its group, a grace period polled
// by identity so a pid handed to another process counts as exited, then KILL.
// Every signal re-proves the child first (Prober.Signal), so a change at any
// point stops the rest. A broker that is gone or no longer the one proven is
// left alone.
func (s *Supervisor) terminate(c childproof.Child) {
	if err := s.prober.Signal(c, syscall.SIGTERM, true); err != nil {
		log.Printf("bus: not stopping nats-server pid %d: %v", c.Pid(), err)
		return
	}
	grace := s.grace
	if grace <= 0 {
		grace = stopGrace
	}
	step := min(100*time.Millisecond, grace/4)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && s.prober.Same(c) {
		time.Sleep(step)
	}
	if s.prober.Same(c) {
		if err := s.prober.Signal(c, syscall.SIGKILL, true); err != nil {
			log.Printf("bus: not killing nats-server pid %d: %v", c.Pid(), err)
		}
	}
}

// Reload sends SIGHUP so the broker re-reads authorization without dropping
// unaffected clients. The Manager calls it after a rewrite. With no broker
// running (not started yet, or down in backoff) there is nothing to signal
// and nothing lost: the next start reads the rewritten files.
func (s *Supervisor) Reload() error {
	s.mu.Lock()
	pid, child, ready := s.pid, s.child, s.ready
	s.mu.Unlock()
	if pid == 0 || !ready {
		log.Printf("bus: conf rewritten while no broker is running; the next start reads it")
		return nil
	}
	if err := s.prober.Signal(child, syscall.SIGHUP, false); err != nil {
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
	pid, child, done := s.pid, s.child, s.watchDone
	s.ready = false
	s.pid, s.cmd, s.child, s.watchDone = 0, nil, childproof.Child{}, nil // a second Stop is a no-op
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
	s.terminate(child)
	s.removeIdentityFiles()
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
	// The structural tick also retries a per-role user the broker had not yet
	// accepted, so a confirmation that missed its window heals on its own.
	s.mgr.confirmAsync()
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
	child := s.child
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

// Status snapshots the broker for `marvel bus status`.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Managed: true, Listen: s.mgr.bus.Listen, URL: s.mgr.bus.URL, Domain: s.mgr.Domain(),
		Class: s.mgr.bus.Class, Provider: s.mgr.bus.Provider, Mode: string(s.mgr.bus.Mode), CallerIdentity: s.mgr.bus.CallerIdentity,
		Version:  s.version,
		ConfPath: s.mgr.ConfPath(), PID: s.pid, Adopted: s.adopted, Ready: s.readyLocked(), Restarts: s.restarts,
		Pidfile: s.pidState,
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
	if (st.Leaf == "up" || st.Leaf == "down") && !s.leafSince.IsZero() {
		st.LeafSince = s.leafSince.Format(time.RFC3339)
		st.LeafFor = leafDuration(s.now().Sub(s.leafSince))
	}
	if (st.Leaf == "up" || st.Leaf == "down") && !s.leafObservedAt.IsZero() {
		st.LeafObservedAt = s.leafObservedAt.Format(time.RFC3339)
		st.LeafValidUntil = s.leafObservedAt.Add(leafValidPolls * s.leafPoll).Format(time.RFC3339)
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

// pidVerdict is what a pidfile proved at start.
type pidVerdict struct {
	pid    int
	kind   string // none, proven, stale or legacy
	reason string // for stale: dead, start, exe, argv, sidecar-pid or sidecar
	child  childproof.Child
}
