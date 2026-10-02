package daemon

import (
	"strings"
	"testing"
)

// The apply-time refusal of max-age on a role with several replicas (marvel#452)
// has a second door: `marvel scale`. A role applied at one replica with max-age
// must not be scaled above one, or it reaches the same drain of seats that were
// never asked for a handoff.
const scaleMaxAgeManifest = `
workspace:
  name: scalemax
teams:
  - name: crew
    roles:
      - name: aged
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
        shift:
          on: max-age
          max_age: 8h
      - name: pressured
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
        shift:
          on: context-pressure
          headroom_tokens: 5
`

func storedReplicas(t *testing.T, d *Daemon, role string) int {
	t.Helper()
	team, err := d.store.GetTeam("scalemax/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	for _, r := range team.Roles {
		if r.Name == role {
			return r.Replicas
		}
	}
	t.Fatalf("role %s not found", role)
	return -1
}

func TestScaleRefusesMaxAgeRoleAboveOneReplica(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, scaleMaxAgeManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	resp := d.handleScale(mustMarshal(t, scaleParams{TeamKey: "scalemax/crew", Role: "aged", Replicas: 3}))
	if resp.Error == "" || !strings.Contains(resp.Error, "max-age") || !strings.Contains(resp.Error, "replicas > 1") {
		t.Fatalf("scale to 3: error = %q, want a max-age refusal naming replicas > 1", resp.Error)
	}
	if got := storedReplicas(t, d, "aged"); got != 1 {
		t.Fatalf("stored replicas = %d after a refused scale, want 1", got)
	}
}

func TestScaleAllowsMaxAgeRoleToZeroAndOneAndPressureRoleAboveOne(t *testing.T) {
	skipIfNoTmux(t)
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, scaleMaxAgeManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	for _, c := range []struct {
		role string
		n    int
	}{{"aged", 0}, {"aged", 1}, {"pressured", 2}} {
		if resp := d.handleScale(mustMarshal(t, scaleParams{TeamKey: "scalemax/crew", Role: c.role, Replicas: c.n})); resp.Error != "" {
			t.Fatalf("scale %s to %d: %s; want it allowed", c.role, c.n, resp.Error)
		}
		if got := storedReplicas(t, d, c.role); got != c.n {
			t.Fatalf("stored replicas for %s = %d, want %d", c.role, got, c.n)
		}
	}
}
