package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/session"
)

// globalRoleDeclManifest declares a role global. sleep is on every PATH, so the
// manifest passes the runtime pre-flight and any refusal is the guard's.
const globalRoleDeclManifest = `
[workspace]
name = "grole"

[[team]]
name = "grole-ops"

  [[team.role]]
  name = "research-supervisor"
  replicas = 0
  global_role = "supervisor"

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`

// adopting stands in for a cluster bus with a fixed admitted set.
type adopting struct{ admitted []string }

func (adopting) URL() string                                      { return "nats://127.0.0.1:4222" }
func (adopting) Credential(string, string) (string, string, bool) { return "", "", false }
func (a adopting) GlobalRole(r api.Role) string {
	return config.ResolvedGlobalRole(r, a.admitted)
}

var _ session.BusEnv = adopting{}

// Test 9: a role that declares the global tier on a cluster that does not admit
// its name is refused at apply, naming the role and the config key, and nothing
// is stored. The same manifest applies once the name is admitted. Admission is
// also checked at every render (the guard); this is the early, loud half.
func TestApplyRefusesAnUnadmittedGlobalRole(t *testing.T) {
	d := newHandlerDaemon(t)
	d.sessMgr.Bus = adopting{}

	resp := applyManifest(t, d, globalRoleDeclManifest)
	if resp.Error == "" {
		t.Fatal("apply accepted a global role the cluster does not admit")
	}
	for _, want := range []string{"research-supervisor", "global_roles"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q does not name %q", resp.Error, want)
		}
	}
	if len(d.store.ListTeams()) != 0 || len(d.store.ListWorkspaces()) != 0 {
		t.Errorf("a refused apply stored %d teams and %d workspaces", len(d.store.ListTeams()), len(d.store.ListWorkspaces()))
	}

	d.sessMgr.Bus = adopting{admitted: []string{"research-supervisor"}}
	if resp := applyManifest(t, d, globalRoleDeclManifest); resp.Error != "" {
		t.Fatalf("apply refused once the name is admitted: %s", resp.Error)
	}
	teams := d.store.ListTeams()
	if len(teams) != 1 || teams[0].Roles[0].GlobalRole != api.GlobalRoleSupervisor {
		t.Errorf("stored teams = %+v, want the declaration kept", teams)
	}
}

// A role that declares "none", declares nothing, or is the supervisor needs no
// admission, so every manifest that applies today still applies.
func TestApplyNeedsNoAdmissionForTheDefaultsAndOptOut(t *testing.T) {
	d := newHandlerDaemon(t)
	d.sessMgr.Bus = adopting{}
	cases := []struct{ ws, role, decl string }{
		{"gnone", "research-supervisor", `global_role = "none"`},
		{"gunset", "research-supervisor", ""},
		{"gsup", "supervisor", `global_role = "supervisor"`},
	}
	for _, tc := range cases {
		manifest := strings.Replace(globalRoleDeclManifest, `global_role = "supervisor"`, tc.decl, 1)
		manifest = strings.Replace(manifest, "research-supervisor", tc.role, 1)
		manifest = strings.ReplaceAll(manifest, "grole", tc.ws)
		if resp := applyManifest(t, d, manifest); resp.Error != "" {
			t.Errorf("%s %q refused: %s", tc.role, tc.decl, resp.Error)
		}
	}
}

// With no bus at all there is no global tier to widen, so a declaration is
// inert there and a manifest written for a fleet still applies on a laptop.
func TestApplyWithNoBusLeavesTheDeclarationInert(t *testing.T) {
	d := newHandlerDaemon(t)
	d.sessMgr.Bus = nil
	if resp := applyManifest(t, d, globalRoleDeclManifest); resp.Error != "" {
		t.Fatalf("apply refused with no bus: %s", resp.Error)
	}
}
