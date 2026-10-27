package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// Placement at apply (docs/design/session-working-directory.md, marvel#255).
// Roles are parked (replicas 0) so these need no tmux.

const workDirManifestYAML = `
workspace:
  name: placed
teams:
  - name: crew
    workdir: sub
    roles:
      - name: crew
        replicas: 0
        runtime:
          command: sleep
          args: ["300"]
`

// applyWithRoot posts a manifest with the workspace_root param the CLI sends.
func applyWithRoot(t *testing.T, d *Daemon, manifest, root string) daemonrpc.Response {
	t.Helper()
	m := map[string]any{"manifest_data": []byte(manifest)}
	if root != "" {
		m["workspace_root"] = root
	}
	params, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal apply params: %v", err)
	}
	return d.handleApply(params)
}

func advisoriesOf(t *testing.T, resp daemonrpc.Response) []string {
	t.Helper()
	var r struct {
		Advisories []string `json:"advisories"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatalf("decode apply result: %v", err)
	}
	return r.Advisories
}

// The workspace_root param is the absolute root marvel work computed; the
// daemon resolves the relative team workdir against it and stores both.
func TestApplyUsesTheWorkspaceRootParam(t *testing.T) {
	d := newHandlerDaemon(t)
	// Apply stores the root with symlinks resolved (macOS temp directories
	// sit behind a link), so the expectation is the resolved form.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if resp := applyWithRoot(t, d, workDirManifestYAML, root); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	ws, err := d.store.GetWorkspace("placed")
	if err != nil || ws.Root != root {
		t.Fatalf("stored root = %q (%v), want %q", ws.Root, err, root)
	}
	team, err := d.store.GetTeam("placed/crew")
	if err != nil || team.WorkDir != filepath.Join(root, "sub") {
		t.Fatalf("stored team workdir = %q (%v), want %q", team.WorkDir, err, filepath.Join(root, "sub"))
	}
}

// With no root anywhere the apply still succeeds, says so, and declares no
// placement: running fleet tooling must not break on an upgrade. The hard
// refusal follows once every caller sends a root.
func TestApplyWithoutRootWarnsAndPlacesNothing(t *testing.T) {
	d := newHandlerDaemon(t)
	manifest := strings.Replace(workDirManifestYAML, "    workdir: sub\n", "", 1)
	resp := applyWithRoot(t, d, manifest, "")
	if resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	if adv := strings.Join(advisoriesOf(t, resp), "; "); !strings.Contains(adv, "workspace.root") {
		t.Fatalf("advisories = %q, want one naming workspace.root", adv)
	}
	if ws, err := d.store.GetWorkspace("placed"); err != nil || ws.Root != "" {
		t.Fatalf("stored root = %q (%v), want empty", ws.Root, err)
	}
}

// What the daemon cannot place it refuses, and refuses before anything is
// stored.
func TestApplyRefusesWhatItCannotPlace(t *testing.T) {
	root := t.TempDir() // "sub" is never created under it
	cases := []struct {
		name, root, manifest, want string
	}{
		{"relative root", "rel/root", workDirManifestYAML, "not absolute"},
		{"tilde root", "~/proj", workDirManifestYAML, "not absolute"},
		{"root that does not exist", filepath.Join(root, "gone"), workDirManifestYAML, "does not exist"},
		{"team workdir that does not exist", root, workDirManifestYAML, "does not exist"},
	}
	for _, tc := range cases {
		d := newHandlerDaemon(t)
		resp := applyWithRoot(t, d, tc.manifest, tc.root)
		if resp.Error == "" || !strings.Contains(resp.Error, tc.want) {
			t.Errorf("%s: error = %q, want it to contain %q", tc.name, resp.Error, tc.want)
		}
		if _, err := d.store.GetTeam("placed/crew"); err == nil {
			t.Errorf("%s: the team was stored by a refused apply", tc.name)
		}
	}
}

// describe team shows a role whose spawn is refused for its directory, with the
// path and the reason, so an operator is not left reading daemon logs.
func TestDescribeTeamShowsAPlacementRefusal(t *testing.T) {
	d := newHandlerDaemon(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(strings.Replace(workDirManifestYAML, "replicas: 0", "replicas: 1", 1), "name: placed", "name: described", 1)
	// Apply checks the directory, so it exists then and is removed after: the
	// shape of a worktree deleted under a running team.
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if resp := applyWithRoot(t, d, manifest, root); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	d.teamCtrl.ReconcileOnce()
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "described" {
			_ = d.sessMgr.Delete(s.Key())
		}
	}
	if err := os.Remove(sub); err != nil {
		t.Fatal(err)
	}
	d.teamCtrl.ReconcileOnce()

	out := describeTeam(t, d, "described/crew")
	conds, _ := out["Conditions"].([]any)
	if len(conds) != 1 {
		t.Fatalf("Conditions = %v, want one", out["Conditions"])
	}
	c, _ := conds[0].(map[string]any)
	if c["role"] != "crew" || c["type"] != "PlacementRefused" || c["status"] != "True" {
		t.Errorf("condition = %v, want role crew, type PlacementRefused, status True", c)
	}
	if msg, _ := c["message"].(string); !strings.Contains(msg, filepath.Join(root, "sub")) || !strings.Contains(msg, "does not exist") {
		t.Errorf("message = %q, want the directory and the reason", msg)
	}

	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	d.teamCtrl.ReconcileOnce()
	if again := describeTeam(t, d, "described/crew"); again["Conditions"] != nil {
		t.Errorf("Conditions after the directory returned = %v, want none", again["Conditions"])
	}
}

// A manifest that names no root, applied from another directory, moves the
// stored root to where the file sits; the apply says so, with both paths
// (#1086). The same directory again, or a root the manifest names, is quiet.
func TestApplyWarnsWhenADerivedRootMovesTheStoredOne(t *testing.T) {
	d := newHandlerDaemon(t)
	manifest := strings.Replace(workDirManifestYAML, "    workdir: sub\n", "", 1)
	// The params carry the unresolved temp paths, as marvel work would; the
	// stored root is resolved, so the comparison must be too.
	homeRaw, stagingRaw := t.TempDir(), t.TempDir()
	home, err := filepath.EvalSymlinks(homeRaw)
	if err != nil {
		t.Fatal(err)
	}
	staging, err := filepath.EvalSymlinks(stagingRaw)
	if err != nil {
		t.Fatal(err)
	}
	if resp := applyWithRoot(t, d, manifest, homeRaw); resp.Error != "" {
		t.Fatalf("first apply: %s", resp.Error)
	}
	resp := applyWithRoot(t, d, manifest, homeRaw)
	if resp.Error != "" {
		t.Fatalf("re-apply from home: %s", resp.Error)
	}
	if adv := strings.Join(advisoriesOf(t, resp), "; "); strings.Contains(adv, "moves") {
		t.Errorf("re-apply from the same directory warned: %q", adv)
	}
	resp = applyWithRoot(t, d, manifest, stagingRaw)
	if resp.Error != "" {
		t.Fatalf("apply from staging: %s", resp.Error)
	}
	adv := strings.Join(advisoriesOf(t, resp), "; ")
	if !strings.Contains(adv, home) || !strings.Contains(adv, staging) {
		t.Errorf("advisories = %q, want one naming %s and %s", adv, home, staging)
	}
	named := strings.Replace(manifest, "  name: placed\n", "  name: placed\n  root: "+home+"\n", 1)
	resp = applyWithRoot(t, d, named, home)
	if resp.Error != "" {
		t.Fatalf("apply with a named root: %s", resp.Error)
	}
	if adv := strings.Join(advisoriesOf(t, resp), "; "); strings.Contains(adv, "moves") {
		t.Errorf("a root the manifest names warned: %q", adv)
	}
}
