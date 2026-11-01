package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// A role that declares runtime.goose on a runtime that is not goose applies, and
// the apply logs one line saying the table is ignored.
func TestApplyLogsTheGooseTableIgnoredAdvisory(t *testing.T) {
	_, sock, teardown := startTestDaemon(t, "test-goose-advisory")
	t.Cleanup(teardown)

	manifest := `
[workspace]
name = "test-goose-advisory-b"

[[team]]
name = "crew"

  [[team.role]]
  name = "sup"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]

    [team.role.runtime.goose]
    posture = "approve"
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
		if strings.Contains(line, `apply: team crew role sup: runtime "sleep" is not goose; runtime.goose is ignored`) {
			return
		}
	}
	t.Errorf("no goose advisory in %d log lines", len(result.Lines))
}
