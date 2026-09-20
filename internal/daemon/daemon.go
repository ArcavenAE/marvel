// Package daemon provides the marvel daemon — a long-running process
// that manages sessions via tmux and serves CLI requests over Unix sockets,
// SSH tunnels, or (for advanced use) bare TCP sockets.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/logbuf"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/service"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/team"
	"github.com/arcavenae/marvel/internal/tmux"
	"github.com/arcavenae/marvel/internal/usage"
)

const (
	// The default socket path used to be declared here as well as in
	// internal/config, both as the literal /tmp/marvel.sock. This one
	// had no callers, which is the whole hazard: a second declaration
	// of a value like this survives a fix to the first and comes back
	// the moment someone reaches for the nearest constant. The single
	// resolution point is now config.ResolveSocket.

	// DefaultMRVLPort is the default port for the mrvl:// protocol.
	DefaultMRVLPort = "6785"
	// ReconcileInterval is how often the team controller reconciles.
	ReconcileInterval = 2 * time.Second
	// MetricsInterval is how often the process sampler rolls up CPU and
	// memory over each session's pid subtree. Slower than
	// ReconcileInterval on purpose: a pass reads the whole process table,
	// and the numbers inform an operator rather than a control decision.
	MetricsInterval = 5 * time.Second
)

// listenNetwork returns "tcp" if the address looks like host:port,
// otherwise "unix". The host:port rule itself is paths.IsTCPAddr, which
// is the single definition shared with the client and with the socket
// length check.
func listenNetwork(addr string) string {
	if paths.IsTCPAddr(addr) {
		return "tcp"
	}
	return "unix"
}

// isMRVL returns true if the address is a mrvl:// URL.
func isMRVL(addr string) bool {
	return strings.HasPrefix(addr, "mrvl://")
}

// isSSH returns true if the address is an ssh:// URL.
func isSSH(addr string) bool {
	return strings.HasPrefix(addr, "ssh://")
}

