package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// docs/design/host-health-guard.md section 4.4 item 1: apply logs the max-age
// advisory for an interactive single-replica role with no max-age trigger, and
// the apply itself still succeeds.
func TestApplyLogsTheMaxAgeAdvisory(t *testing.T) {
	_, sock, teardown := startTestDaemon(t, "test-maxage-advisory")
	t.Cleanup(teardown)

	manifest := `
[workspace]
name = "test-maxage-advisory-b"

[[team]]
name = "crew"

  [[team.role]]
  name = "sup"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`
	resp, err := daemonrpc.SendRequest(sock, daemonrpc.Request{
		Method: "apply",
		Params: mustMarshal(t, map[string]any{"manifest_data": []byte(manifest)}),
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
	for _, line := range result.Lines {
		if strings.Contains(line, "apply: team crew role sup: interactive single-replica role declares no max-age shift trigger") {
			return
		}
	}
	t.Errorf("no max-age advisory in %d log lines", len(result.Lines))
}
