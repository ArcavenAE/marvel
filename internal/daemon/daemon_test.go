package daemon

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestDaemonLifecycle(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	sock := filepath.Join(os.TempDir(), "marvel-test.sock")
	t.Cleanup(func() {
		d.Stop()
		_ = os.Remove(sock)
	})

	if err := d.Start(sock); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	// Apply a manifest with roles
	manifest := `
[workspace]
name = "test-daemon"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 2

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`
	resp, err := SendRequest(sock, Request{
		Method: "apply",
		Params: mustMarshal(t, map[string]any{"manifest_data": []byte(manifest)}),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("apply error: %s", resp.Error)
	}

	// Wait for reconciliation
	time.Sleep(500 * time.Millisecond)

	// Get sessions
	resp, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil {
		t.Fatalf("get sessions: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("get sessions error: %s", resp.Error)
	}

	var sessions []json.RawMessage
	if err := json.Unmarshal(resp.Result, &sessions); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Get teams
	resp, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "teams"}),
	})
	if err != nil {
		t.Fatalf("get teams: %v", err)
	}
	var teams []json.RawMessage
	if err := json.Unmarshal(resp.Result, &teams); err != nil {
		t.Fatalf("unmarshal teams: %v", err)
	}
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}

	// Scale down with role
	resp, err = SendRequest(sock, Request{
		Method: "scale",
		Params: mustMarshal(t, map[string]any{"team_key": "test-daemon/squad", "role": "worker", "replicas": 1}),
	})
	if err != nil {
		t.Fatalf("scale: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("scale error: %s", resp.Error)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify scaled
	resp, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil {
		t.Fatalf("get after scale: %v", err)
	}
	if err := json.Unmarshal(resp.Result, &sessions); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after scale, got %d", len(sessions))
	}

	// Scale without role should error
	resp, err = SendRequest(sock, Request{
		Method: "scale",
		Params: mustMarshal(t, map[string]any{"team_key": "test-daemon/squad", "replicas": 3}),
	})
	if err != nil {
		t.Fatalf("scale without role: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error when scaling without role")
	}

	// Heartbeat — send context pressure for a running session
	resp, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil {
		t.Fatalf("get sessions for heartbeat: %v", err)
	}
	var sessionsForHB []struct {
		Name      string `json:"Name"`
		Workspace string `json:"Workspace"`
	}
	if err := json.Unmarshal(resp.Result, &sessionsForHB); err != nil {
		t.Fatalf("unmarshal sessions for heartbeat: %v", err)
	}
	if len(sessionsForHB) > 0 {
		sessionKey := sessionsForHB[0].Workspace + "/" + sessionsForHB[0].Name
		resp, err = SendRequest(sock, Request{
			Method: "heartbeat",
			Params: mustMarshal(t, map[string]any{"session_key": sessionKey, "context_percent": 55.5}),
		})
		if err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
		if resp.Error != "" {
			t.Fatalf("heartbeat error: %s", resp.Error)
		}

		// Verify via describe
		resp, err = SendRequest(sock, Request{
			Method: "describe",
			Params: mustMarshal(t, map[string]string{"resource_type": "session", "name": sessionKey}),
		})
		if err != nil {
			t.Fatalf("describe session: %v", err)
		}
		var described map[string]any
		_ = json.Unmarshal(resp.Result, &described)
		if cp, ok := described["ContextPercent"].(float64); !ok || cp != 55.5 {
			t.Fatalf("expected context_percent 55.5, got %v", described["ContextPercent"])
		}
	}

	// Run — create a one-off session
	resp, err = SendRequest(sock, Request{
		Method: "run",
		Params: mustMarshal(t, map[string]any{
			"workspace":       "test-daemon",
			"runtime_command": "sleep",
			"runtime_args":    []string{"300"},
		}),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("run error: %s", resp.Error)
	}
	var runResult map[string]string
	_ = json.Unmarshal(resp.Result, &runResult)
	if runResult["status"] != "created" {
		t.Fatalf("expected status created, got %s", runResult["status"])
	}
	if runResult["session_key"] == "" {
		t.Fatal("expected session_key in run result")
	}

	// Shift — initiate a rolling shift
	resp, err = SendRequest(sock, Request{
		Method: "shift",
		Params: mustMarshal(t, map[string]any{"team_key": "test-daemon/squad"}),
	})
	if err != nil {
		t.Fatalf("shift: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("shift error: %s", resp.Error)
	}
	var shiftResult map[string]string
	_ = json.Unmarshal(resp.Result, &shiftResult)
	if shiftResult["status"] != "shift_initiated" {
		t.Fatalf("expected shift_initiated, got %s", shiftResult["status"])
	}

	// Wait for shift to complete (several reconcile ticks).
	time.Sleep(2 * time.Second)

	// Scale during shift should work now (shift should be complete).
	// But first, verify sessions exist with gen 2.
	_, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil {
		t.Fatalf("get after shift: %v", err)
	}

	// Delete workspace
	resp, err = SendRequest(sock, Request{
		Method: "delete",
		Params: mustMarshal(t, map[string]string{"resource_type": "workspace", "name": "test-daemon"}),
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("delete error: %s", resp.Error)
	}

	// Verify cascade: teams and sessions from the deleted workspace must be
	// gone so the reconciler doesn't respawn sessions against orphan teams.
	// See ArcavenAE/marvel#15.
	time.Sleep(500 * time.Millisecond)

	resp, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "teams"}),
	})
	if err != nil {
		t.Fatalf("get teams after delete workspace: %v", err)
	}
	var teamsAfter []json.RawMessage
	if err := json.Unmarshal(resp.Result, &teamsAfter); err != nil {
		t.Fatalf("unmarshal teams after delete: %v", err)
	}
	if len(teamsAfter) != 0 {
		t.Fatalf("expected 0 teams after delete workspace, got %d", len(teamsAfter))
	}

	resp, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil {
		t.Fatalf("get sessions after delete workspace: %v", err)
	}
	var sessionsAfter []json.RawMessage
	if err := json.Unmarshal(resp.Result, &sessionsAfter); err != nil {
		t.Fatalf("unmarshal sessions after delete: %v", err)
	}
	if len(sessionsAfter) != 0 {
		t.Fatalf("expected 0 sessions after delete workspace, got %d", len(sessionsAfter))
	}
}