// Request is a JSON-RPC-like request from the CLI.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC-like response to the CLI.
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`

	// DaemonHome is the layout home this daemon is rooted at
	// (~/.marvel), stamped on every response so a client can tell
	// whether the daemon it reached is the one it meant. Empty from a
	// daemon that predates the field, and omitempty keeps the envelope
	// readable by a client that predates it, so no protocol version bump
	// or handshake is involved.
	//
	// Diagnostic, not preventive. A field on the RESPONSE is read after
	// the request has already been sent: for read methods that is fine,
	// but for mutating methods the client learns it hit the wrong daemon
	// after it has already changed it. Prevention would put the
	// expectation on the REQUEST and have the daemon reject a mismatch,
	// which is authorization-shaped and belongs with aae-orc-sqh0. See
	// docs/design/daemon-isolation.md decision 8.
	DaemonHome string `json:"daemon_home,omitempty"`
}

// DefaultLogBufferLines is the default ring-buffer depth for the
// daemon's in-memory log tail. About 10k lines ≈ 1–2 MB at typical
// daemon verbosity.
const DefaultLogBufferLines = 10000

// Daemon is the marvel daemon.
type Daemon struct {
	store     *api.Store
	sessMgr   *session.Manager
	teamCtrl  *team.Controller
	driver    *tmux.Driver
	listener  net.Listener
	sshServer *SSHServer
	// ctx is the run context set by Start; cancel ends it. Long-lived
	// connection handlers (events.watch) select on ctx.Done so a
	// shutdown does not wait on a client that never hangs up. Nil until
	// Start, which is the case for tests that call handlers directly;
	// stopping() turns nil into a channel that never fires.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Path of the pid file to create on Start and remove on Stop.
	// Empty = no pid file.
	pidFile string

	// Advisory lock on the control socket path, held from Start to
	// shutdown. Nil for TCP listeners, which have their own kernel-level
	// exclusion.
	socketLock *socketLock

	// reclaim makes the startup reconcile destroy marvel-* tmux state
	// this daemon does not own, instead of leaving it running. Off by
	// default; `marvel daemon --reclaim` is the deliberate act.
	reclaim bool

	// home is the layout home this daemon is rooted at (~/.marvel),
	// stamped onto every response. Empty when the home directory cannot
	// be resolved, which is the same condition under which nothing else
	// in the layout works either; the field is then simply absent from
	// the wire and the client has nothing to compare.
	home string
	// bus keeps a managed local broker's rendered configuration current
	// with the applied teams (brief 10, aae-orc-e9g8i). Nil when this
	// daemon's cluster has no managed bus section.
	bus *bus.Manager
	// busSup supervises the managed broker as a child of this daemon; nil when
	// the cluster has no managed bus. keepBus asks shutdown to leave it running
	// for the next daemon to adopt (reexec, `marvel stop --keep-bus`).
	busSup  *bus.Supervisor
	keepBus bool

	// In-memory ring of the most recent log lines. Always non-nil.
	logs *logbuf.Buffer

	// In-memory ring of structured state-transition events.
	// Always non-nil.
	events *events.Ring

	// Heartbeat auth notices repeat at the offending process's tick rate
	// and never stop on their own, because nothing reaps an orphan. One
	// line per refusal buried a daemon log in a live incident
	// (aae-orc-k58k: 44 refusals in 90s from two orphans at a 2s tick).
	// Throttled per (kind, session) so the condition still announces
	// itself immediately and then stays quiet, reporting how many it
	// swallowed when it next speaks.
	hbAuthMu sync.Mutex
	hbAuth   map[string]*heartbeatAuthNotice

	// orphans accumulates stale-token heartbeat refusals into queryable
	// inventory: the positive, self-announcing orphan signal (aae-orc-m4of,
	// k58k). Always non-nil. Marvel reports orphans, it never kills them.
	orphans *orphanRegistry

	// Per-session context and token accountant, fed by the adapter
	// streams through the session manager. Always non-nil.
	usage *usage.Accountant

	// metricsWarn keeps a sampler that cannot read the process table
	// from writing the same line every interval for the life of the
	// daemon.
	metricsWarn sync.Once

	// reexec replaces the process image. Defaults to syscall.Exec; tests
	// override it to assert the pre-exec contract without actually
	// handing the process over.
	reexec func(argv0 string, argv []string, envv []string) error
}

// Options configures optional daemon behavior. Zero value disables
// everything optional (matches legacy behavior).
type Options struct {
	// PidFile, when non-empty, is written with the daemon's PID on
	// Start and removed on Stop. If the file already exists and
	// points at a live process, Start refuses with an error.
	PidFile string
	// LogBuffer, when non-nil, is the in-memory log ring the daemon
	// tees its log stream through. When nil, New allocates one at
	// DefaultLogBufferLines. Tests may pre-allocate to inspect.
	LogBuffer *logbuf.Buffer
	// Events, when non-nil, is the structured event ring. When nil,
	// New allocates one at events.DefaultCapacity. Tests may pre-
	// allocate to inspect emitted events.
	Events *events.Ring
	// StateBolt, when non-empty, is the path to the bbolt L2 file the
	// daemon uses for durable state. NewWithOptions calls Store.OpenBolt
	// at this path, rehydrating any existing records into memory. Empty
	// disables persistence (in-memory only) — daemon restart loses
	// state and AdoptOrKill degenerates to kill-all. The CLI defaults
	// this to layout.DaemonBolt() (~/.marvel/state/marvel.bolt).
	// See orc finding-050 / aae-orc-k4e4.
	StateBolt string
	// ShiftTimeout bounds how long a single shift may run before the team
	// controller declares it stuck, aborts it, and rolls back with a
	// team.shift-timed-out event. Zero keeps the controller's built-in
	// 10-minute default. Exposed so an operator can tune it (and demo the
	// timeout without a 10-minute wait) via the daemon's --shift-timeout
	// flag or MARVEL_SHIFT_TIMEOUT. See aae-orc-sape / ArcavenAE/marvel#88.
	ShiftTimeout time.Duration
	// Reclaim makes the startup reconcile pass destroy marvel-* tmux
	// state this daemon does not own, rather than leaving it running and
	// reporting it. The zero value is the ratified default (leave alone).
	// Exposed as `marvel daemon --reclaim` for the operator who knows the
	// host is theirs and wants it clean. See aae-orc-kvcs.
	Reclaim bool
	// ContextLimits, when non-nil, replaces the shipped model-to-window
	// table the usage accountant resolves denominators against. Tests set
	// it so a fixture's model does not have to be a real shipped entry.
	ContextLimits usage.Table
}

// New creates a new daemon with default options.
func New() (*Daemon, error) {
	return NewWithOptions(Options{})
}

// NewWithOptions creates a new daemon with the given options.
func NewWithOptions(opts Options) (*Daemon, error) {
	driver, err := tmux.NewDriver()
	if err != nil {
		return nil, fmt.Errorf("init tmux driver: %w", err)
	}

	store := api.NewStore()
	if opts.StateBolt != "" {
		if oerr := store.OpenBolt(opts.StateBolt); oerr != nil {
			return nil, fmt.Errorf("open state bolt at %s: %w", opts.StateBolt, oerr)
		}
		log.Printf("daemon state file: %s (resource_version=%d)", opts.StateBolt, store.ResourceVersion())
	}
	sessMgr := session.NewManager(store, driver)
	teamCtrl := team.NewController(store, sessMgr)
	// Zero leaves the controller on its built-in default (10 minutes);
	// a nonzero value from --shift-timeout / MARVEL_SHIFT_TIMEOUT overrides.
	teamCtrl.ShiftTimeout = opts.ShiftTimeout
	// Must follow OpenBolt: the controller reads its crash-loop state
	// out of the same bolt file. Before this, a role frozen at
	// MaxRestarts respawned on the first reconcile tick after restart.
	// See aae-orc-qdew.
	if rerr := teamCtrl.RehydrateRoleHealth(); rerr != nil {
		return nil, fmt.Errorf("rehydrate role health: %w", rerr)
	}

	buf := opts.LogBuffer
	if buf == nil {
		buf = logbuf.New(DefaultLogBufferLines)
	}

	evRing := opts.Events
	if evRing == nil {
		evRing = events.NewRing(events.DefaultCapacity)
	}
	sessMgr.Events = evRing
	teamCtrl.Events = evRing

	// The usage accountant is event-driven, so it needs no goroutine and
	// no interval of its own. *api.Store satisfies its Sink directly.
	limits := opts.ContextLimits
	if limits == nil {
		limits = usage.DefaultTable()
	}
	// One resolver, shared between the accountant and the heartbeat path,
	// so a window learned on either (Claude declares its own in-stream) is
	// visible to the other. The heartbeat path reaches it through the
	// store's injected ContextLimitResolveFunc, whose usage-free signature
	// keeps internal/api from importing internal/usage. See aae-orc-38yr.
	resolver := usage.NewResolver(limits)
	acct := usage.New(store, resolver, usage.WithEvents(evRing))
	sessMgr.Usage = acct
	store.SetContextLimitResolver(func(harness, model string, args []string, manifestLimit, feedLimit int, redirection api.BackendRedirection) (int, string) {
		limit, src, _ := resolver.Resolve(usage.Request{
			Harness:       harness,
			StreamModel:   model,
			RuntimeArgs:   args,
			ManifestLimit: manifestLimit,
			FeedLimit:     feedLimit,
			Redirection:   redirection,
		})
		return limit, string(src)
	})

	// Resolved once at construction rather than per response: the home
	// cannot change under a running daemon, and a failure here is not
	// worth refusing to start over. An empty home means the field is
	// absent on the wire.
	var home string
	if layout, lerr := paths.Default(); lerr == nil {
		home = layout.Home
	}

	d := &Daemon{
		store:    store,
		sessMgr:  sessMgr,
		teamCtrl: teamCtrl,
		driver:   driver,
		pidFile:  opts.PidFile,
		reclaim:  opts.Reclaim,
		home:     home,
		logs:     buf,
		events:   evRing,
		usage:    acct,
		orphans:  newOrphanRegistry(),
		reexec:   syscall.Exec,
	}
	// The controller evaluates count-shaped admission clauses on its own
	// (store counts, no meter). This seam is what lets InitiateShift also
	// evaluate a cumulative clause without internal/team importing
	// internal/usage. See aae-orc-qiay.
	teamCtrl.Snapshots = d
	return d, nil
}

// Usage returns the daemon's context and token accountant. Exported for
// tests and for the read-side consumers (admission control, shift
// triggers) that will hold it as a usage.Reader.
func (d *Daemon) Usage() *usage.Accountant { return d.usage }

// LogBuffer returns the daemon's in-memory log ring. Callers can
// hook it into log.SetOutput to tee stderr into the buffer; the
// daemon process does this in cmd/marvel.
func (d *Daemon) LogBuffer() *logbuf.Buffer { return d.logs }

// Start starts the daemon: listens on Unix or TCP socket and starts reconciliation.
// The address format determines the network: "host:port" for TCP, a file path
// for Unix. Examples: "~/.marvel/run/marvel.sock", "0.0.0.0:9090", ":9090".
func (d *Daemon) Start(socketPath string) error {
	// Refuse to start if a pid file already points at a live process.
	if d.pidFile != "" {
		if err := checkPidFileFree(d.pidFile); err != nil {
			return err
		}
	}

	network := listenNetwork(socketPath)

	if network == "unix" {
		if err := paths.CheckSocketPath(socketPath); err != nil {
			return err
		}
		// Take the lock BEFORE unlinking. Removing the socket
		// unconditionally is how a live daemon got left unreachable when
		// a second one exited on the same path; holding the lock means
		// the path we are about to unlink is nobody else's.
		lock, err := lockSocketPath(socketPath)
		if err != nil {
			return err
		}
		d.socketLock = lock
		_ = os.Remove(socketPath)
	}

	ln, err := net.Listen(network, socketPath)
	if err != nil {
		return fmt.Errorf("listen %s (%s): %w", socketPath, network, err)
	}
	d.listener = ln
	d.sessMgr.SocketPath = socketPath
	d.teamCtrl.SocketPath = socketPath

	if d.pidFile != "" {
		if err := writePidFile(d.pidFile); err != nil {
			_ = ln.Close()
			return err
		}
	}

	// The bus comes up after the pidfile guard and before the reconcile
	// loop: a daemon that lost the pidfile race must never reach another
	// daemon's broker, and no session may spawn before there is a bus to
	// hand it. A managed bus that cannot start is a start failure, the
	// tmux posture.
	if err := d.attachServices(socketPath); err != nil {
		_ = ln.Close()
		if d.pidFile != "" {
			_ = os.Remove(d.pidFile)
		}
		return err
	}

	// Reconcile marvel-* tmux state against recorded intent. Panes that
	// match the rehydrated intent are adopted; anything else is LEFT
	// RUNNING and reported, unless the operator asked to reclaim.
	//
	// Kill used to be the default here, and without L2 the store is
	// empty, so it degenerated to kill-all (ArcavenAE/marvel#13's
	// CleanupOrphanTmux fix, orc finding-050). That is what let an
	// ordinary `marvel daemon` destroy another daemon's entire running
	// fleet. Reversed 2026-08-07 per docs/design/daemon-isolation.md
	// decision 5: err on accumulation, not destruction.
	reconcile, what := d.sessMgr.AdoptOrLeave, "AdoptOrLeave"
	if d.reclaim {
		reconcile, what = d.sessMgr.AdoptOrKill, "AdoptOrKill"
	}
	if _, _, err := reconcile(); err != nil {
		log.Printf("%s on startup: %v", what, err)
	}

	// Decide the convergence posture from what adoption actually reclaimed,
	// then hold anything cold at the start line. RefreshLiveness reaps records
	// whose panes did not survive (a host reboot leaves stale Running rows), so
	// InitConvergencePosture reads true live presence: a team with surviving
	// panes converges (its fleet is running — maintain it), a team with none
	// holds until `marvel converge`. This runs BEFORE the reconcile loop so a
	// stale bolt can never cold-spawn a fleet on start. See aae-orc-cxdf/rwiw.
	d.teamCtrl.RefreshLiveness()
	if perr := d.teamCtrl.InitConvergencePosture(); perr != nil {
		log.Printf("init convergence posture: %v", perr)
	}
	d.logConvergencePosture()

	// Re-project policies for the sessions adoption just reclaimed. Until
	// aae-orc-d6sc, Reproject had exactly ONE caller — the manifest-apply
	// path below — so a daemon restart or `daemon reexec` left every adopted
	// agent reading whatever settings file it was given at spawn. Two
	// consequences, and the second is the one d6sc is about: an edited policy
	// did not reach a running agent until someone re-applied a manifest
	// (contradicting design principle 4), and a statusline hook baking a
	// version-pinned binary path was never refreshed after an upgrade.
	//
	// This runs AFTER RefreshLiveness so the store reflects true live
	// presence — Reproject skips sessions that are not alive, and a stale
	// Running row would otherwise waste a projection attempt.
	//
	// It narrows the d6sc orphan window rather than closing it: nothing here
	// helps between a package prune and the next daemon event. The stable
	// indirection that would close it is daemon-side and still reserved.
	if n := d.sessMgr.Reproject(); n > 0 {
		log.Printf("startup: re-projected policy for %d adopted session(s)", n)
	}

	// Announced from Start, not from the constructor. cmd/marvel installs
	// log.SetOutput (log ring plus the optional --log-file) only after
	// NewWithOptions returns, so a line written during construction reaches
	// bare stderr and neither `marvel daemon logs` nor the log file. This is
	// the one observability affordance for the in-memory token window, so it
	// has to land where the docs say it lands.
	d.logTokenBudgetWindows()

	ctx, cancel := context.WithCancel(context.Background())
	d.ctx, d.cancel = ctx, cancel

	// Start team reconciliation loop.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.teamCtrl.Run(ctx, ReconcileInterval)
	}()

	// Start the process sampler. Sibling of the reconcile loop rather
	// than a step inside it: sampling reads the process table and takes
	// no part in reconciliation, so it should not be able to slow a
	// reconcile pass down.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.RunMetrics(ctx, MetricsInterval)
	}()

	// Accept connections.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					log.Printf("accept: %v", err)
					continue
				}
			}
			go d.handleConn(conn)
		}
	}()

	log.Printf("marvel daemon listening on %s (%s)", socketPath, network)
	return nil
}

// StartMRVL starts the mrvl:// listener (embedded SSH server) alongside
// the Unix/TCP listener. The daemon generates a host key on first run
// and authenticates clients against ~/.marvel/authorized_keys.
// If addr has no port, defaults to 6785.
func (d *Daemon) StartMRVL(addr string) error {
	if addr == "" {
		addr = ":" + DefaultMRVLPort
	}
	if !strings.Contains(addr, ":") {
		addr = addr + ":" + DefaultMRVLPort
	}
	srv, err := newSSHServer(d)
	if err != nil {
		return fmt.Errorf("init ssh server: %w", err)
	}
	d.sshServer = srv
	return srv.Start(addr)
}

// Detach shuts the daemon down and leaves every agent it manages
// running. The reconciler and listeners stop, durable state is
// checkpointed and the bolt file released, and no tmux pane is touched,
// so the next daemon start rehydrates the same recorded intent and
// AdoptOrKill reclaims the live panes. This is the SIGINT/SIGTERM path
// and what plain `marvel stop` does.
//
// Detaching rather than tearing down is what makes the graceful path as
// recoverable as an ungraceful one: before this split, Stop deleted
// every session and killed every pane before closing bolt, so adoption
// could only ever fire after a kill -9. See aae-orc-1aoe.
//
// Detach is also the first half of in-place self-update
// (aae-orc-zk5r): checkpoint, release the bolt file, then syscall.Exec
// the replacement binary, which comes back up and adopts. Nothing here
// assumes the process is about to exit.
func (d *Daemon) Detach() { d.shutdown(false) }

// Stop shuts the daemon down and destroys what it manages: every
// workspace's sessions are deleted and its tmux session killed before
// durable state is closed. This is `marvel stop --teardown`, for the
// operator who wants the machine clean, and the path tests use.
func (d *Daemon) Stop() { d.shutdown(true) }

// Checkpoint flushes durable state to disk without stopping anything.
// The seam for a self-update that execs without a full Detach
// (aae-orc-zk5r). No-op when persistence is disabled.
func (d *Daemon) Checkpoint() error { return d.store.Checkpoint() }

// Reexec replaces the running daemon's process image with a fresh exec
// of the marvel binary at its current path, argv and environment
// preserved, without stopping any managed agent. It detaches first
// (the reconciler and listeners stop, durable state is checkpointed,
// the bolt file is released, every pane is left running), then execs.
// The successor process re-opens the same bolt, re-binds the same
// socket, and adopts the live panes via AdoptOrKill.
//
// This is the in-place self-update path (aae-orc-zk5r). It composes with
// `marvel upgrade`, which fetches and installs the new binary on disk:
// upgrade replaces the bytes, Reexec adopts them. The two stay distinct
// verbs so "install a new binary" and "adopt it without dropping agents"
// never fight over one name.
//
// The binary path resolves before the detach, so a resolution failure
// leaves the daemon serving. On success syscall.Exec does not return.
// Reexec returns an error only if the exec syscall itself fails, which
// happens after the detach: the daemon has stopped serving but its
// agents survive, and a fresh daemon start adopts them.
func (d *Daemon) Reexec() error {
	exe, err := selfExecPath()
	if err != nil {
		return err
	}
	// Detach checkpoints durable state, releases the bolt file, and stops
	// serving without touching a pane. Its own doc names this as the first
	// half of self-update. The broker stays up too: the successor adopts it
	// by pidfile and listener, and the agents' shims never see a drop.
	d.keepBus = true
	d.Detach()
	return d.reexec(exe, os.Args, os.Environ())
}

// selfExecPath resolves the path to the running marvel binary for
// re-exec. Linux reports /proc/self/exe; after `marvel upgrade` replaces
// the binary in place the kernel appends " (deleted)" to the old inode's
// symlink target, so trim it; the replacement lives at the cleaned path.
func selfExecPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve marvel binary path: %w", err)
	}
	return cleanExecPath(exe), nil
}

// cleanExecPath strips the " (deleted)" suffix the Linux kernel appends
// to /proc/self/exe once the running binary's inode is replaced. The
// replacement binary lives at the cleaned path.
func cleanExecPath(p string) string {
	return strings.TrimSuffix(p, " (deleted)")
}

func (d *Daemon) shutdown(teardown bool) {
	// Transient credentials live only in memory, so a detach, reexec, or
	// teardown drops them (brief 9 S4). The successor starts empty and cannot
	// know what was lost, so name it here, in the departing process that still
	// holds them. The log line is durable; the event serves a live watcher.
	if n := len(d.store.ListCredentials()); n > 0 {
		msg := fmt.Sprintf("%d transient credential(s) dropped; push them again once the daemon is back", n)
		events.Emit(d.events, events.Event{
			Kind:     events.KindCredentialTransientDropped,
			Severity: events.SeverityWarning,
			Message:  msg,
		})
		log.Printf("%s: %s", events.KindCredentialTransientDropped, msg)
	}

	if d.cancel != nil {
		d.cancel()
	}

	if d.sshServer != nil {
		d.sshServer.Stop()
	}

	addr := ""
	if d.listener != nil {
		addr = d.listener.Addr().String()
		_ = d.listener.Close()
	}
	d.wg.Wait()

	if teardown {
		for _, ws := range d.store.ListWorkspaces() {
			if err := d.sessMgr.CleanupWorkspace(ws.Name); err != nil {
				log.Printf("cleanup workspace %s: %v", ws.Name, err)
			}
		}
	}

	// The broker's lifetime is the daemon's unless the departing daemon is
	// handing over (reexec, --keep-bus). Teardown always takes it down.
	// After wg.Wait so no reconcile tick can spawn against a bus that is
	// on its way out.
	if d.busSup != nil {
		d.busSup.Stop(d.keepBus && !teardown)
	}

	// Flush before releasing the file handle: on the detach path the
	// records written here are exactly what the next start adopts from.
	// Both calls are no-ops when persistence is disabled.
	if err := d.store.Checkpoint(); err != nil {
		log.Printf("checkpoint state: %v", err)
	}
	if err := d.store.CloseBolt(); err != nil {
		log.Printf("close bolt: %v", err)
	}

	// Only remove socket file for Unix sockets. Unlink before releasing
	// the lock, so no other daemon can bind the path in between and have
	// this one delete its socket.
	if addr != "" && listenNetwork(addr) == "unix" {
		_ = os.Remove(addr)
	}
	d.socketLock.release()
	d.socketLock = nil
	if d.pidFile != "" {
		_ = os.Remove(d.pidFile)
	}
	if teardown {
		log.Println("marvel daemon stopped, agents torn down")
		return
	}
	log.Printf("marvel daemon detached, %d session(s) left running", len(d.store.ListSessions()))
}

// writePidFile creates/overwrites pidfile with the current PID.
func writePidFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), paths.ModeDir); err != nil {
		return fmt.Errorf("create pidfile dir: %w", err)
	}
	data := []byte(fmt.Sprintf("%d\n", os.Getpid()))
	if err := os.WriteFile(path, data, paths.ModeKnownHosts); err != nil {
		return fmt.Errorf("write pidfile %s: %w", path, err)
	}
	return nil
}

// checkPidFileFree refuses to start if pidfile names a running process.
// Stale pidfiles (process no longer exists) are quietly replaced.
func checkPidFileFree(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read pidfile %s: %w", path, err)
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		// Corrupt pidfile — treat as stale.
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	// Signal 0 is the "is it alive" check on Unix.
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return fmt.Errorf("pidfile %s names live process %d — another daemon already running", path, pid)
	}
	return nil
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	// The local unix socket is the daemon owner, so it is admin.
	d.handleRWCAs(conn, localCaller())
}

// handleRWC processes one request on rwc as the local admin caller. The SSH
// transport uses handleRWCAs with the authenticated key's scope instead.
func (d *Daemon) handleRWC(rwc io.ReadWriteCloser) {
	d.handleRWCAs(rwc, localCaller())
}

// handleRWCAs processes a single JSON-RPC request/response on any
// io.ReadWriteCloser as the given caller. Both the Unix socket (local admin)
// and SSH channels (the authenticated key's scope) reach it.
func (d *Daemon) handleRWCAs(rwc io.ReadWriteCloser, c caller) {
	defer func() { _ = rwc.Close() }()

	var req Request
	if err := json.NewDecoder(rwc).Decode(&req); err != nil {
		resp := Response{Error: fmt.Sprintf("decode request: %v", err)}
		_ = json.NewEncoder(rwc).Encode(d.stamp(resp))
		return
	}

	// events.watch is the one method that answers with many responses
	// on the same connection, so it is routed here, where the handler
	// can keep rwc, instead of through dispatchAs, which returns one
	// Response. Scope is enforced the same way first.
	if req.Method == MethodEventsWatch {
		if resp, refused := d.scopeRefusal(req, c); refused {
			_ = json.NewEncoder(rwc).Encode(d.stamp(resp))
			return
		}
		d.handleEventsWatch(rwc, req.Params)
		return
	}

	resp := d.dispatchAs(req, c)
	_ = json.NewEncoder(rwc).Encode(d.stamp(resp))
}

// stamp records which daemon answered. It sits on the write path rather
// than in the 14 handlers so no method can be added without it, and it
// covers the decode-error reply too: a malformed request answered by the
// wrong daemon is worth attributing as much as a successful one.
func (d *Daemon) stamp(resp Response) Response {
	resp.DaemonHome = d.home
	return resp
}

// dispatch routes a request as the local admin caller. The local socket path
// and the daemon's own tests use it; the SSH transport uses dispatchAs with
// the authenticated key's scope.
func (d *Daemon) dispatch(req Request) Response {
	return d.dispatchAs(req, localCaller())
}

// scopeRefusal applies the caller's scope to a method. The returned
// Response is the refusal to send when refused is true. Shared by
// dispatchAs and the streaming path so no method can reach a handler
// without passing it.
func (d *Daemon) scopeRefusal(req Request, c caller) (Response, bool) {
	if !methodAllowedForScope(req.Method, c.scope) {
		who := c.fingerprint
		if who == "" {
			who = "local"
		}
		log.Printf("scope refused: method %q not permitted for %s key %s", req.Method, c.scope, who)
		return Response{Error: fmt.Sprintf("method %q is not permitted for a %s key", req.Method, c.scope)}, true
	}
	if methodRequiresLocal(req.Method) && !c.local {
		log.Printf("local-only refused: method %q attempted over mrvl:// by key %s", req.Method, c.fingerprint)
		return Response{Error: fmt.Sprintf("method %q is only available on the local unix socket", req.Method)}, true
	}
	return Response{}, false
}

// dispatchAs enforces the caller's scope, then routes the request. A method
// the scope does not permit is refused before any handler runs.
func (d *Daemon) dispatchAs(req Request, c caller) Response {
	if resp, refused := d.scopeRefusal(req, c); refused {
		return resp
	}
	switch req.Method {
	case "apply":
		return d.handleApply(req.Params)
	case "get":
		return d.handleGet(req.Params)
	case "describe":
		return d.handleDescribe(req.Params)
	case "delete":
		return d.handleDelete(req.Params)
	case "scale":
		return d.handleScale(req.Params)
	case "converge":
		return d.handleConverge(req.Params)
	case "reap":
		return d.handleReap(req.Params)
	case "heartbeat":
		return d.handleHeartbeat(req.Params)
	case "run":
		return d.handleRun(req.Params)
	case "shift":
		return d.handleShift(req.Params)
	case "reset-health":
		return d.handleResetHealth(req.Params)
	case "inject":
		return d.handleInject(req.Params)
	case "capture":
		return d.handleCapture(req.Params)
	case "credential.put":
		return d.handleCredentialPut(req.Params, c)
	case "credential.get":
		return d.handleCredentialGet(req.Params)
	case "credential.reveal":
		return d.handleCredentialReveal(req.Params, c)
	case "backend.verify":
		return d.handleBackendVerify(req.Params)
	case "bus.status":
		return d.handleBusStatus()
	case "bus.leaf.connect":
		return d.handleBusLeafConnect()
	case "bus.leaf.disconnect":
		return d.handleBusLeafDisconnect()
	case "credential.list":
		return d.handleCredentialList()
	case "credential.delete":
		return d.handleCredentialDelete(req.Params, c)
	case "stop":
		return d.handleStop(req.Params)
	case "reexec":
		return d.handleReexec()
	case "logs":
		return d.handleLogs(req.Params)
	case "events":
		return d.handleEvents(req.Params)
	case MethodEventsWatch:
		// Reachable only through dispatch(), which tests and the SSH
		// path do not use for this method; handleRWCAs routes it to the
		// streaming handler before dispatch.
		return Response{Error: fmt.Sprintf("%s streams many responses on one connection; use daemon.WatchEventsWith", MethodEventsWatch)}
	case "orphans":
		return d.handleOrphans()
	case "plan":
		return d.handlePlan()
	default:
		return Response{Error: fmt.Sprintf("unknown method: %s", req.Method)}
	}
}

// Logs params — tail of the daemon's in-memory log ring.
type logsParams struct {
	N int `json:"n"` // number of lines; 0 or negative = unbounded (whole buffer)
}

type logsResult struct {
	Lines []string `json:"lines"`
}

func (d *Daemon) handleLogs(params json.RawMessage) Response {
	var p logsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return Response{Error: fmt.Sprintf("bad params: %v", err)}
		}
	}
	if p.N <= 0 {
		p.N = d.logs.Cap()
	}
	lines := d.logs.Tail(p.N)
	data, err := json.Marshal(logsResult{Lines: lines})
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal logs: %v", err)}
	}
	return Response{Result: data}
}

// Events params — filtered tail of the daemon's structured event ring.
type eventsParams struct {
	N           int    `json:"n"` // number of events; <=0 returns the whole ring
	Workspace   string `json:"workspace,omitempty"`
	Team        string `json:"team,omitempty"`
	Role        string `json:"role,omitempty"`
	Session     string `json:"session,omitempty"`
	Kind        string `json:"kind,omitempty"`
	MinSeverity string `json:"min_severity,omitempty"` // "" or "warning"
	// SinceSeq returns only events with Seq strictly greater than this
	// value — the follow-mode resume cursor. Zero means no cursor.
	SinceSeq uint64 `json:"since_seq,omitempty"`
}

// EventsBatch is the result of the events method and one message on an
// events.watch stream. Dropped and ResumeFrom are set only on a stream,
// and only when the daemon-side watch overflowed since the last message:
// Dropped events were never sent, and Snapshot with since_seq =
// ResumeFrom returns them, subject to the ring's capacity.
type EventsBatch struct {
	Events     []events.Event `json:"events"`
	Dropped    uint64         `json:"dropped,omitempty"`
	ResumeFrom uint64         `json:"resume_from,omitempty"`
}

func (p eventsParams) filter() events.Filter {
	return events.Filter{
		Workspace:   p.Workspace,
		Team:        p.Team,
		Role:        p.Role,
		Session:     p.Session,
		Kind:        events.Kind(p.Kind),
		MinSeverity: events.Severity(p.MinSeverity),
		SinceSeq:    p.SinceSeq,
	}
}

func (d *Daemon) handleEvents(params json.RawMessage) Response {
	var p eventsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return Response{Error: fmt.Sprintf("bad params: %v", err)}
		}
	}
	snap := d.events.Snapshot(p.filter(), p.N)
	data, err := json.Marshal(EventsBatch{Events: snap})
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal events: %v", err)}
	}
	return Response{Result: data}
}

// MethodEventsWatch is the streaming sibling of the events method. The
// request carries the same params; the daemon answers with one
// EventsBatch for the backlog (events after since_seq, or the last n
// when there is no cursor) and then one per delivery for the life of
// the connection. The client ends the stream by closing its side.
const MethodEventsWatch = "events.watch"

// eventsWatchKeepalive bounds how long a dead peer holds a watch open
// when no event arrives to expose the broken write. Each tick sends an
// empty batch, which clients ignore.
const eventsWatchKeepalive = 30 * time.Second

// eventsWatchBatchMax caps how many queued events one stream message
// carries, so a burst is sent as a few messages rather than one large
// one and a slow encoder does not starve the keepalive.
const eventsWatchBatchMax = 256

// stopping returns the run context's done channel, or a channel that
// never fires when Start has not run.
func (d *Daemon) stopping() <-chan struct{} {
	if d.ctx == nil {
		return nil
	}
	return d.ctx.Done()
}

// handleEventsWatch is the daemon side of `marvel events --follow`: the
// first real subscriber of the internal bus (docs/design/internal-bus.md
// section 3, aae-orc-5ltr3). It holds a Watch for the connection's life
// and writes each delivery as it arrives. The Watch is registered
// before the backlog Snapshot so the two are contiguous; deliveries the
// snapshot already covered are skipped by Seq.
func (d *Daemon) handleEventsWatch(rwc io.ReadWriteCloser, params json.RawMessage) {
	enc := json.NewEncoder(rwc)
	var p eventsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			_ = enc.Encode(d.stamp(Response{Error: fmt.Sprintf("bad params: %v", err)}))
			return
		}
	}
	f := p.filter()
	w := d.events.Watch(f, 0)
	defer w.Close()

	send := func(b EventsBatch) bool {
		data, err := json.Marshal(b)
		if err != nil {
			_ = enc.Encode(d.stamp(Response{Error: fmt.Sprintf("marshal events: %v", err)}))
			return false
		}
		return enc.Encode(d.stamp(Response{Result: data})) == nil
	}

	backlog := d.events.Snapshot(f, p.N)
	var last uint64
	if n := len(backlog); n > 0 {
		last = backlog[n-1].Seq
	}
	if !send(EventsBatch{Events: backlog}) {
		return
	}

	// The request is the only thing the client ever writes, so a read
	// that returns is the client hanging up. This is what ends the
	// stream on the unix socket and on an SSH channel alike.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, rwc)
	}()

	keepalive := time.NewTicker(eventsWatchKeepalive)
	defer keepalive.Stop()
	for {
		var batch []events.Event
		select {
		case <-gone:
			return
		case <-d.stopping():
			return
		case <-keepalive.C:
		case ev := <-w.Events():
			batch = append(batch, ev)
		drain:
			for len(batch) < eventsWatchBatchMax {
				select {
				case ev := <-w.Events():
					batch = append(batch, ev)
				default:
					break drain
				}
			}
		}
		out := EventsBatch{}
		for _, ev := range batch {
			if ev.Seq <= last {
				continue
			}
			out.Events = append(out.Events, ev)
			last = ev.Seq
		}
		out.Dropped, out.ResumeFrom = w.Gap()
		if !send(out) {
			return
		}
	}
}

// Apply params
type applyParams struct {
	ManifestData []byte `json:"manifest_data"`
}

func (d *Daemon) handleApply(params json.RawMessage) Response {
	var p applyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	m, err := api.ParseManifestBytes(p.ManifestData)
	if err != nil {
		// ParseManifestBytes already prefixes its errors ("parse manifest:"
		// for a validation failure, "parse yaml/toml manifest:" for a syntax
		// one), so return it as-is. Re-wrapping with "parse manifest: %v"
		// doubled the prefix. Matches the ValidateRuntimes/ValidateBudgets
		// pre-flight siblings below, which also surface err.Error() directly.
		return Response{Error: err.Error()}
	}

	// Pre-flight: refuse to apply if any role's runtime command/script
	// isn't resolvable. See ArcavenAE/marvel#9 — without this a missing
	// binary produced no diagnostic, just a silent pane that exited
	// immediately and entered the restart loop.
	if err := m.ValidateRuntimes(); err != nil {
		return Response{Error: err.Error()}
	}

	// Pre-flight: refuse a declared dimension no role in the team can ever
	// report, so a mute gate is an error rather than a silent no-op. The
	// capability predicate comes from the session manager's adapter
	// registry: mode alone does not answer it, since a generic role can
	// declare headless and still have no stream path.
	if err := m.ValidateBudgets(d.sessMgr.CanStreamRole); err != nil {
		return Response{Error: err.Error()}
	}

	// Admission, before Apply commits anything. Refusing the declaration is
	// the whole design: gating only the spawn would leave a permanently
	// unsatisfiable desired state, a teams table reporting replicas that
	// will never exist, and a reconciler re-deciding the same impossible
	// deficit every tick. See aae-orc-qiay.
	for _, mt := range m.Teams {
		b := mt.Budget.Budget()
		if !b.Declared() {
			continue
		}
		t := api.Team{Name: mt.Name, Workspace: m.Workspace.Name, Budget: b}
		for _, r := range mt.Roles {
			t.Roles = append(t.Roles, api.Role{Name: r.Name, Replicas: r.Replicas})
		}
		live := api.CountAlive(d.store.ListSessionsByTeam(t.Workspace, t.Name))
		declared := 0
		for i := range t.Roles {
			declared += t.Roles[i].Replicas
		}
		if resp := d.admitGrowth(t, "", declared-live, admission.TriggerApply); resp != nil {
			return *resp
		}
	}

	if err := m.Apply(d.store); err != nil {
		return Response{Error: fmt.Sprintf("apply manifest: %v", err)}
	}

	// Apply is an explicit "make it so": the operator named these teams and
	// their replica counts, so they converge. This is distinct from a daemon
	// start rehydrating stale desired state (aae-orc-cxdf), which holds at the
	// start line. Setting the posture here — not relying on the store default —
	// keeps apply's spawn-on-apply behavior while the start path stays held.
	for _, mt := range m.Teams {
		teamKey := m.Workspace.Name + "/" + mt.Name
		if err := d.store.UpdateTeam(teamKey, func(live *api.Team) error {
			live.ConvergencePosture = api.PostureConverge
			return nil
		}); err != nil {
			log.Printf("apply: set convergence posture for %s: %v", teamKey, err)
		}
	}

	// Re-project policies for already-running sessions before reconciling.
	// A policy edited in this manifest reconciles by rewriting the session's
	// settings file (finding-024 contract half); Claude Code's file watcher
	// hot-reloads it with no restart. New sessions spawned by the reconcile
	// below get their projection at spawn time.
	if n := d.sessMgr.Reproject(); n > 0 {
		log.Printf("apply: re-projected policy for %d running session(s)", n)
	}
	// A newly applied team needs its broker user before its sessions spawn.
	d.regenerateBus("apply")

	// Trigger immediate reconciliation.
	d.teamCtrl.ReconcileOnce()

	result, _ := json.Marshal(map[string]string{
		"status":    "applied",
		"workspace": m.Workspace.Name,
	})
	return Response{Result: result}
}

// Get params
type getParams struct {
	ResourceType string `json:"resource_type"`
}

func (d *Daemon) handleGet(params json.RawMessage) Response {
	var p getParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	var result any
	switch p.ResourceType {
	case "sessions", "session":
		// Join the two truths marvel keeps about a role: live session rows
		// plus synthetic rows for any declared role held down with no live
		// session (crash-looping in its backoff window). Without the join a
		// restart_policy=always role is absent from the table for the whole
		// backoff window rather than shown as held. See aae-orc-prhx.
		// Annotate-and-synthesize (aae-orc-kj5bq): held roles with no live
		// row are synthesized; roles that kept a terminal row are annotated
		// in place so "done trying" reads differently from "coming back".
		// ListSessions returns value snapshots, so this mutates copies and
		// never the store — Reason stays out of persisted state.
		//
		// team.listingFor in controller_test.go mirrors this join so tests
		// can assert on what `get sessions` actually shows. Keep them in
		// step: if this moves, those tests keep passing while the surface
		// regresses.
		held, reasons := d.teamCtrl.ProjectHeldRoleRows(d.store)
		live := d.store.ListSessions()
		for i := range live {
			if r, ok := reasons[live[i].Key()]; ok && live[i].Reason == "" {
				live[i].Reason = r
			}
		}
		result = append(live, held...)
	case "teams", "team":
		result = d.store.ListTeams()
	case "workspaces", "workspace":
		result = d.store.ListWorkspaces()
	case "endpoints", "endpoint":
		result = d.store.ListEndpoints()
	case "policies", "policy":
		result = d.store.ListPolicies()
	case "credentials", "credential":
		result = d.store.ListCredentials()
	case "budgets", "budget":
		result = d.budgetRows()
	default:
		return Response{Error: fmt.Sprintf("unknown resource type: %s", p.ResourceType)}
	}

	data, err := json.Marshal(result)
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal result: %v", err)}
	}
	return Response{Result: data}
}

// planResult carries the convergence preview returned by the plan RPC: the
// RolePlans the next reconcile tick would enact, computed without enacting
// anything. Named-struct result, like logsResult and eventsResult, so the
// wire shape is self-describing and can gain aggregate fields later without
// breaking the CLI decode.
type planResult struct {
	Plans []team.RolePlan `json:"plans"`
}

// handlePlan returns the steady-state convergence plan for every team not
// mid-shift — the read-only preview surface for `marvel plan` (aae-orc-nrk1).
// It calls Controller.PlanConvergence, which is pure: no session is spawned
// or deleted and no store record is written, so this RPC never changes
// cluster state. Teams mid-shift are omitted by PlanConvergence; see its doc
// comment for the two fidelity limits a consumer must account for.
func (d *Daemon) handlePlan() Response {
	data, err := json.Marshal(planResult{Plans: d.teamCtrl.PlanConvergence()})
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal plan: %v", err)}
	}
	return Response{Result: data}
}

// Describe params
type describeParams struct {
	ResourceType string `json:"resource_type"`
	Name         string `json:"name"`
}

func (d *Daemon) handleDescribe(params json.RawMessage) Response {
	var p describeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	var result any
	var err error
	switch p.ResourceType {
	case "session":
		result, err = d.store.GetSession(p.Name)
	case "team":
		result, err = d.store.GetTeam(p.Name)
	case "workspace":
		result, err = d.store.GetWorkspace(p.Name)
	case "endpoint":
		result, err = d.store.GetEndpoint(p.Name)
	case "credential":
		// GetCredential returns metadata only; the Value is json:"-" and
		// never crosses the wire. Reveal is a separate local-socket path (S3).
		result, err = d.store.GetCredential(p.Name)
	default:
		return Response{Error: fmt.Sprintf("unknown resource type: %s", p.ResourceType)}
	}

	if err != nil {
		return Response{Error: err.Error()}
	}

	data, _ := json.Marshal(result)
	return Response{Result: data}
}

// Delete params
type deleteParams struct {
	ResourceType string `json:"resource_type"`
	Name         string `json:"name"`
}

func (d *Daemon) handleDelete(params json.RawMessage) Response {
	var p deleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	var err error
	switch p.ResourceType {
	case "session":
		err = d.sessMgr.Delete(p.Name)
	case "team":
		// Delete team and its sessions.
		t, getErr := d.store.GetTeam(p.Name)
		if getErr != nil {
			return Response{Error: getErr.Error()}
		}
		sessions := d.store.ListSessionsByTeam(t.Workspace, t.Name)
		for _, s := range sessions {
			_ = d.sessMgr.Delete(s.Key())
		}
		err = d.store.DeleteTeam(p.Name)
		// Clear accumulated crash-loop state for this team's roles so a
		// subsequent re-apply starts fresh. See ArcavenAE/marvel#29.
		d.teamCtrl.ClearRoleHealthForTeam(t.Workspace, t.Name)
		// The team's broker user goes with it; rewrite and reload is revocation.
		d.regenerateBus("delete team " + p.Name)
	case "workspace":
		ws, getErr := d.store.GetWorkspace(p.Name)
		if getErr != nil {
			return Response{Error: getErr.Error()}
		}
		// Cascade: delete teams in this workspace (and their sessions) so the
		// reconciler doesn't respawn sessions against orphaned team records.
		for _, t := range d.store.ListTeams() {
			if t.Workspace != ws.Name {
				continue
			}
			for _, s := range d.store.ListSessionsByTeam(t.Workspace, t.Name) {
				_ = d.sessMgr.Delete(s.Key())
			}
			if delErr := d.store.DeleteTeam(t.Key()); delErr != nil {
				log.Printf("delete workspace %s: delete team %s: %v", ws.Name, t.Key(), delErr)
			}
		}
		_ = d.sessMgr.CleanupWorkspace(ws.Name)
		err = d.store.DeleteWorkspace(p.Name)
		// Clear accumulated crash-loop state for every role under every
		// team in this workspace. See ArcavenAE/marvel#29.
		d.teamCtrl.ClearRoleHealthForWorkspace(ws.Name)
	default:
		return Response{Error: fmt.Sprintf("unknown resource type: %s", p.ResourceType)}
	}

	if err != nil {
		return Response{Error: err.Error()}
	}

	result, _ := json.Marshal(map[string]string{"status": "deleted"})
	return Response{Result: result}
}

// Scale params
type scaleParams struct {
	TeamKey  string `json:"team_key"`
	Role     string `json:"role"`
	Replicas int    `json:"replicas"`
}

func (d *Daemon) handleScale(params json.RawMessage) Response {
	var p scaleParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	t, err := d.store.GetTeam(p.TeamKey)
	if err != nil {
		return Response{Error: err.Error()}
	}

	if t.Shift.Phase != "" {
		return Response{Error: fmt.Sprintf("team %s: shift in progress, cannot scale", p.TeamKey)}
	}

	if p.Role == "" {
		var names []string
		for _, r := range t.Roles {
			names = append(names, r.Name)
		}
		return Response{Error: fmt.Sprintf("role is required; available roles: %v", names)}
	}

	// Role existence is checked BEFORE the budget gate and before the
	// mutation. It used to be checked after UpdateTeam, which was harmless
	// while nothing else could refuse; with a budget gate in front of the
	// mutation, a mistyped role name would otherwise report a budget error
	// instead of "role not found". The scan reads the snapshot GetTeam
	// already returned.
	old := -1
	for _, r := range t.Roles {
		if r.Name == p.Role {
			old = r.Replicas
			break
		}
	}
	if old < 0 {
		return Response{Error: fmt.Sprintf("role %s not found in team %s", p.Role, p.TeamKey)}
	}

	// A scale-down adds nothing and is never refused: shedding sessions is
	// how an operator frees headroom.
	if resp := d.admitGrowth(t, p.Role, p.Replicas-old, admission.TriggerScale); resp != nil {
		return *resp
	}

	// Then the declaration clause, which the spawn gate above cannot see:
	// it compares LIVE sessions, and live can sit below declared (a crashed
	// replica, a role in backoff). Second rather than first because when
	// both hold, the spawn gate's message is the more specific one; this
	// gate exists for the window where only it can refuse. See
	// admitDeclaration.
	if resp := d.admitDeclaration(t, p.Role, p.Replicas, old); resp != nil {
		return *resp
	}

	// Commit the replica change to the live team under the store lock.
	// Pre-fix, this mutated a pointer returned by GetTeam — which used
	// to alias store state. Now GetTeam returns a snapshot, so scaling
	// must go through UpdateTeam. See orc finding-032.
	if err := d.store.UpdateTeam(p.TeamKey, func(live *api.Team) error {
		for i := range live.Roles {
			if live.Roles[i].Name == p.Role {
				live.Roles[i].Replicas = p.Replicas
				return nil
			}
		}
		return nil
	}); err != nil {
		return Response{Error: err.Error()}
	}

	d.teamCtrl.ReconcileOnce()

	result, _ := json.Marshal(map[string]any{
		"status":   "scaled",
		"team":     p.TeamKey,
		"role":     p.Role,
		"replicas": p.Replicas,
	})
	return Response{Result: result}
}

// convergeParams selects which team(s) to move and which way. An empty TeamKey
// targets every team (the daemon-wide go-line). Hold flips the posture the
// other way — back to the start line — for a future majordomo or an operator
// re-arming a team; the CLI exposes only the converge direction.
type convergeParams struct {
	TeamKey string `json:"team_key,omitempty"`
	Hold    bool   `json:"hold,omitempty"`
}

// convergeRoleReport is one role's line in the converge result: what the
// reconcile the daemon is about to drive will do for it.
type convergeRoleReport struct {
	Team    string `json:"team"`
	Role    string `json:"role"`
	Action  string `json:"action"`
	Spawn   int    `json:"spawn,omitempty"`
	Desired int    `json:"desired"`
	Actual  int    `json:"actual"`
}

type convergeResult struct {
	Posture string               `json:"posture"`
	Teams   []string             `json:"teams"`
	Roles   []convergeRoleReport `json:"roles"`
}

// handleConverge sets the convergence posture (the aae-orc-rwiw go-line) and
// then reconciles once so the change takes effect immediately. The posture flip
// lives in the team controller (SetConvergencePosture), not here, so the same
// control-plane lever is available to a future in-process majordomo; the CLI
// `marvel converge` is one client of this RPC.
func (d *Daemon) handleConverge(params json.RawMessage) Response {
	var p convergeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return Response{Error: fmt.Sprintf("bad params: %v", err)}
		}
	}

	posture := api.PostureConverge
	if p.Hold {
		posture = api.PostureHold
	}

	plans, err := d.teamCtrl.SetConvergencePosture(p.TeamKey, posture)
	if err != nil {
		return Response{Error: err.Error()}
	}

	// Enact the new posture. On a converge this spawns toward desired; on a
	// hold it changes nothing this tick (the gate withholds cold spawns) but is
	// recorded for the next reconcile and for `describe`.
	d.teamCtrl.ReconcileOnce()

	res := convergeResult{Posture: string(posture)}
	seen := make(map[string]struct{})
	for _, pl := range plans {
		key := pl.Workspace + "/" + pl.Team
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			res.Teams = append(res.Teams, key)
		}
		res.Roles = append(res.Roles, convergeRoleReport{
			Team:    key,
			Role:    pl.Role,
			Action:  string(pl.Action),
			Spawn:   pl.Spawn,
			Desired: pl.Desired,
			Actual:  pl.Actual,
		})
	}

	out, _ := json.Marshal(res)
	return Response{Result: out}
}

// logConvergencePosture writes a one-line-per-team start summary of which teams
// are held at the start line with cold work waiting, so an operator reading the
// daemon log after start knows a `marvel converge` is available and what it
// would spawn. Silent when nothing is held. Called once from Start after the
// posture is initialized.
func (d *Daemon) logConvergencePosture() {
	plans := d.teamCtrl.PlanConvergence()
	held := make(map[string]int) // team key -> sessions a converge would spawn
	for _, pl := range plans {
		if pl.Action != team.RoleSpawn {
			continue
		}
		t, err := d.store.GetTeam(pl.Workspace + "/" + pl.Team)
		if err != nil || t.Posture() != api.PostureHold {
			continue
		}
		held[t.Key()] += pl.Spawn
	}
	for key, n := range held {
		log.Printf("convergence: team %s held at the start line; `marvel converge %s` would spawn %d session(s)",
			key, key, n)
	}
}

// heartbeatParams is an alias, not a copy: producers build the same type
// through api.NewHeartbeatRequest, so the wire contract has one
// definition and a field rename breaks every call site at compile time
// rather than one of them at runtime. The alias keeps the local spelling
// used throughout this file and its tests.
//
// The previous arrangement was a struct here and a map literal at each
// producer, which is how a forwarder came to omit SessionToken silently.
// See api.HeartbeatRequest and finding-023.
type heartbeatParams = api.HeartbeatRequest

func (d *Daemon) handleHeartbeat(params json.RawMessage) Response {
	var p heartbeatParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	auth, err := d.store.UpdateSessionHeartbeat(p)
	if err != nil {
		if errors.Is(err, api.ErrHeartbeatUnauthorized) {
			// Two different faults reach this branch and they have
			// opposite remedies, so they get different messages and
			// throttle independently. Presenting NO token is a producer
			// that was not updated to send one (the codex reader shipped
			// exactly that, see #176). Presenting a WRONG token is a live
			// process holding a credential minted for an earlier
			// incarnation of this session key: an orphan from a previous
			// daemon, which nothing reaps (aae-orc-k58k).
			cause, msg := "no-token", "refused: this heartbeat presented no token while the session "+
				"carries one, so its producer is not sending MARVEL_HEARTBEAT_TOKEN"
			if p.SessionToken != "" {
				cause, msg = "stale-token", "refused: token does not match the session claimed; an "+
					"orphaned agent from an earlier daemon is still heartbeating for this key. "+
					"See 'marvel orphans'. Clear it with 'marvel reap --confirm', or start with "+
					"'marvel daemon --reclaim'"
				// A stale-token refusal is a positive orphan sighting: record
				// it so the condition is queryable, not only a throttled log
				// line that ages out of the ring (aae-orc-m4of).
				d.orphans.observe(p.SessionKey, time.Now())
			}
			d.emitHeartbeatAuth2(events.KindHeartbeatRefused, cause, p.SessionKey, msg)
		}
		return Response{Error: err.Error()}
	}
	if auth == api.HeartbeatAuthUnbound {
		d.emitHeartbeatAuth(events.KindHeartbeatUnbound, p.SessionKey,
			"admitted unbound: session record carries no token, restart it to bind its heartbeat")
	}

	result, _ := json.Marshal(map[string]string{"status": "ok"})
	return Response{Result: result}
}

// emitHeartbeatAuth records an authentication outcome on the ring and in
// the daemon log. Both, because the two answer different questions: the
// ring is what an operator filters when a session's CTX% or liveness
// looks wrong, and the log is what survives the ring's eviction window.
//
// The workspace/team/role coordinates come from the claimed session when
// it exists, so a refusal can be filtered beside that session's other
// events rather than only by kind.
// heartbeatAuthInterval is how long a repeated heartbeat auth notice
// stays quiet before it speaks again. A minute is long enough to stop a
// 2s-tick orphan from owning the log and short enough that an operator
// watching a fleet still sees the condition is ongoing.
const heartbeatAuthInterval = time.Minute

type heartbeatAuthNotice struct {
	last       time.Time
	suppressed int
}

// emitHeartbeatAuth reports a heartbeat that was refused or admitted
// unbound. The first occurrence for a (kind, session) is always reported;
// repeats inside heartbeatAuthInterval are counted and folded into the
// next one, so a condition that cannot fix itself does not drown out
// everything else in the log.
func (d *Daemon) emitHeartbeatAuth(kind events.Kind, sessionKey, message string) {
	d.emitHeartbeatAuth2(kind, "", sessionKey, message)
}

// emitHeartbeatAuth2 is emitHeartbeatAuth with a cause discriminator, so
// two faults that share a kind and a session still each get reported
// rather than one silencing the other.
func (d *Daemon) emitHeartbeatAuth2(kind events.Kind, cause, sessionKey, message string) {
	key := string(kind) + "|" + cause + "|" + sessionKey

	d.hbAuthMu.Lock()
	if d.hbAuth == nil {
		d.hbAuth = make(map[string]*heartbeatAuthNotice)
	}
	n, seen := d.hbAuth[key]
	if !seen {
		n = &heartbeatAuthNotice{}
		d.hbAuth[key] = n
	}
	now := time.Now()
	if seen && now.Sub(n.last) < heartbeatAuthInterval {
		n.suppressed++
		d.hbAuthMu.Unlock()
		return
	}
	swallowed := n.suppressed
	n.suppressed = 0
	n.last = now
	d.hbAuthMu.Unlock()

	if swallowed > 0 {
		message = fmt.Sprintf("%s (%d more since the last notice)", message, swallowed)
	}

	ev := events.Event{
		Kind:     kind,
		Severity: events.SeverityWarning,
		Session:  sessionKey,
		Message:  message,
	}
	if sess, err := d.store.GetSession(sessionKey); err == nil {
		ev.Workspace = sess.Workspace
		ev.Team = sess.Team
		ev.Role = sess.Role
	}
	events.Emit(d.events, ev)
	log.Printf("heartbeat %s for %s: %s", kind, sessionKey, message)
}

// Run params
type runParams struct {
	Workspace      string   `json:"workspace"`
	Team           string   `json:"team"`
	Role           string   `json:"role"`
	RuntimeCommand string   `json:"runtime_command"`
	RuntimeArgs    []string `json:"runtime_args"`
	Script         string   `json:"script"`
}

func (d *Daemon) handleRun(params json.RawMessage) Response {
	var p runParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	if p.Workspace == "" {
		p.Workspace = "default"
	}
	if p.Team == "" {
		p.Team = "adhoc"
	}
	if p.Role == "" {
		p.Role = "adhoc"
	}

	// Ensure workspace exists.
	ws := &api.Workspace{Name: p.Workspace, CreatedAt: time.Now().UTC()}
	_ = d.store.CreateWorkspace(ws)

	rt := api.Runtime{
		Name:    p.RuntimeCommand,
		Command: p.RuntimeCommand,
		Args:    p.RuntimeArgs,
		Script:  p.Script,
	}

	// An ad-hoc run bypasses the controller entirely, so a controller-only
	// gate would leave a real hole: --team can name a team that declares a
	// budget. A run into a team with no Team record (the default
	// default/adhoc/adhoc) declares no budget and is admitted unchanged.
	if t, gerr := d.store.GetTeam(p.Workspace + "/" + p.Team); gerr == nil {
		if resp := d.admitGrowth(t, p.Role, 1, admission.TriggerRun); resp != nil {
			return *resp
		}
	}

	sess := &api.Session{
		Name:      fmt.Sprintf("run-%d", time.Now().UTC().UnixMilli()),
		Workspace: p.Workspace,
		Team:      p.Team,
		Role:      p.Role,
		Runtime:   rt,
	}

	if err := d.sessMgr.Create(sess); err != nil {
		return Response{Error: fmt.Sprintf("create session: %v", err)}
	}

	result, _ := json.Marshal(map[string]string{
		"status":      "created",
		"session_key": sess.Key(),
	})
	return Response{Result: result}
}

// Shift params
type shiftParams struct {
	TeamKey string `json:"team_key"`
	Role    string `json:"role,omitempty"`
}

func (d *Daemon) handleShift(params json.RawMessage) Response {
	var p shiftParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	if err := d.teamCtrl.InitiateShift(p.TeamKey, p.Role); err != nil {
		return Response{Error: fmt.Sprintf("initiate shift: %v", err)}
	}

	// Trigger immediate reconciliation to start the shift.
	d.teamCtrl.ReconcileOnce()

	result, _ := json.Marshal(map[string]string{
		"status": "shift_initiated",
		"team":   p.TeamKey,
	})
	return Response{Result: result}
}

// Reset-health params — clear one role's crash-loop state (RestartCount +
// backoff) without deleting the team. See aae-orc-fv3h.
type resetHealthParams struct {
	TeamKey string `json:"team_key"` // workspace/team
	Role    string `json:"role"`
}

// handleResetHealth clears a single role's accumulated restart count and
// backoff — the operator override for a role that has recovered, and the only
// route to thaw a saturated or restart_policy=never freeze short of deleting
// the team. It resolves the team so it can validate the role and derive the
// canonical workspace/team, then reconciles so a role no longer held back can
// spawn its replacement immediately.
func (d *Daemon) handleResetHealth(params json.RawMessage) Response {
	var p resetHealthParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	if p.Role == "" {
		return Response{Error: "reset-health requires --role"}
	}
	t, err := d.store.GetTeam(p.TeamKey)
	if err != nil {
		return Response{Error: fmt.Sprintf("team %s: %v", p.TeamKey, err)}
	}
	found := false
	for i := range t.Roles {
		if t.Roles[i].Name == p.Role {
			found = true
			break
		}
	}
	if !found {
		return Response{Error: fmt.Sprintf("role %s not found in team %s", p.Role, p.TeamKey)}
	}

	cleared := d.teamCtrl.ClearRoleHealthForRole(t.Workspace, t.Name, p.Role)
	// A cleared freeze or elapsed backoff means the reconciler can now spawn
	// the role's missing replicas; run one pass so the effect is immediate
	// rather than waiting for the next tick.
	d.teamCtrl.ReconcileOnce()

	result, _ := json.Marshal(map[string]any{
		"status":  "health_reset",
		"cleared": cleared,
		"team":    p.TeamKey,
		"role":    p.Role,
	})
	return Response{Result: result}
}

// Inject params — send keystrokes to a session's pane (executive privilege).
type injectParams struct {
	SessionKey string `json:"session_key"`
	Text       string `json:"text"`
	Literal    bool   `json:"literal"`
	Enter      bool   `json:"enter"`
}

func (d *Daemon) handleInject(params json.RawMessage) Response {
	var p injectParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	sess, err := d.store.GetSession(p.SessionKey)
	if err != nil {
		return Response{Error: err.Error()}
	}

	if sess.PaneID == "" {
		return Response{Error: fmt.Sprintf("session %s has no pane", p.SessionKey)}
	}

	if err := d.driver.SendKeys(sess.PaneID, p.Text, p.Literal, p.Enter); err != nil {
		return Response{Error: fmt.Sprintf("inject %s: %v", p.SessionKey, err)}
	}

	log.Printf("inject: %s <- %d bytes (literal=%v, enter=%v)", p.SessionKey, len(p.Text), p.Literal, p.Enter)

	result, _ := json.Marshal(map[string]string{
		"status":  "injected",
		"session": p.SessionKey,
	})
	return Response{Result: result}
}

// Capture params — read a session's pane content.
type captureParams struct {
	SessionKey string `json:"session_key"`
	Start      *int   `json:"start,omitempty"`
	End        *int   `json:"end,omitempty"`
}

// captureVisibleEnd stands in for an omitted end bound. tmux clamps an
// over-large -E to the last visible row, which is also tmux's own default
// end — so a start-only request spans scrollback through the bottom of
// the screen instead of stopping at -E 0, the TOP visible line (a
// one-line span on alternate-screen panes; marvel#114).
const captureVisibleEnd = 100000

// captureBounds resolves the optional start/end params to concrete tmux
// bounds. Either bound alone triggers a range capture; the missing bound
// defaults to tmux's own default for that side (-S 0 = top of visible,
// -E clamped to bottom of visible).
func captureBounds(p captureParams) (start, end int, ranged bool) {
	if p.Start == nil && p.End == nil {
		return 0, 0, false
	}
	start, end = 0, captureVisibleEnd
	if p.Start != nil {
		start = *p.Start
	}
	if p.End != nil {
		end = *p.End
	}
	return start, end, true
}

func (d *Daemon) handleCapture(params json.RawMessage) Response {
	var p captureParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	sess, err := d.store.GetSession(p.SessionKey)
	if err != nil {
		return Response{Error: err.Error()}
	}

	if sess.PaneID == "" {
		return Response{Error: fmt.Sprintf("session %s has no pane", p.SessionKey)}
	}

	var content string
	if start, end, ranged := captureBounds(p); ranged {
		content, err = d.driver.CapturePaneRange(sess.PaneID, start, end)
	} else {
		content, err = d.driver.CapturePane(sess.PaneID)
	}
	if err != nil {
		return Response{Error: fmt.Sprintf("capture %s: %v", p.SessionKey, err)}
	}

	result, _ := json.Marshal(map[string]string{
		"status":  "captured",
		"session": p.SessionKey,
		"content": content,
	})
	return Response{Result: result}
}

// Stop params
type stopParams struct {
	// Teardown selects agent destruction over detach: delete every
	// session and kill every workspace tmux session before exiting.
	// Default (false) detaches: agents keep running and the next
	// daemon start adopts them.
	Teardown bool `json:"teardown,omitempty"`
	// KeepBus leaves a managed nats-server running when the daemon exits,
	// so the next daemon adopts it and the agents' bus connections hold.
	// Ignored with Teardown, which ends the bus with everything else.
	KeepBus bool `json:"keep_bus,omitempty"`
}

// stopMode decodes the stop params into the shutdown mode. Absent or
// empty params mean detach; a client that predates the flag must not
// get a teardown.
func stopMode(params json.RawMessage) (teardown bool, mode string, err error) {
	var p stopParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return false, "", err
		}
	}
	if p.Teardown {
		return true, "teardown", nil
	}
	return false, "detach", nil
}

// handleReap lists marvel-* tmux state this daemon does not own and,
// only when the caller confirms, destroys it.
//
// It is the other half of the 2026-08-07 ruling. Leaving unrecorded
// state alone is the safe default, and it accumulates; this is how an
// operator clears it deliberately, having seen what they are about to
// lose. Confirmation is the caller's explicit flag, never inferred, so
// the destructive branch cannot be reached by a command that merely
// looks like a query.
type orphansResult struct {
	Orphans []OrphanRecord `json:"orphans"`
}

// orphanRecords snapshots the orphan registry and decorates each key with
// its workspace/team/role from the current session record, so the report
// filters beside that session's other state. Resolving at read time keeps
// the coordinates fresh across shifts that reuse a key.
func (d *Daemon) orphanRecords() []OrphanRecord {
	records := d.orphans.snapshot(time.Now())
	for i := range records {
		if sess, err := d.store.GetSession(records[i].SessionKey); err == nil {
			records[i].Workspace = sess.Workspace
			records[i].Team = sess.Team
			records[i].Role = sess.Role
		}
	}
	return records
}

// handleOrphans reports session keys with a live orphan presenter — a
// process heartbeating with a token minted for an earlier incarnation of
// the key. Read-only: marvel reports orphans and never kills them
// (aae-orc-m4of; the never-destroy rule from PRs #123, #132 stands).
func (d *Daemon) handleOrphans() Response {
	data, err := json.Marshal(orphansResult{Orphans: d.orphanRecords()})
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal orphans: %v", err)}
	}
	return Response{Result: data}
}

func (d *Daemon) handleReap(params json.RawMessage) Response {
	var p struct {
		Confirm bool `json:"confirm"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return Response{Error: fmt.Sprintf("bad params: %v", err)}
		}
	}

	found, err := d.sessMgr.UnrecordedTmuxState()
	if err != nil {
		return Response{Error: err.Error()}
	}

	// Orphans ride along as evidence, not as reap targets: a stale-token
	// presenter is a process marvel will not kill (aae-orc-m4of), distinct
	// from the unrecorded panes reap destroys. Naming both is candidate 1
	// of m4of — reap names processes as well as panes.
	orphans := d.orphanRecords()

	if !p.Confirm {
		result, _ := json.Marshal(map[string]any{
			"reaped":     false,
			"candidates": found,
			"orphans":    orphans,
		})
		return Response{Result: result}
	}

	_, killed, err := d.sessMgr.AdoptOrKill()
	if err != nil {
		return Response{Error: err.Error()}
	}
	result, _ := json.Marshal(map[string]any{
		"reaped":     true,
		"killed":     killed,
		"candidates": found,
		"orphans":    orphans,
	})
	return Response{Result: result}
}

