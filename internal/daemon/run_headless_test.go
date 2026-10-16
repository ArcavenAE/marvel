package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// runWith sends a run request as the CLI would, from loose params so the test
// states the wire names and not the Go field names.
func runWith(t *testing.T, d *Daemon, p map[string]any) Response {
	t.Helper()
	if _, ok := p["runtime_command"]; !ok {
		p["runtime_command"] = "sleep"
		p["runtime_args"] = []string{"300"}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return d.handleRun(raw)
}

func ranSession(t *testing.T, d *Daemon, resp Response) api.Session {
	t.Helper()
	if resp.Error != "" {
		t.Fatalf("run: %s", resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	sess, err := d.store.GetSession(out["session_key"])
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// A run can ask for a headless launch with its prompt and an environment, and
// the session records them in its runtime.
func TestRunCarriesModePromptAndEnv(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := ranSession(t, d, runWith(t, d, map[string]any{
		"workspace": "runh1", "mode": "headless", "prompt": "do the thing",
		"env": map[string]string{"PROBE_BRANCH_KEY": "b1", "OTHER": "x"},
	}))
	rt := sess.Runtime
	if rt.Mode != api.RuntimeModeHeadless || rt.Prompt != "do the thing" {
		t.Errorf("runtime mode %q prompt %q, want headless and the prompt", rt.Mode, rt.Prompt)
	}
	if rt.Env["PROBE_BRANCH_KEY"] != "b1" || rt.Env["OTHER"] != "x" {
		t.Errorf("runtime env = %v, want both entries", rt.Env)
	}
}

// A headless run with no prompt is refused before a session exists, because a
// harness with no prompt reads a tty nobody types into.
func TestRunRefusesHeadlessWithoutAPrompt(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := runWith(t, d, map[string]any{"workspace": "runh2", "mode": "headless"})
	if resp.Error == "" || !strings.Contains(resp.Error, "prompt") {
		t.Fatalf("error = %q, want a refusal naming the missing prompt", resp.Error)
	}
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "runh2" {
			t.Errorf("a refused run left session %s behind", s.Key())
		}
	}
}

// A mode other than headless is refused rather than ignored.
func TestRunRefusesAnUnknownMode(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := runWith(t, d, map[string]any{"workspace": "runh3", "mode": "batch", "prompt": "p"})
	if resp.Error == "" || !strings.Contains(resp.Error, "mode") {
		t.Fatalf("error = %q, want a refusal naming the mode", resp.Error)
	}
}

// --name fixes the session key, and a second run under the same name does not
// start a second seat: the store refuses it and the reply says "already exists".
func TestRunNamesTheSessionAndRefusesAReusedName(t *testing.T) {
	d := newHandlerDaemon(t)
	first := ranSession(t, d, runWith(t, d, map[string]any{"workspace": "runh4", "name": "branch-7"}))
	if first.Key() != "runh4/branch-7" {
		t.Fatalf("key = %q, want runh4/branch-7", first.Key())
	}
	resp := runWith(t, d, map[string]any{"workspace": "runh4", "name": "branch-7"})
	if resp.Error == "" || !strings.Contains(resp.Error, "already exists") {
		t.Fatalf("error = %q, want an already-exists refusal", resp.Error)
	}
	n := 0
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "runh4" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d sessions in the workspace after a reused name, want 1", n)
	}
}

// A name that would change the key's shape is refused.
func TestRunRefusesAnUnsafeName(t *testing.T) {
	d := newHandlerDaemon(t)
	for _, name := range []string{"a/b", "has space", "-lead", "../x"} {
		resp := runWith(t, d, map[string]any{"workspace": "runh5", "name": name})
		if resp.Error == "" || !strings.Contains(resp.Error, "name") {
			t.Errorf("name %q: error = %q, want a refusal naming the name", name, resp.Error)
		}
	}
}

const branchRunManifest = `
workspace:
  name: runh6
teams:
  - name: arm
    budget:
      max_sessions: 2
    roles:
      - name: orchestrator
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`

// A named run into a declared team under a role the team does not declare is
// still admitted against the team's budget.
func TestNamedRunIntoAnUndeclaredRoleIsAdmittedByBudget(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, branchRunManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	d.teamCtrl.ReconcileOnce()
	ranSession(t, d, runWith(t, d, map[string]any{
		"workspace": "runh6", "team": "arm", "role": "branch-run", "name": "b1",
	}))
	resp := runWith(t, d, map[string]any{
		"workspace": "runh6", "team": "arm", "role": "branch-run", "name": "b2",
	})
	if resp.Error == "" || !strings.Contains(resp.Error, "max_sessions=2") {
		t.Fatalf("error = %q, want the budget refusal for a third live session", resp.Error)
	}
}

// Such a run is neither shed nor refilled by the controller: ten ticks later
// the session is still there, still one, and the declared role is unchanged.
func TestNamedRunIntoAnUndeclaredRoleSurvivesTheController(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, branchRunManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	d.teamCtrl.ReconcileOnce()
	sess := ranSession(t, d, runWith(t, d, map[string]any{
		"workspace": "runh6", "team": "arm", "role": "branch-run", "name": "b1",
	}))
	for i := 0; i < 10; i++ {
		d.teamCtrl.ReconcileOnce()
	}
	if _, err := d.store.GetSession(sess.Key()); err != nil {
		t.Fatalf("the named run was shed: %v", err)
	}
	var orch, branch int
	for _, s := range d.store.ListSessionsByTeam("runh6", "arm") {
		switch s.Role {
		case "orchestrator":
			orch++
		case "branch-run":
			branch++
		}
	}
	if orch != 1 || branch != 1 {
		t.Errorf("after ten ticks: %d orchestrator, %d branch-run; want 1 and 1", orch, branch)
	}
}
