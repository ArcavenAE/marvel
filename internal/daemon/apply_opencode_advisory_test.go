package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// A role on opencode that declares a heartbeat healthcheck applies, and the
// apply logs one line saying opencode has no heartbeat feed. The control is the
// same manifest on a role that is not on opencode, which logs nothing of the kind.
//
// Both roles have replicas = 0, so the apply starts no session, and a stub
// opencode is first on PATH so the apply's command pre-flight passes where no
// opencode is installed and a real one is never reached. The test checks that
// the stub is the binary found, that nothing called it, and that no session
// exists.
func TestApplyLogsTheOpenCodeHeartbeatAdvisory(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls.log")
	stub := "#!/bin/sh\necho \"$@\" >> " + calls + "\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if found, err := exec.LookPath("opencode"); err != nil || found != filepath.Join(bin, "opencode") {
		t.Fatalf("opencode resolves to %q (%v), want the stub %q", found, err, filepath.Join(bin, "opencode"))
	}

	_, sock, teardown := startTestDaemon(t, "test-opencode-advisory")
	t.Cleanup(teardown)

	manifest := func(ws, team, role, command string) string {
		return `
[workspace]
name = "` + ws + `"

[[team]]
name = "` + team + `"

  [[team.role]]
  name = "` + role + `"
  replicas = 0

    [team.role.runtime]
    command = "` + command + `"
    args = ["300"]

    [team.role.healthcheck]
    type = "heartbeat"
`
	}
	apply := func(m string) []string {
		t.Helper()
		resp, err := daemonrpc.SendRequest(sock, daemonrpc.Request{
			Method: "apply",
			Params: mustMarshal(t, map[string]any{"manifest_data": []byte(m)}),
		})
		if err != nil || resp.Error != "" {
			t.Fatalf("apply must not be refused: err=%v resp.Error=%q", err, resp.Error)
		}
		resp, err = daemonrpc.SendRequest(sock, daemonrpc.Request{Method: "logs"})
		if err != nil || resp.Error != "" {
			t.Fatalf("logs: err=%v resp.Error=%q", err, resp.Error)
		}
		var result logsResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal logs: %v", err)
		}
		return result.Lines
	}
	has := func(lines []string, want string) bool {
		for _, l := range lines {
			if strings.Contains(l, want) {
				return true
			}
		}
		return false
	}

	other := apply(manifest("test-opencode-advisory-b", "crewb", "other", "sleep"))
	if has(other, "opencode has no heartbeat feed") {
		t.Error("the advisory was logged for a role that is not on opencode")
	}
	lines := apply(manifest("test-opencode-advisory-c", "crewc", "sup", "opencode"))
	if !has(lines, `apply: team crewc role sup: opencode has no heartbeat feed, so healthcheck type "heartbeat" is not fed; advisory only`) {
		t.Errorf("no opencode advisory in %d log lines", len(lines))
	}

	// Nothing started: no session of either role exists, and the stub, which is
	// the only opencode the apply could reach, was never called.
	resp, err := daemonrpc.SendRequest(sock, daemonrpc.Request{
		Method: "get",
		Params: mustMarshal(t, map[string]string{"resource_type": "sessions"}),
	})
	if err != nil || resp.Error != "" {
		t.Fatalf("get sessions: err=%v resp.Error=%q", err, resp.Error)
	}
	for _, name := range []string{"crewb-other", "crewc-sup"} {
		if strings.Contains(string(resp.Result), name) {
			t.Errorf("a session of %s exists, but both roles have replicas = 0", name)
		}
	}
	if b, err := os.ReadFile(calls); err == nil && len(b) > 0 {
		t.Errorf("the stub opencode was called: %q", b)
	}
}
