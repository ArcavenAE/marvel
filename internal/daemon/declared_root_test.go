package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// A workspace.root the manifest declares and this host lacks is refused at apply
// and at plan. The tests post what marvel work posts (cmd/marvel/workroot.go): an
// absolute declared root as the cleaned root itself, and a relative one joined to
// the manifest file's directory, which exists on the client. The earlier check
// dropped the posted root as client-only and accepted both with an advisory.
func declaredRootManifest(root string) string {
	return "workspace:\n  name: placed\n  root: " + root + "\nteams:\n  - name: crew\n    roles:\n      - name: crew\n        replicas: 0\n        runtime:\n          command: sleep\n          args: [\"300\"]\n"
}

func TestApplyRefusesAMissingDeclaredRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-tree")
	manifestDir := t.TempDir()
	for _, tc := range []struct{ name, declared, posted, named string }{
		{"absolute", missing, missing, missing},
		{"relative", "absent-tree", filepath.Join(manifestDir, "absent-tree"), filepath.Join(manifestDir, "absent-tree")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newHandlerDaemon(t)
			resp := applyWithRoot(t, d, declaredRootManifest(tc.declared), tc.posted)
			if resp.Error == "" {
				t.Fatalf("apply went through (advisories %q), want a refusal naming %s", advisoriesOf(t, resp), tc.named)
			}
			if !strings.Contains(resp.Error, "workspace.root") || !strings.Contains(resp.Error, tc.named) {
				t.Errorf("error = %q, want it to name workspace.root and %s", resp.Error, tc.named)
			}
			if !strings.Contains(resp.Error, "crew/crew") {
				t.Errorf("error = %q, want it to name the role placed from the root", resp.Error)
			}
			if _, err := d.store.GetWorkspace("placed"); err == nil {
				t.Error("the workspace was stored although the apply was refused")
			}
		})
	}
}

func TestApplyAcceptsAnExistingDeclaredRoot(t *testing.T) {
	d := newHandlerDaemon(t)
	root := t.TempDir()
	if resp := applyWithRoot(t, d, declaredRootManifest(root), root); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
}

func TestPrunePlanRefusesAMissingDeclaredRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-tree")
	manifestDir := t.TempDir()
	for _, tc := range []struct{ name, declared, posted, named string }{
		{"absolute", missing, missing, missing},
		{"relative", "absent-tree", filepath.Join(manifestDir, "absent-tree"), filepath.Join(manifestDir, "absent-tree")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pruneDaemon(t)
			params, err := json.Marshal(map[string]any{
				"manifest_data":   []byte(declaredRootManifest(tc.declared)),
				"manifest_source": pruneSrc,
				"workspace_root":  tc.posted,
			})
			if err != nil {
				t.Fatal(err)
			}
			resp := d.dispatchAs(daemonrpc.Request{Method: "prune_plan", Params: params}, caller{scope: ScopeAdmin, local: true})
			if resp.Error == "" || !strings.Contains(resp.Error, tc.named) {
				t.Fatalf("plan error = %q, want a refusal naming %s", resp.Error, tc.named)
			}
		})
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
