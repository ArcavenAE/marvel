package daemon

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#335: a role declared at replicas 0 is parked. The apply succeeds,
// the role is stored at desired 0 beside its running sibling, and nothing
// spawns for it, so the manifest can record what marvel scale can set.
func TestApplyParkedRoleIsStoredAndSpawnsNothing(t *testing.T) {
	const manifest = `
workspace:
  name: parked
teams:
  - name: crew
    roles:
      - name: worker
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
      - name: spare
        replicas: 0
        runtime:
          command: sleep
          args: ["300"]
`
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, manifest); resp.Error != "" {
		t.Fatalf("apply with a parked role failed: %s", resp.Error)
	}
	team, err := d.store.GetTeam("parked/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	var found bool
	for _, r := range team.Roles {
		if r.Name == "spare" {
			found = true
			if r.Replicas != 0 {
				t.Fatalf("spare desired = %d, want 0", r.Replicas)
			}
		}
	}
	if !found {
		t.Fatal("the parked role was not stored")
	}
	for _, s := range d.store.ListSessionsByTeam("parked", "crew") {
		if s.Role == "spare" && api.CountAlive([]api.Session{s}) > 0 {
			t.Fatalf("a parked role spawned session %s", s.Key())
		}
	}
}
