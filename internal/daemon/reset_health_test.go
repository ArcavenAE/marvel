package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

// resetHealthManifest is a one-role team with no budget or healthcheck — the
// reset-health wiring test only needs a team the handler can resolve.
const resetHealthManifest = `
workspace:
  name: recover
teams:
  - name: crew
    roles:
      - name: worker
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`

// TestHandleResetHealth exercises the reset-health RPC wiring (aae-orc-fv3h):
// the handler resolves the team, validates the role, and reports whether a
// crash-loop record was cleared. The state-level assertion ("RestartCount 0
// and no backoff") lives on the controller method it calls,
// TestClearRoleHealthForRole; this test guards the daemon seam around it.
func TestHandleResetHealth(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, resetHealthManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}

	// A valid team/role with no accumulated health: succeeds, reports nothing
	// to clear (cleared=false), never errors.
	resp := d.handleResetHealth(mustMarshal(t, resetHealthParams{TeamKey: "recover/crew", Role: "worker"}))
	if resp.Error != "" {
		t.Fatalf("reset-health on a valid role errored: %s", resp.Error)
	}
	var res struct {
		Status  string `json:"status"`
		Cleared bool   `json:"cleared"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Status != "health_reset" {
		t.Errorf("status = %q, want health_reset", res.Status)
	}
	if res.Cleared {
		t.Errorf("cleared = true, want false (no crash-loop state recorded yet)")
	}

	// Unknown role in a known team is refused with a role-scoped message.
	resp = d.handleResetHealth(mustMarshal(t, resetHealthParams{TeamKey: "recover/crew", Role: "ghost"}))
	if resp.Error == "" || !strings.Contains(resp.Error, "role ghost not found") {
		t.Errorf("unknown-role error = %q, want it to name the missing role", resp.Error)
	}

	// Unknown team is refused before any role lookup.
	resp = d.handleResetHealth(mustMarshal(t, resetHealthParams{TeamKey: "recover/absent", Role: "worker"}))
	if resp.Error == "" || !strings.Contains(resp.Error, "recover/absent") {
		t.Errorf("unknown-team error = %q, want it to name the missing team", resp.Error)
	}

	// A missing --role is refused: the verb targets exactly one role.
	resp = d.handleResetHealth(mustMarshal(t, resetHealthParams{TeamKey: "recover/crew"}))
	if resp.Error == "" || !strings.Contains(resp.Error, "requires --role") {
		t.Errorf("missing-role error = %q, want it to require --role", resp.Error)
	}
}
