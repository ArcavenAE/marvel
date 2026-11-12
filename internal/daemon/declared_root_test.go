package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// A workspace.root the manifest declares and this host lacks is refused at apply
// and at plan, before the client's workspace_root parameter replaces it. The
// parameter is a directory that exists (the client's own), which is what hid the
// missing tree from the earlier check.
func declaredRootManifest(root string) string {
	return "workspace:\n  name: placed\n  root: " + root + "\nteams:\n  - name: crew\n    roles:\n      - name: crew\n        replicas: 0\n        runtime:\n          command: sleep\n          args: [\"300\"]\n"
}

func TestApplyRefusesAMissingDeclaredRoot(t *testing.T) {
	d := newHandlerDaemon(t)
	missing := filepath.Join(t.TempDir(), "absent-tree")
	resp := applyWithRoot(t, d, declaredRootManifest(missing), t.TempDir())
	if resp.Error == "" {
		t.Fatalf("apply went through (advisories %q), want a refusal naming %s", advisoriesOf(t, resp), missing)
	}
	if !strings.Contains(resp.Error, "workspace.root") || !strings.Contains(resp.Error, missing) {
		t.Errorf("error = %q, want it to name workspace.root and %s", resp.Error, missing)
	}
	if _, err := d.store.GetWorkspace("placed"); err == nil {
		t.Error("the workspace was stored although the apply was refused")
	}
}

func TestApplyAcceptsAnExistingDeclaredRoot(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyWithRoot(t, d, declaredRootManifest(t.TempDir()), t.TempDir()); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
}

func TestPrunePlanRefusesAMissingDeclaredRoot(t *testing.T) {
	d := pruneDaemon(t)
	missing := filepath.Join(t.TempDir(), "absent-tree")
	params, err := json.Marshal(map[string]any{
		"manifest_data":   []byte(declaredRootManifest(missing)),
		"manifest_source": pruneSrc,
		"workspace_root":  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := d.dispatchAs(daemonrpc.Request{Method: "prune_plan", Params: params}, caller{scope: ScopeAdmin, local: true})
	if resp.Error == "" || !strings.Contains(resp.Error, missing) {
		t.Fatalf("plan error = %q, want a refusal naming %s", resp.Error, missing)
	}
}

// marvel plan -f refuses a role workdir the daemon's host lacks, as apply does.
func TestPrunePlanRefusesAMissingRoleWorkdir(t *testing.T) {
	d := pruneDaemon(t)
	root := t.TempDir()
	missing := filepath.Join(root, "absent-tree")
	manifest := "workspace:\n  name: placed\nteams:\n  - name: crew\n    roles:\n      - name: builder\n        replicas: 0\n        workdir: " + missing + "\n        runtime:\n          command: sleep\n          args: [\"300\"]\n"
	params, err := json.Marshal(map[string]any{
		"manifest_data":   []byte(manifest),
		"manifest_source": pruneSrc,
		"workspace_root":  root,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := d.dispatchAs(daemonrpc.Request{Method: "prune_plan", Params: params}, caller{scope: ScopeAdmin, local: true})
	if resp.Error == "" || !strings.Contains(resp.Error, "builder") || !strings.Contains(resp.Error, missing) {
		t.Fatalf("plan error = %q, want a refusal naming role builder and %s", resp.Error, missing)
	}
}

// A plan of a manifest whose placement is sound still answers.
func TestPrunePlanStillAnswersWhenPlacementIsSound(t *testing.T) {
	d := pruneDaemon(t)
	root := t.TempDir()
	params, err := json.Marshal(map[string]any{
		"manifest_data":   []byte(declaredRootManifest(root)),
		"manifest_source": pruneSrc,
		"workspace_root":  root,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := d.dispatchAs(daemonrpc.Request{Method: "prune_plan", Params: params}, caller{scope: ScopeAdmin, local: true})
	if resp.Error != "" {
		t.Fatalf("plan: %s", resp.Error)
	}
}
