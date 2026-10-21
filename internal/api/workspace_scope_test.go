package api

import (
	"strings"
	"testing"
)

// A fixture manifest; no real workspace is named.
const scopeManifest = `
[workspace]
name = "scoped"
%s

[[team]]
name = "scoped-team"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`

func scopedManifest(t *testing.T, line string) (*Manifest, error) {
	t.Helper()
	return ParseManifestBytes([]byte(strings.Replace(scopeManifest, "%s", line, 1)))
}

// A workspace may declare scope = "client" and nothing else.
func TestWorkspaceScopeParses(t *testing.T) {
	m, err := scopedManifest(t, `scope = "client"`)
	if err != nil {
		t.Fatalf("client scope: %v", err)
	}
	if m.Workspace.Scope != WorkspaceScopeClient {
		t.Fatalf("scope = %q", m.Workspace.Scope)
	}
	if _, err := scopedManifest(t, `scope = "customer"`); err == nil || !strings.Contains(err.Error(), "workspace.scope") {
		t.Fatalf("an unknown scope: err = %v, want a workspace.scope refusal", err)
	}
	if m, err := scopedManifest(t, ""); err != nil || m.Workspace.Scope != "" {
		t.Fatalf("no scope: %v", err)
	}
}

// Apply stores the scope, and a later apply that names none leaves it, so a
// manifest edit cannot quietly open a client workspace's panes.
func TestWorkspaceScopeApplyKeepsAStoredScope(t *testing.T) {
	st := NewStore()
	m, err := scopedManifest(t, `scope = "client"`)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(st); err != nil {
		t.Fatal(err)
	}
	if ws, err := st.GetWorkspace("scoped"); err != nil || ws.Scope != WorkspaceScopeClient {
		t.Fatalf("after apply: %+v, %v", ws, err)
	}
	again, err := scopedManifest(t, `root = "/tmp/scoped"`)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Apply(st); err != nil {
		t.Fatal(err)
	}
	if ws, err := st.GetWorkspace("scoped"); err != nil || ws.Scope != WorkspaceScopeClient {
		t.Fatalf("after a re-apply naming no scope: %+v, %v", ws, err)
	}
}

// A re-apply whose only change is adding scope = "client" to a workspace that
// exists stores it: marking a known client workspace is the rollout step, and
// it is a manifest edit of exactly this shape.
func TestWorkspaceScopeReapplyAddsTheClientScope(t *testing.T) {
	st := NewStore()
	m, err := scopedManifest(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(st); err != nil {
		t.Fatal(err)
	}
	if ws, err := st.GetWorkspace("scoped"); err != nil || ws.Scope != "" {
		t.Fatalf("after an apply naming no scope: %+v, %v", ws, err)
	}
	marked, err := scopedManifest(t, `scope = "client"`)
	if err != nil {
		t.Fatal(err)
	}
	if err := marked.Apply(st); err != nil {
		t.Fatal(err)
	}
	if ws, err := st.GetWorkspace("scoped"); err != nil || ws.Scope != WorkspaceScopeClient {
		t.Fatalf("after a re-apply adding the client scope: %+v, %v", ws, err)
	}
}
