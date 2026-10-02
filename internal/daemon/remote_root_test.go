package daemon

import (
	"path/filepath"
	"strings"
	"testing"
)

const remoteRootManifest = `
[workspace]
name = "remoteroot"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`

// `marvel work` posts the client's own directory. When the daemon runs on
// another host that path does not exist there, and the apply must still go
// through: it warns and places nothing, as for an absent root.
func TestApplyWithAClientOnlyRootGoesThroughWithAWarning(t *testing.T) {
	d := newHandlerDaemon(t)
	clientRoot := filepath.Join(t.TempDir(), "only-on-the-client")
	params := mustMarshal(t, map[string]any{
		"manifest_data":  []byte(remoteRootManifest),
		"workspace_root": clientRoot,
	})
	resp := d.handleApply(params)
	if resp.Error != "" {
		t.Fatalf("apply refused a client-only root: %s", resp.Error)
	}
	if !strings.Contains(string(resp.Result), "does not exist on this host") {
		t.Errorf("result = %s, want a warning that the root does not exist on this host", resp.Result)
	}
	if ws, err := d.store.GetWorkspace("remoteroot"); err != nil || ws.Root != "" {
		t.Errorf("stored root = %q (%v), want none: nothing is placed", ws.Root, err)
	}
	if team, _ := d.store.GetTeam("remoteroot/squad"); team.WorkDir != "" {
		t.Errorf("team anchor = %q, want none", team.WorkDir)
	}
}