// StopResult is what a stop request reports back before the daemon exits.
type StopResult struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
	// Unowned lists the marvel-* tmux state this daemon has no record of.
	// Teardown deletes recorded sessions and kills recorded workspaces'
	// tmux sessions, so anything in here survives it.
	Unowned []string `json:"unowned,omitempty"`
}

// stopReport builds the response for a stop request, surveying what a
// teardown will leave standing.
//
// Teardown is scoped to recorded state on purpose: Decision 5
// (docs/design/daemon-isolation.md, ratified 2026-08-07) reserves the
// destruction of marvel-* tmux state this daemon does not own for the
// explicit acts, `marvel daemon --reclaim` and `marvel reap --confirm`.
// A daemon that swept by name at teardown would destroy a second
// daemon's fleet, which is the bug that ruling was written to end.
//
// What was missing is the honesty, not the sweep. The CLI printed
// "agents torn down" whatever survived, because this response was built
// before shutdown and carried nothing about it. Surveying here, on the
// request goroutine, is what makes the answer reachable at all: the
// shutdown goroutine ends the process. See ArcavenAE/marvel#92.
func (d *Daemon) stopReport(teardown bool, mode string) Response {
	res := StopResult{Status: "stopping", Mode: mode}
	if teardown {
		found, err := d.sessMgr.UnrecordedTmuxState()
		if err != nil {
			// A survey that cannot run is worth a log line and nothing
			// more: refusing the stop over it would strand the operator.
			log.Printf("survey unowned tmux state: %v", err)
		}
		res.Unowned = found
	}
	data, err := json.Marshal(res)
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal stop result: %v", err)}
	}
	return Response{Result: data}
}

