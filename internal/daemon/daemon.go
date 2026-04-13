// Package daemon provides the marvel daemon — a long-running process
// that manages sessions via tmux and serves CLI requests over Unix sockets,
// SSH tunnels, or (for advanced use) bare TCP sockets.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/team"
	"github.com/arcavenae/marvel/internal/tmux"
)

const (
	// DefaultSocket is the default Unix socket path.
	DefaultSocket = "/tmp/marvel.sock"
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

// Daemon is the marvel daemon.
type Daemon struct {
	store    *api.Store
	sessMgr  *session.Manager
	teamCtrl *team.Controller
	driver   *tmux.Driver
	listener net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// New creates a new daemon.
func New() (*Daemon, error) {
	driver, err := tmux.NewDriver()
	if err != nil {
		return nil, fmt.Errorf("init tmux driver: %w", err)
	}

	store := api.NewStore()
	sessMgr := session.NewManager(store, driver)
	teamCtrl := team.NewController(store, sessMgr)

	return &Daemon{
		store:    store,
		sessMgr:  sessMgr,
		teamCtrl: teamCtrl,
		driver:   driver,
	}, nil
}

// Start starts the daemon: listens on Unix or TCP socket and starts reconciliation.
// The address format determines the network: "host:port" for TCP, a file path
// for Unix. Examples: "/tmp/marvel.sock", "0.0.0.0:9090", ":9090".
func (d *Daemon) Start(socketPath string) error {
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

	// Print remote connection string for SSH access.
	if network == "unix" {
		if hostname, err := os.Hostname(); err == nil {
			user := os.Getenv("USER")
			if user != "" {
				log.Printf("remote access: --socket ssh://%s@%s%s", user, hostname, socketPath)
			}
		}
	}
	return nil
}

// Stop shuts down the daemon, cleaning up all resources.
func (d *Daemon) Stop() {
	if d.cancel != nil {
		d.cancel()
	}

	addr := ""
	if d.listener != nil {
		addr = d.listener.Addr().String()
		d.listener.Close()
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
	log.Println("marvel daemon stopped")
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeError(conn, fmt.Sprintf("decode request: %v", err))
		return
	}

	resp := d.dispatch(req)
	json.NewEncoder(conn).Encode(resp)
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
	default:
		return Response{Error: fmt.Sprintf("unknown method: %s", req.Method)}
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

func writeError(conn net.Conn, msg string) {
	resp := Response{Error: msg}
	json.NewEncoder(conn).Encode(resp)
}

// SendRequest sends a request to the daemon and returns the response.
//
// Address formats:
//
//	/tmp/marvel.sock                          → Unix socket (default)
//	ssh://user@host/tmp/marvel.sock           → SSH tunnel to Unix socket
//	ssh://user@host:22/tmp/marvel.sock        → SSH tunnel with explicit SSH port
//	ssh://host:9090                           → SSH tunnel to TCP port on remote
//	tcp://host:9090 or host:9090              → bare TCP (advanced use)
func SendRequest(socketPath string, req Request) (*Response, error) {
	conn, err := dialDaemon(socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &resp, nil
}

// dialDaemon connects to the daemon, routing through SSH if the address
// is an ssh:// URL.
func dialDaemon(addr string) (net.Conn, error) {
	if isSSH(addr) {
		return dialSSH(addr)
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

// dialSSH parses an ssh:// URL and dials the daemon's socket through an
// SSH tunnel. Auth uses the SSH agent (SSH_AUTH_SOCK) with fallback to
// common key files (~/.ssh/id_ed25519, ~/.ssh/id_rsa).
//
// URL formats:
//
//	ssh://user@host/path/to/socket        → SSH to host:22, dial Unix socket
//	ssh://user@host:2222/path/to/socket   → SSH to host:2222, dial Unix socket
//	ssh://user@host:9090                  → SSH to host:22, dial TCP localhost:9090
//	ssh://host/path/to/socket             → SSH as current user
func dialSSH(addr string) (net.Conn, error) {
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

	// Determine what to dial on the remote side.
	remotePath := u.Path
	var remoteNetwork, remoteAddr string

	if remotePath != "" && remotePath != "/" {
		// Path present → Unix socket on the remote.
		// Any port in the URL is the SSH port.
		remoteNetwork = "unix"
		remoteAddr = remotePath
		if sshPort == "" {
			sshPort = "22"
		}
	} else {
		// No path → the port is the daemon's TCP port on the remote.
		// SSH connects to port 22, then dials localhost:<port> on the remote.
		remoteNetwork = "tcp"
		if sshPort == "" {
			return nil, fmt.Errorf("ssh address %q: need a port (for remote TCP) or a path (for remote Unix socket)", addr)
		}
		remoteAddr = "localhost:" + sshPort
		sshPort = "22"
	}

	authMethods := sshAuthMethods()
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH auth available (set SSH_AUTH_SOCK or add keys to ~/.ssh/)")
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	sshConn, err := ssh.Dial("tcp", net.JoinHostPort(sshHost, sshPort), config)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s@%s:%s: %w", user, sshHost, sshPort, err)
	}

	conn, err := sshConn.Dial(remoteNetwork, remoteAddr)
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh tunnel %s %s via %s: %w", remoteNetwork, remoteAddr, sshHost, err)
	}

	return &sshWrappedConn{Conn: conn, sshClient: sshConn}, nil
}

// sshWrappedConn wraps an SSH-tunneled connection so that closing it
// also closes the underlying SSH client.
type sshWrappedConn struct {
	net.Conn
	sshClient *ssh.Client
}

func (c *sshWrappedConn) Close() error {
	err := c.Conn.Close()
	_ = c.sshClient.Close()
	return err
}

// sshAuthMethods returns available SSH authentication methods.
// Prefers the SSH agent, falls back to common key files.
func sshAuthMethods() []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	// SSH agent (most common for developers).
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Key file fallback.
	home, err := os.UserHomeDir()
	if err != nil {
		return methods
	}

	for _, name := range []string{"id_ed25519", "id_rsa"} {
		key, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	return methods
}
