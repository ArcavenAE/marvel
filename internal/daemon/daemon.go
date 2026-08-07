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
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/logbuf"
	"github.com/arcavenae/marvel/internal/paths"
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
// otherwise "unix". Used by the daemon listener side only.
func listenNetwork(addr string) string {
	if strings.Contains(addr, ":") {
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
	cancel    context.CancelFunc
	wg        sync.WaitGroup

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

	// In-memory ring of the most recent log lines. Always non-nil.
	logs *logbuf.Buffer

	// In-memory ring of structured state-transition events.
	// Always non-nil.
	events *events.Ring

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
	acct := usage.New(store, usage.NewResolver(limits), usage.WithEvents(evRing))
	sessMgr.Usage = acct

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

	// Announced from Start, not from the constructor. cmd/marvel installs
	// log.SetOutput (log ring plus the optional --log-file) only after
	// NewWithOptions returns, so a line written during construction reaches
	// bare stderr and neither `marvel daemon logs` nor the log file. This is
	// the one observability affordance for the in-memory token window, so it
	// has to land where the docs say it lands.
	d.logTokenBudgetWindows()

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

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
	// half of self-update.
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
	d.handleRWC(conn)
}

// handleRWC processes a single JSON-RPC request/response on any
// io.ReadWriteCloser — used by both Unix socket and SSH channels.
func (d *Daemon) handleRWC(rwc io.ReadWriteCloser) {
	defer func() { _ = rwc.Close() }()

	var req Request
	if err := json.NewDecoder(rwc).Decode(&req); err != nil {
		resp := Response{Error: fmt.Sprintf("decode request: %v", err)}
		_ = json.NewEncoder(rwc).Encode(d.stamp(resp))
		return
	}

	resp := d.dispatch(req)
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

func (d *Daemon) dispatch(req Request) Response {
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
	case "reap":
		return d.handleReap(req.Params)
	case "heartbeat":
		return d.handleHeartbeat(req.Params)
	case "run":
		return d.handleRun(req.Params)
	case "shift":
		return d.handleShift(req.Params)
	case "inject":
		return d.handleInject(req.Params)
	case "capture":
		return d.handleCapture(req.Params)
	case "stop":
		return d.handleStop(req.Params)
	case "reexec":
		return d.handleReexec()
	case "logs":
		return d.handleLogs(req.Params)
	case "events":
		return d.handleEvents(req.Params)
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

type eventsResult struct {
	Events []events.Event `json:"events"`
}

func (d *Daemon) handleEvents(params json.RawMessage) Response {
	var p eventsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return Response{Error: fmt.Sprintf("bad params: %v", err)}
		}
	}
	f := events.Filter{
		Workspace:   p.Workspace,
		Team:        p.Team,
		Role:        p.Role,
		Session:     p.Session,
		Kind:        events.Kind(p.Kind),
		MinSeverity: events.Severity(p.MinSeverity),
		SinceSeq:    p.SinceSeq,
	}
	snap := d.events.Snapshot(f, p.N)
	data, err := json.Marshal(eventsResult{Events: snap})
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal events: %v", err)}
	}
	return Response{Result: data}
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
		return Response{Error: fmt.Sprintf("parse manifest: %v", err)}
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

	// Re-project policies for already-running sessions before reconciling.
	// A policy edited in this manifest reconciles by rewriting the session's
	// settings file (finding-024 contract half); Claude Code's file watcher
	// hot-reloads it with no restart. New sessions spawned by the reconcile
	// below get their projection at spawn time.
	if n := d.sessMgr.Reproject(); n > 0 {
		log.Printf("apply: re-projected policy for %d running session(s)", n)
	}

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
		result = d.store.ListSessions()
	case "teams", "team":
		result = d.store.ListTeams()
	case "workspaces", "workspace":
		result = d.store.ListWorkspaces()
	case "endpoints", "endpoint":
		result = d.store.ListEndpoints()
	case "policies", "policy":
		result = d.store.ListPolicies()
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

// Heartbeat params
type heartbeatParams struct {
	SessionKey     string  `json:"session_key"`
	ContextPercent float64 `json:"context_percent"`
	// Model is the model as the reporter names it, "" when the
	// reporter does not know (the simulator). The statusline feed
	// sends the harness's display name.
	Model string `json:"model,omitempty"`
}

func (d *Daemon) handleHeartbeat(params json.RawMessage) Response {
	var p heartbeatParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	if err := d.store.UpdateSessionHeartbeat(p.SessionKey, p.ContextPercent, p.Model); err != nil {
		return Response{Error: err.Error()}
	}

	result, _ := json.Marshal(map[string]string{"status": "ok"})
	return Response{Result: result}
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

	if !p.Confirm {
		result, _ := json.Marshal(map[string]any{
			"reaped":     false,
			"candidates": found,
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
	})
	return Response{Result: result}
}

func (d *Daemon) handleStop(params json.RawMessage) Response {
	teardown, mode, err := stopMode(params)
	if err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
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
	result, _ := json.Marshal(map[string]string{"status": "stopping", "mode": mode})
	return Response{Result: result}
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