func (d *Daemon) handleStop(params json.RawMessage) Response {
	teardown, mode, err := stopMode(params)
	if err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	if len(params) > 0 {
		var p stopParams
		_ = json.Unmarshal(params, &p) // stopMode already validated the shape
		d.keepBus = p.KeepBus
	}
	// Survey before the shutdown goroutine starts, so the answer is
	// computed while the process is still alive to send it.
	resp := d.stopReport(teardown, mode)
	// Shut down off the request goroutine: the client is still reading
	// this connection, and the listener closes inside shutdown.
	go func() {
		time.Sleep(100 * time.Millisecond)
		if teardown {
			d.Stop()
		} else {
			d.Detach()
		}
		os.Exit(0)
	}()
	return resp
}

// handleReexec tells the running daemon to replace its own process image
// with a fresh exec of the marvel binary, adopting the live panes rather
// than stopping the agents. The CLI (`marvel daemon reexec`, or
// `marvel upgrade --daemon` after the binary is installed) calls this;
// the daemon must exec itself because the CLI cannot exec the daemon's
// process.
func (d *Daemon) handleReexec() Response {
	// Resolve the binary here so an unresolvable path is reported to the
	// client without detaching; better to keep serving than to stop and
	// then find nothing to exec.
	exe, err := selfExecPath()
	if err != nil {
		return Response{Error: err.Error()}
	}
	// Re-exec off the request goroutine so this response reaches the
	// client before the process image is replaced, mirroring handleStop.
	go func() {
		time.Sleep(100 * time.Millisecond)
		if rerr := d.Reexec(); rerr != nil {
			log.Printf("reexec failed after detach: %v; start a fresh daemon to adopt the running panes", rerr)
			os.Exit(1)
		}
	}()
	result, _ := json.Marshal(map[string]string{"status": "reexec", "binary": exe})
	return Response{Result: result}
}

