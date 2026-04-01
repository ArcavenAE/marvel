package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
		os.Remove(sock)
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
		json.Unmarshal(resp.Result, &described)
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
	json.Unmarshal(resp.Result, &runResult)
	if runResult["status"] != "created" {
		t.Fatalf("expected status created, got %s", runResult["status"])
	}
	if runResult["session_key"] == "" {
		t.Fatal("expected session_key in run result")
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
}
