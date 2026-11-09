package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// A role on opencode that declares a heartbeat healthcheck applies, and the
// apply logs one line saying opencode has no heartbeat feed. The control is the
// same manifest on a role that is not on opencode, which logs nothing of the kind.
func TestApplyLogsTheOpenCodeHeartbeatAdvisory(t *testing.T) {
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
  replicas = 1

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
}