// DialOptions controls how the client connects to a marvel daemon.
type DialOptions struct {
	// Identity is an optional private key file used for SSH auth. When
	// set, it takes precedence over SSH_AUTH_SOCK and default key files.
	Identity string
	// TrustUnknownHost, when true, auto-adds any unknown host key to
	// ~/.marvel/known_hosts without prompting. Used by
	// `marvel keys trust` — do not set for ordinary RPC calls.
	TrustUnknownHost bool
	// StrictHostKey, when true, refuses unknown hosts without prompting.
	// Intended for non-interactive scripts. When false and the caller
	// is on a TTY, marvel prompts; when false and off-TTY, marvel
	// refuses with a pointer to `marvel keys trust`.
	StrictHostKey bool
}

// SendRequest sends a request to the daemon and returns the response,
// using default auth (SSH_AUTH_SOCK or ~/.ssh/*).
//
// Address formats:
//
//	~/.marvel/run/marvel.sock                 → Unix socket (default, local)
//	mrvl://host                               → daemon SSH server on port 6785
//	mrvl://user@host:port                     → daemon SSH server on custom port
//	ssh://user@host/path/to/marvel.sock       → tunnel through sshd to Unix socket
//	tcp://host:port                           → bare TCP (advanced use)
func SendRequest(socketPath string, req Request) (*Response, error) {
	return SendRequestWith(socketPath, req, DialOptions{})
}