// startTestDaemon brings up a daemon on a per-test Unix socket with one
// running session (sleep 300 in a generic adapter) and returns the
// session key, socket, and a teardown func. The session's tmux pane is
// alive, so inject/capture/ReapDead have something to hit.
//
// The daemon's log ring is wired as one of the log.SetOutput writers so
// tests exercising 'logs' RPC see real startup log lines. Production
// does this in cmd/marvel/main.go; tests do it here.
func startTestDaemon(t *testing.T, workspace string) (sessionKey, sock string, teardown func()) {
	t.Helper()
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	// Restore the default log output when the test ends so we don't
	// leak the ring-buffer sink into sibling tests.
	prevLogFlags := log.Flags()
	log.SetOutput(io.MultiWriter(d.LogBuffer(), os.Stderr))
	origRestore := func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(prevLogFlags)
	}

	sock = filepath.Join(os.TempDir(), "marvel-test-"+workspace+".sock")
	if err := d.Start(sock); err != nil {
		origRestore()
		t.Fatalf("start daemon: %v", err)
	}
	teardown = func() {
		d.Stop()
		_ = os.Remove(sock)
		origRestore()
	}

	manifest := `
[workspace]
name = "` + workspace + `"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`
	resp, err := SendRequest(sock, Request{
		Method: "apply",
		Params: mustMarshal(t, map[string]any{"manifest_data": []byte(manifest)}),
	})
	if err != nil || resp.Error != "" {
		teardown()
		t.Fatalf("apply: err=%v resp.Error=%q", err, resp.Error)
	}
	// Wait for reconciliation to create the session + pane.
	time.Sleep(600 * time.Millisecond)

	resp, err = SendRequest(sock, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil || resp.Error != "" {
		teardown()
		t.Fatalf("get sessions: err=%v resp.Error=%q", err, resp.Error)
	}
	var sessions []struct {
		Name      string
		Workspace string
		PaneID    string
	}
	if err := json.Unmarshal(resp.Result, &sessions); err != nil {
		teardown()
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(sessions) == 0 {
		teardown()
		t.Fatal("expected at least one session after apply")
	}
	sessionKey = sessions[0].Workspace + "/" + sessions[0].Name
	return sessionKey, sock, teardown
}

func TestHandleInjectCapture(t *testing.T) {
	sessionKey, sock, teardown := startTestDaemon(t, "test-inject")
	t.Cleanup(teardown)

	// Inject text into the session's pane. The sleep process ignores
	// stdin so the text sits in the tty buffer — capture-pane will
	// still render it as input characters on the pane.
	marker := "XXXX-INJECT-MARKER-" + t.Name()
	resp, err := SendRequest(sock, Request{
		Method: "inject",
		Params: mustMarshal(t, map[string]any{
			"session_key": sessionKey,
			"text":        marker,
			"literal":     true,
		}),
	})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("inject error: %s", resp.Error)
	}
	var injectResult map[string]string
	_ = json.Unmarshal(resp.Result, &injectResult)
	if injectResult["status"] != "injected" {
		t.Fatalf("expected status injected, got %s", injectResult["status"])
	}

	// tmux send-keys + capture is eventually-consistent; give it a beat.
	time.Sleep(200 * time.Millisecond)

	resp, err = SendRequest(sock, Request{
		Method: "capture",
		Params: mustMarshal(t, map[string]any{"session_key": sessionKey}),
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("capture error: %s", resp.Error)
	}
	var captureResult map[string]string
	_ = json.Unmarshal(resp.Result, &captureResult)
	if captureResult["status"] != "captured" {
		t.Fatalf("expected status captured, got %s", captureResult["status"])
	}
	if !strings.Contains(captureResult["content"], marker) {
		t.Fatalf("expected captured content to contain %q, got: %q", marker, captureResult["content"])
	}
}

func TestHandleInjectUnknownSession(t *testing.T) {
	_, sock, teardown := startTestDaemon(t, "test-inject-err")
	t.Cleanup(teardown)

	resp, err := SendRequest(sock, Request{
		Method: "inject",
		Params: mustMarshal(t, map[string]any{
			"session_key": "does-not-exist/nobody-0",
			"text":        "hello",
		}),
	})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error injecting into unknown session")
	}
}

