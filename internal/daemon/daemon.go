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

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/logbuf"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/team"
	"github.com/arcavenae/marvel/internal/tmux"
)

const (
	// DefaultSocket is the default Unix socket path.
	DefaultSocket = "/tmp/marvel.sock"
	// DefaultMRVLPort is the default port for the mrvl:// protocol.
	DefaultMRVLPort = "6785"
	// ReconcileInterval is how often the team controller reconciles.
	ReconcileInterval = 2 * time.Second
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

	// In-memory ring of the most recent log lines. Always non-nil.
	logs *logbuf.Buffer
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
	sessMgr := session.NewManager(store, driver)
	teamCtrl := team.NewController(store, sessMgr)

	buf := opts.LogBuffer
	if buf == nil {
		buf = logbuf.New(DefaultLogBufferLines)
	}

	return &Daemon{
		store:    store,
		sessMgr:  sessMgr,
		teamCtrl: teamCtrl,
		driver:   driver,
		pidFile:  opts.PidFile,
		logs:     buf,
	}, nil
}

// LogBuffer returns the daemon's in-memory log ring. Callers can
// hook it into log.SetOutput to tee stderr into the buffer; the
// daemon process does this in cmd/marvel.
func (d *Daemon) LogBuffer() *logbuf.Buffer { return d.logs }

// Start starts the daemon: listens on Unix or TCP socket and starts reconciliation.
// The address format determines the network: "host:port" for TCP, a file path
// for Unix. Examples: "/tmp/marvel.sock", "0.0.0.0:9090", ":9090".
func (d *Daemon) Start(socketPath string) error {
	// Refuse to start if a pid file already points at a live process.
	if d.pidFile != "" {
		if err := checkPidFileFree(d.pidFile); err != nil {
			return err
		}
	}

	network := listenNetwork(socketPath)

	if network == "unix" {
		// Remove stale Unix socket.
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

	// Reclaim the marvel-* tmux namespace before anything else creates
	// panes: a previous daemon instance may have left sessions (and their
	// forestage/claude processes) running. Our fresh in-memory state
	// knows nothing about them. See ArcavenAE/marvel#13.
	if err := d.sessMgr.CleanupOrphanTmux(); err != nil {
		log.Printf("cleanup orphan tmux on startup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	// Start team reconciliation loop.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.teamCtrl.Run(ctx, ReconcileInterval)
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

// Stop shuts down the daemon, cleaning up all resources.
func (d *Daemon) Stop() {
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

	// Clean up all workspaces.
	for _, ws := range d.store.ListWorkspaces() {
		if err := d.sessMgr.CleanupWorkspace(ws.Name); err != nil {
			log.Printf("cleanup workspace %s: %v", ws.Name, err)
		}
	}

	// Only remove socket file for Unix sockets.
	if addr != "" && listenNetwork(addr) == "unix" {
		_ = os.Remove(addr)
	}
	if d.pidFile != "" {
		_ = os.Remove(d.pidFile)
	}
	log.Println("marvel daemon stopped")
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
		_ = json.NewEncoder(rwc).Encode(resp)
		return
	}

	resp := d.dispatch(req)
	_ = json.NewEncoder(rwc).Encode(resp)
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
		return d.handleStop()
	case "logs":
		return d.handleLogs(req.Params)
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

	if err := m.Apply(d.store); err != nil {
		return Response{Error: fmt.Sprintf("apply manifest: %v", err)}
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
	case "workspace":
		ws, getErr := d.store.GetWorkspace(p.Name)
		if getErr != nil {
			return Response{Error: getErr.Error()}
		}
		_ = d.sessMgr.CleanupWorkspace(ws.Name)
		err = d.store.DeleteWorkspace(p.Name)
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

	found := false
	for i := range t.Roles {
		if t.Roles[i].Name == p.Role {
			t.Roles[i].Replicas = p.Replicas
			found = true
			break
		}
	}
	if !found {
		return Response{Error: fmt.Sprintf("role %s not found in team %s", p.Role, p.TeamKey)}
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
}

func (d *Daemon) handleHeartbeat(params json.RawMessage) Response {
	var p heartbeatParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}

	if err := d.store.UpdateSessionHeartbeat(p.SessionKey, p.ContextPercent); err != nil {
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
	if p.Start != nil && p.End != nil {
		content, err = d.driver.CapturePaneRange(sess.PaneID, *p.Start, *p.End)
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

func (d *Daemon) handleStop() Response {
	go func() {
		time.Sleep(100 * time.Millisecond)
		d.Stop()
		os.Exit(0)
	}()
	result, _ := json.Marshal(map[string]string{"status": "stopping"})
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
//	/tmp/marvel.sock                          → Unix socket (default, local)
//	mrvl://host                               → daemon SSH server on port 6785
//	mrvl://user@host:port                     → daemon SSH server on custom port
//	ssh://user@host/tmp/marvel.sock           → tunnel through sshd to Unix socket
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