// SendRequestWith sends a request using the supplied dial options. Use
// this when the caller has a per-cluster identity key or known_hosts
// file it wants to thread through.
func SendRequestWith(socketPath string, req Request, opts DialOptions) (*Response, error) {
	conn, err := dialDaemonWith(socketPath, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &resp, nil
}

// ErrWatchUnsupported is returned by WatchEventsWith when the daemon
// predates events.watch. The caller falls back to polling the events
// method with a since_seq cursor.
var ErrWatchUnsupported = errors.New("daemon does not support events.watch; falling back to polling")

// WatchEventsWith opens an events.watch stream against the daemon and
// calls fn for every message until fn returns an error, the daemon
// closes the stream, or the dial fails. The first message is the backlog
// (see MethodEventsWatch); later messages carry live deliveries and, when
// the daemon-side watch overflowed, the gap. fn receives the stamped
// Response alongside the decoded batch so the caller can read
// DaemonHome once.
//
// A daemon that does not know the method answers the request with an
// unknown-method error, which is returned as ErrWatchUnsupported. The
// daemon closing the connection ends the stream with an error that says
// so; a clean end is fn returning an error of its own, which is
// returned unchanged.
func WatchEventsWith(socketPath string, params json.RawMessage, opts DialOptions, fn func(*Response, EventsBatch) error) error {
	conn, err := dialDaemonWith(socketPath, opts)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := json.NewEncoder(conn).Encode(Request{Method: MethodEventsWatch, Params: params}); err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	dec := json.NewDecoder(conn)
	first := true
	for {
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return errors.New("event stream ended: the daemon closed the connection")
			}
			return fmt.Errorf("read event stream: %w", err)
		}
		if resp.Error != "" {
			if first && strings.HasPrefix(resp.Error, "unknown method") {
				return ErrWatchUnsupported
			}
			return errors.New(resp.Error)
		}
		var b EventsBatch
		if len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, &b); err != nil {
				return fmt.Errorf("parse event stream: %w", err)
			}
		}
		if err := fn(&resp, b); err != nil {
			return err
		}
		first = false
	}
}