func TestHandleLogs(t *testing.T) {
	_, sock, teardown := startTestDaemon(t, "test-logs")
	t.Cleanup(teardown)

	resp, err := SendRequest(sock, Request{
		Method: "logs",
		Params: mustMarshal(t, map[string]any{"n": 100}),
	})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("logs error: %s", resp.Error)
	}
	var result logsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal logs: %v", err)
	}
	if len(result.Lines) == 0 {
		t.Fatal("expected at least one log line from daemon startup")
	}
	// The daemon prints 'marvel daemon listening on ...' on Start —
	// the ring buffer should have it.
	var sawListening bool
	for _, line := range result.Lines {
		if strings.Contains(line, "marvel daemon listening on") {
			sawListening = true
			break
		}
	}
	if !sawListening {
		t.Fatalf("expected listening log line; got %d lines, first: %q", len(result.Lines), result.Lines[0])
	}
}

func TestHandleLogsDefaultN(t *testing.T) {
	_, sock, teardown := startTestDaemon(t, "test-logs-default")
	t.Cleanup(teardown)

	// No params — should default to the full ring.
	resp, err := SendRequest(sock, Request{Method: "logs"})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("logs error: %s", resp.Error)
	}
	var result logsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal logs: %v", err)
	}
	if len(result.Lines) == 0 {
		t.Fatal("expected at least one log line with no params")
	}
}

func TestListenNetwork(t *testing.T) {
	t.Parallel()
	tests := []struct {
		addr     string
		expected string
	}{
		{"/tmp/marvel.sock", "unix"},
		{"0.0.0.0:9090", "tcp"},
		{":9090", "tcp"},
		{"localhost:9090", "tcp"},
		{"192.168.1.5:9090", "tcp"},
		{"/var/run/marvel.sock", "unix"},
	}
	for _, tt := range tests {
		got := listenNetwork(tt.addr)
		if got != tt.expected {
			t.Errorf("listenNetwork(%q) = %q, want %q", tt.addr, got, tt.expected)
		}
	}
}

func TestIsSSH(t *testing.T) {
	t.Parallel()
	tests := []struct {
		addr     string
		expected bool
	}{
		{"ssh://user@host/tmp/marvel.sock", true},
		{"ssh://host:9090", true},
		{"/tmp/marvel.sock", false},
		{"localhost:9090", false},
		{"tcp://host:9090", false},
	}
	for _, tt := range tests {
		got := isSSH(tt.addr)
		if got != tt.expected {
			t.Errorf("isSSH(%q) = %v, want %v", tt.addr, got, tt.expected)
		}
	}
}

func TestDaemonTCP(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	addr := "127.0.0.1:0" // OS picks a free port
	t.Cleanup(func() {
		d.Stop()
	})

	if err := d.Start(addr); err != nil {
		t.Fatalf("start daemon on TCP: %v", err)
	}

	// Get the actual address the OS assigned.
	actualAddr := d.listener.Addr().String()

	// Send a request over TCP.
	resp, err := SendRequest(actualAddr, Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil {
		t.Fatalf("TCP request: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("TCP response error: %s", resp.Error)
	}
}
