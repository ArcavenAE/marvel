// Package daemon provides the marvel daemon — a long-running process
// that manages sessions via tmux and serves CLI requests over a Unix socket.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

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

// Start starts the daemon: listens on Unix socket and starts reconciliation.
func (d *Daemon) Start(socketPath string) error {
	// Remove stale socket.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", socketPath, err)
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

	log.Printf("marvel daemon listening on %s", socketPath)
	return nil
}

// Stop shuts down the daemon, cleaning up all resources.
func (d *Daemon) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	if d.listener != nil {
		d.listener.Close()
	}
	d.wg.Wait()

	// Clean up all workspaces.
	for _, ws := range d.store.ListWorkspaces() {
		if err := d.sessMgr.CleanupWorkspace(ws.Name); err != nil {
			log.Printf("cleanup workspace %s: %v", ws.Name, err)
		}
	}

	_ = os.Remove(DefaultSocket)
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
func SendRequest(socketPath string, req Request) (*Response, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s: %w", socketPath, err)
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