// dialDaemonWith connects to the daemon. Routes based on address scheme:
//
//	mrvl://host            → embedded SSH server on port 6785
//	mrvl://host:port       → embedded SSH server on custom port
//	ssh://host/path        → tunnel through sshd to Unix socket
//	ssh://host:port        → embedded SSH server (same as mrvl://)
//	tcp://host:port        → bare TCP (advanced)
//	/path/to/socket        → Unix socket (local)
func dialDaemonWith(addr string, opts DialOptions) (net.Conn, error) {
	if isMRVL(addr) {
		return dialMRVL(addr, opts)
	}
	if isSSH(addr) {
		return dialSSH(addr, opts)
	}

	// Strip tcp:// prefix for explicit bare-TCP use.
	if strings.HasPrefix(addr, "tcp://") {
		addr = strings.TrimPrefix(addr, "tcp://")
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("connect to daemon at %s (tcp): %w", addr, err)
		}
		return conn, nil
	}

	network := listenNetwork(addr)
	conn, err := net.Dial(network, addr)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s (%s): %w", addr, network, err)
	}
	return conn, nil
}

// dialMRVL connects to a daemon's embedded SSH server via the mrvl:// protocol.
// Default port is 6785 if not specified.
func dialMRVL(addr string, opts DialOptions) (net.Conn, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("parse mrvl address %q: %w", addr, err)
	}

	user := u.User.Username()
	if user == "" {
		user = os.Getenv("USER")
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = DefaultMRVLPort
	}

	return dialSSHDirect(user, host, port, opts)
}

// dialSSH parses an ssh:// URL and dials the daemon's socket through an
// SSH tunnel. Auth prefers opts.Identity, then SSH_AUTH_SOCK, then common
// key files (~/.ssh/id_ed25519, ~/.ssh/id_rsa).
//
// URL formats:
//
//	ssh://user@host/path/to/socket        → SSH to host:22, dial Unix socket
//	ssh://user@host:2222/path/to/socket   → SSH to host:2222, dial Unix socket
//	ssh://user@host:9090                  → SSH to host:22, dial TCP localhost:9090
//	ssh://host/path/to/socket             → SSH as current user
func dialSSH(addr string, opts DialOptions) (net.Conn, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("parse ssh address %q: %w", addr, err)
	}

	user := u.User.Username()
	if user == "" {
		user = os.Getenv("USER")
	}

	sshHost := u.Hostname()
	sshPort := u.Port()

	// Determine connection mode.
	remotePath := u.Path

	if remotePath != "" && remotePath != "/" {
		// Mode 1: path present → tunnel through sshd to remote Unix socket.
		// Any port in the URL is the SSH port (default 22).
		if sshPort == "" {
			sshPort = "22"
		}
		return dialSSHTunnel(user, sshHost, sshPort, "unix", remotePath, opts)
	}

	// Mode 2: no path → connect directly to daemon's embedded SSH server.
	// The port in the URL is the daemon's SSH server port.
	if sshPort == "" {
		return nil, fmt.Errorf("ssh address %q: need a port for the daemon's SSH server", addr)
	}
	return dialSSHDirect(user, sshHost, sshPort, opts)
}

// dialSSHTunnel connects through a remote sshd to a Unix or TCP socket (mode 1).
func dialSSHTunnel(user, host, sshPort, network, addr string, opts DialOptions) (net.Conn, error) {
	config, err := sshClientConfig(user, opts)
	if err != nil {
		return nil, err
	}

	sshConn, err := ssh.Dial("tcp", net.JoinHostPort(host, sshPort), config)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s@%s:%s: %w", user, host, sshPort, err)
	}

	conn, err := sshConn.Dial(network, addr)
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh tunnel %s %s via %s: %w", network, addr, host, err)
	}

	return &sshWrappedConn{Conn: conn, sshClient: sshConn}, nil
}

// dialSSHDirect connects to the daemon's embedded SSH server (mode 2).
// Opens a session channel for JSON-RPC instead of tunneling to a socket.
func dialSSHDirect(user, host, port string, opts DialOptions) (net.Conn, error) {
	config, err := sshClientConfig(user, opts)
	if err != nil {
		return nil, err
	}

	sshConn, err := ssh.Dial("tcp", net.JoinHostPort(host, port), config)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s@%s:%s: %w", user, host, port, err)
	}

	session, err := sshConn.NewSession()
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh open session on %s: %w", host, err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh stdout pipe: %w", err)
	}

	// Start shell so the channel stays open for bidirectional I/O.
	if err := session.Shell(); err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh start shell: %w", err)
	}

	return &sshSessionConn{
		Reader:    stdout,
		Writer:    stdin,
		session:   session,
		sshClient: sshConn,
	}, nil
}

func sshClientConfig(user string, opts DialOptions) (*ssh.ClientConfig, error) {
	methods, err := sshAuthMethodsFor(opts.Identity)
	if err != nil {
		return nil, err
	}
	if len(methods) == 0 {
		return nil, errors.New("no SSH auth available: generate a key with 'marvel keys generate' or start ssh-agent")
	}

	layout, err := paths.Default()
	if err != nil {
		return nil, err
	}
	mode := knownhosts.ModePrompt
	if opts.TrustUnknownHost {
		mode = knownhosts.ModeTrust
	} else if opts.StrictHostKey {
		mode = knownhosts.ModeStrict
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: knownhosts.Callback(layout, mode, nil, nil),
		Timeout:         10 * time.Second,
	}, nil
}

// sshSessionConn wraps an SSH session's stdin/stdout as a net.Conn-like
// io.ReadWriteCloser for mode 2 (direct daemon SSH).
type sshSessionConn struct {
	io.Reader
	io.Writer
	session   *ssh.Session
	sshClient *ssh.Client
}

func (c *sshSessionConn) Close() error {
	_ = c.session.Close()
	return c.sshClient.Close()
}

