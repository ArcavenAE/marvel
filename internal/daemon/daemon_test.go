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

	// Apply a manifest
	manifest := `
[workspace]
name = "test-daemon"

[[team]]
name = "workers"
replicas = 2

  [team.runtime]
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

	// Scale down
	resp, err = SendRequest(sock, Request{
		Method: "scale",
		Params: mustMarshal(t, map[string]any{"team_key": "test-daemon/workers", "replicas": 1}),
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