// Implement net.Conn interface stubs for compatibility.
func (c *sshSessionConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (c *sshSessionConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (c *sshSessionConn) SetDeadline(t time.Time) error      { return nil }
func (c *sshSessionConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *sshSessionConn) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "ssh" }
func (dummyAddr) String() string  { return "ssh" }

// sshWrappedConn wraps an SSH-tunneled connection so that closing it
// also closes the underlying SSH client (mode 1).
type sshWrappedConn struct {
	net.Conn
	sshClient *ssh.Client
}

func (c *sshWrappedConn) Close() error {
	err := c.Conn.Close()
	_ = c.sshClient.Close()
	return err
}

// sshAuthMethodsFor returns SSH auth methods for a cluster.
//
// Precedence:
//  1. identity file from the cluster config (if set)
//  2. default marvel client key (~/.marvel/keys/client_ed25519) when present
//  3. SSH_AUTH_SOCK (developer agent)
//  4. ~/.ssh/id_ed25519, ~/.ssh/id_rsa
//
// If identity is set but unreadable or has weak permissions, that is a
// hard error — callers expect the cluster's configured key to be used.
func sshAuthMethodsFor(identity string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if identity != "" {
		signer, err := loadKeyFile(identity, true)
		if err != nil {
			return nil, fmt.Errorf("cluster identity %s: %w", identity, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
		return methods, nil
	}

	// Implicit default: marvel's own client key if it exists.
	if layout, err := paths.Default(); err == nil {
		defaultKey := layout.DefaultClientKey()
		if _, statErr := os.Stat(defaultKey); statErr == nil {
			signer, err := loadKeyFile(defaultKey, true)
			if err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
			} else {
				log.Printf("warning: %s unusable: %v", defaultKey, err)
			}
		}
	}

	// SSH agent (most common for developers).
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Standard ~/.ssh/ fallback.
	home, err := os.UserHomeDir()
	if err != nil {
		return methods, nil
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		signer, err := loadKeyFile(p, false)
		if err != nil {
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	return methods, nil
}

// loadKeyFile reads and parses a private key. When strictPerms is true,
// refuses to load keys with group- or world-accessible permissions.
func loadKeyFile(path string, strictPerms bool) (ssh.Signer, error) {
	if strictPerms {
		if err := paths.VerifyPrivateKeyMode(path); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// busLeafCredential is the Store credential whose presence turns on the
// leaf remote in the rendered broker conf (brief 9 S3 names it; S6 consumes
// it through the broker environment).
const busLeafCredential = "bus/leaf"

// attachServices binds this daemon to its cluster's Services list
// (docs/design/services-list.md section 1.3) and attaches each entry by
// provider. The client config names clusters by how to reach them, and
// the daemon is reached at exactly one socket path, so the entry whose
// socket is ours is ours. A cluster with no services leaves d.bus nil. A
// list the validator refuses is logged and skipped rather than refusing
// the daemon; sessions still run, they just get no managed service. One
// arm today, nats-server; a second driver joins the loop, not a second
// bespoke function.
func (d *Daemon) attachServices(socketPath string) error {
	cfg, err := config.Load()
	if err != nil && !errors.Is(err, config.ErrInvalidClusterName) && !errors.Is(err, config.ErrInvalidBus) && !errors.Is(err, config.ErrInvalidService) {
		log.Printf("services: client config unreadable, no managed service: %v", err)
		return nil
	}
	if cfg == nil {
		return nil
	}
	cl := cfg.ClusterForSocket(socketPath)
	if cl == nil {
		return nil
	}
	if verr := config.ValidateServices(cl); verr != nil {
		log.Printf("services: cluster %s has an invalid services list, nothing attached: %v", cl.Name, verr)
		return nil
	}
	entries, aerr := cl.AllServices()
	if aerr != nil {
		log.Printf("services: cluster %s: %v", cl.Name, aerr)
		return nil
	}
	if len(entries) == 0 {
		return nil
	}
	if nerr := config.ValidateClusterName(cl.Name); nerr != nil {
		log.Printf("services: %v; nothing attached", nerr)
		return nil
	}
	layout, lerr := paths.Default()
	if lerr != nil {
		log.Printf("services: resolve layout: %v", lerr)
		return nil
	}
	busSeen := false
	for i := range entries {
		svc := &entries[i]
		switch svc.Provider {
		case service.ProviderNATSServer:
			if busSeen {
				return fmt.Errorf("services: cluster %s declares a second %s entry %q; one bus per cluster today", cl.Name, service.ClassMessageBus, svc.Name)
			}
			busSeen = true
			if err := d.attachBus(cl, svc, layout); err != nil {
				return err
			}
		default:
			// Unreachable after ValidateServices; named so a registry
			// addition without a daemon arm fails loudly at start.
			return fmt.Errorf("services: cluster %s: service %q: no daemon arm for provider %q", cl.Name, svc.Name, svc.Provider)
		}
	}
	return nil
}

// attachBus is the nats-server arm of attachServices: it renders the
// broker configuration once and supervises the broker, or for an adopted
// or external bus hands sessions the URL and nothing else.
func (d *Daemon) attachBus(cl *config.Cluster, svc *config.Service, layout paths.Layout) error {
	spec, err := svc.BusSpec()
	if err != nil {
		return err
	}
	rb := spec.Resolve(layout.StateDir())
	if !rb.Managed {
		// Sessions still learn the URL; there is no authorization to hand out.
		d.sessMgr.Bus = bus.NewAdopted(rb)
		log.Printf("bus: cluster %s reaches an existing broker at %s (mode %s); sessions receive NATS_URL, nothing rendered", cl.Name, rb.URL, rb.Mode)
		return nil
	}
	mgr, merr := bus.NewManager(filepath.Join(layout.StateDir(), "nats"), cl.Name, rb, d.store, func() bool {
		_, gerr := d.store.GetCredential(busLeafCredential)
		return gerr == nil
	})
	if merr != nil {
		return merr
	}
	sup, serr := bus.NewSupervisor(mgr, layout.RunDir(), layout.LogDir(), d.events)
	if serr != nil {
		return serr
	}
	d.bus = mgr
	d.sessMgr.Bus = mgr
	// Render before start so the child reads a current conf; wire the
	// reloader after, so this first render is not asked to SIGHUP a broker
	// that is not running yet.
	// The leaf seed: Store memory to the child's environment, read at each
	// spawn so a restart after `credential put bus/leaf` carries it. Nothing
	// else sees it: not the conf, not the log, not a session.
	sup.Env = func() []string {
		seed, err := d.store.RevealCredentialValue(busLeafCredential)
		if err != nil {
			return nil
		}
		return []string{bus.LeafSeedEnv + "=" + string(seed)}
	}
	d.regenerateBus("start")
	mgr.Reloader = sup
	// The moment after the listener answers and before any session may
	// spawn belongs to provisioning: a bare broker strands the shim
	// (finding-166). Runs as the admin identity, idempotent, and a failure
	// is a start failure rather than a fleet that looks running and is not.
	sup.AfterReady = func() error {
		admin := mgr.Admin()
		got, perr := bus.Provision(context.Background(), mgr.URL(), admin.Name, admin.Password)
		if perr != nil {
			return perr
		}
		msg := fmt.Sprintf("broker %s provisioned: %s", rb.Listen, got)
		log.Printf("%s: %s", events.KindBusProvisioned, msg)
		events.Emit(d.events, events.Event{Kind: events.KindBusProvisioned, Severity: events.SeverityInfo, Message: msg})
		return nil
	}
	if err := sup.Start(context.Background()); err != nil {
		return err
	}
	d.busSup = sup
	d.teamCtrl.BusGate = func() (bool, string) {
		if sup.Ready() {
			return true, ""
		}
		st := sup.Status()
		if st.BackoffUntil != "" {
			return false, fmt.Sprintf("bus %s is down; restart #%d waits until %s", rb.Listen, st.Restarts, st.BackoffUntil)
		}
		// A structural miss holds new spawns while the process stays up
		// (aae-orc-vy6k7); the reason names what is missing so the
		// operator reads it from the hold, not from the ring.
		if s := st.Structure; s != nil && st.PID != 0 {
			switch {
			case !s.Provisioned:
				return false, fmt.Sprintf("bus %s is missing %s; re-provisioning, nothing is restarted", rb.Listen, strings.Join(s.Missing, ", "))
			case !s.Authorized:
				return false, fmt.Sprintf("bus %s has no authorization loaded; one reload sent, holding until it lands", rb.Listen)
			}
		}
		return false, fmt.Sprintf("bus %s is not ready", rb.Listen)
	}
	return nil
}

// handleBusStatus serves `marvel bus status`: the supervised broker when
// there is one, the adopted URL when the cluster only points at a bus, and
// a plain "no bus" otherwise.
func (d *Daemon) handleBusStatus() Response {
	var st bus.Status
	switch {
	case d.busSup != nil:
		st = d.busSup.Status()
	case d.sessMgr.Bus != nil:
		st = bus.Status{Managed: false, URL: d.sessMgr.Bus.URL(), Leaf: "n/a"}
		if a, ok := d.sessMgr.Bus.(bus.Adopted); ok {
			rb := a.Bus()
			st.Class, st.Provider, st.Mode, st.CallerIdentity = rb.Class, rb.Provider, string(rb.Mode), rb.CallerIdentity
		}
	default:
		return Response{Error: "no bus is configured for this cluster; add a bus section to its entry in the client config"}
	}
	data, err := json.Marshal(st)
	if err != nil {
		return Response{Error: fmt.Sprintf("encode bus status: %v", err)}
	}
	return Response{Result: data}
}

// backendVerifyParams names one session to verify, or every session when
// blank.
type backendVerifyParams struct {
	Session string `json:"session"`
}

// handleBackendVerify answers which backend each session is actually on and
// whether the layers agree (BT7). It runs here rather than in the CLI because
// the overlay directory is the session manager's own state; see
// session.Manager.VerifyBackend.
func (d *Daemon) handleBackendVerify(params json.RawMessage) Response {
	var p backendVerifyParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return Response{Error: fmt.Sprintf("bad params: %v", err)}
		}
	}
	sessions := d.store.ListSessions()
	out := make([]api.BackendVerification, 0, len(sessions))
	for _, sess := range sessions {
		if p.Session != "" && sess.Key() != p.Session {
			continue
		}
		out = append(out, d.sessMgr.VerifyBackend(sess))
	}
	if p.Session != "" && len(out) == 0 {
		return Response{Error: fmt.Sprintf("no session %s", p.Session)}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return Response{Error: fmt.Sprintf("encode backend verification: %v", err)}
	}
	return Response{Result: data}
}

// regenerateBus re-renders the managed broker's files from the applied
// teams and the leaf seed's presence and reloads the broker on a change. It
// is a no-op without a managed bus and quiet when nothing changed; a change
// or a failure lands on the ring.
func (d *Daemon) regenerateBus(reason string) {
	if d.bus == nil {
		return
	}
	changed, err := d.bus.Regenerate()
	d.emitBusRender(reason, changed, err)
}

// regenerateBusNoReload re-renders the managed broker's files without asking
// the broker to reload. The enrollment path uses it when only a fresh process
// can pick the change up (the leaf seed reaching a broker that booted
// unenrolled), so the caller restarts instead of the broker being SIGHUP'd
// with a conf it cannot resolve yet.
func (d *Daemon) regenerateBusNoReload(reason string) error {
	if d.bus == nil {
		return nil
	}
	changed, err := d.bus.Render()
	d.emitBusRender(reason, changed, err)
	return err
}

// emitBusRender records the outcome of a render on the ring and the log.
func (d *Daemon) emitBusRender(reason string, changed bool, err error) {
	switch {
	case err != nil:
		msg := fmt.Sprintf("bus config not rendered (%s): %v", reason, err)
		events.Emit(d.events, events.Event{Kind: events.KindBusRendered, Severity: events.SeverityWarning, Message: msg})
		log.Printf("%s: %s", events.KindBusRendered, msg)
	case changed:
		msg := fmt.Sprintf("bus config rendered (%s): %s", reason, d.bus.ConfPath())
		events.Emit(d.events, events.Event{Kind: events.KindBusRendered, Severity: events.SeverityInfo, Message: msg})
		log.Printf("%s: %s", events.KindBusRendered, msg)
	}
}

// restartBus restarts the supervised broker so a change that only a new
// process can pick up (the leaf seed in its environment) takes effect. A
// failure is logged and surfaced as bus.crashed-shaped warning text on the
// ring; the controller's hold covers the gap until the next successful
// start.
func (d *Daemon) restartBus(reason string) {
	if d.busSup == nil {
		return
	}
	if err := d.busSup.Restart(reason); err != nil {
		msg := fmt.Sprintf("bus restart (%s) failed: %v", reason, err)
		events.Emit(d.events, events.Event{Kind: events.KindBusCrashed, Severity: events.SeverityWarning, Message: msg})
		log.Printf("%s: %s", events.KindBusCrashed, msg)
	}
}

// enrollLeafSeed brings the leaf up after a bus/leaf seed is stored. It reloads
// when the running broker's environment already carries the seed, and restarts
// only when the broker booted unenrolled so a fresh process reads the seed from
// its environment. That restart is the one accepted bounce (surfaced as its own
// "picking up the leaf seed" start, never as a connect/disconnect); afterward
// the environment carries the seed for the process's life and the toggle is
// reload-only (aae-orc-ct0l4).
func (d *Daemon) enrollLeafSeed() {
	if d.bus == nil {
		return
	}
	if d.busSup != nil && d.leafSeedNeedsRestart() {
		// The running broker cannot resolve the current seed on a reload (it
		// booted unenrolled, or its environment carries a different seed than
		// the one now stored). Render without reloading, then restart so the
		// new process reads it. When the operator has the leaf detached, the
		// restart still loads the seed (so a later connect is reload-only) but
		// renders no leaf, so the reason says so and the bounce is not read as
		// a failed bring-up.
		reason := "picking up the leaf seed after enrollment"
		if !d.bus.LeafAttached() {
			reason += "; leaf stays detached until connect"
		}
		if err := d.regenerateBusNoReload("credential.put " + busLeafCredential); err != nil {
			// Restarting on a conf that did not render would read stale files;
			// the render error is already on the ring, so hold and let the next
			// enrollment or reconcile retry.
			return
		}
		d.restartBus(reason)
		return
	}
	d.regenerateBus("credential.put " + busLeafCredential)
}

// leafSeedNeedsRestart reports whether picking up the enrolled seed needs a
// fresh broker process rather than a reload. nats-server reads the seed from
// its environment once at start, so a broker that booted unenrolled, or one
// whose environment carries a different seed than the one now stored (a
// rotation), cannot resolve the current seed on SIGHUP.
func (d *Daemon) leafSeedNeedsRestart() bool {
	if !d.busSup.LeafSeedInEnv() {
		return true
	}
	seed, err := d.store.RevealCredentialValue(busLeafCredential)
	if err != nil {
		return false // nothing to compare against; a reload is the safe default
	}
	return bus.SeedFingerprint(seed) != d.busSup.LeafSeedFingerprint()
}

// handleBusLeafConnect and handleBusLeafDisconnect toggle the leaf link on a
// managed broker without a bounce: they render the leafnodes block in or out
// and reload (SIGHUP), leaving the seed enrolled and every local session
// connected. They never restart; the one enrollment restart lives in
// enrollLeafSeed (aae-orc-ct0l4).
func (d *Daemon) handleBusLeafConnect() Response {
	return d.setLeafAttached(true, "bus.leaf.connect")
}

func (d *Daemon) handleBusLeafDisconnect() Response {
	return d.setLeafAttached(false, "bus.leaf.disconnect")
}

func (d *Daemon) setLeafAttached(attached bool, reason string) Response {
	if d.bus == nil || d.busSup == nil {
		return Response{Error: "no managed bus is configured for this cluster; connect and disconnect apply to a marvel-supervised broker"}
	}
	if d.bus.Bus().HubURL == "" {
		return Response{Error: "this cluster declares no hub (bus.hub.url); there is no leaf to connect or disconnect"}
	}
	changed, err := d.bus.SetLeafAttached(attached)
	if err != nil {
		return Response{Error: fmt.Sprintf("%s: %v", reason, err)}
	}
	verb := "disconnected"
	if attached {
		verb = "connected"
	}
	if changed {
		msg := fmt.Sprintf("leaf %s from %s by config reload; local sessions kept", verb, d.bus.Bus().HubURL)
		events.Emit(d.events, events.Event{Kind: events.KindBusReloaded, Severity: events.SeverityInfo, Message: msg})
		log.Printf("%s: %s", events.KindBusReloaded, msg)
	}
	data, err := json.Marshal(d.busSup.Status())
	if err != nil {
		return Response{Error: fmt.Sprintf("encode bus status: %v", err)}
	}
	return Response{Result: data}
}
