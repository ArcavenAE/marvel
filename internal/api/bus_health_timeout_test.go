package api

import (
	"testing"
	"time"
)

// The ruled timeout for the bus healthcheck (aae-orc-z63a4) is one constant.
// A bus role that names no timeout gets it, a role that names one keeps its
// own, and a heartbeat role keeps the 30 s default it always had.
func TestBusHealthcheckDefaultTimeoutIsTheRuledConstant(t *testing.T) {
	t.Parallel()
	if BusHealthTimeout != 90*time.Second {
		t.Fatalf("BusHealthTimeout = %v, want 90s (three beats of the shim's 30 s timer, the TTL of its presence record)", BusHealthTimeout)
	}
	for _, tc := range []struct {
		name, typ, timeout string
		want               time.Duration
	}{
		{"bus, none named", "bus", "", BusHealthTimeout},
		{"bus, named", "bus", `timeout = "45s"`, 45 * time.Second},
		{"heartbeat, none named", "heartbeat", "", 30 * time.Second},
	} {
		m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "bash"

    [team.role.healthcheck]
    type = "` + tc.typ + `"
    ` + tc.timeout + `
`))
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("%s: apply: %v", tc.name, err)
		}
		team, _ := store.GetTeam("test/squad")
		hc := team.Roles[0].HealthCheck
		if hc == nil || hc.Timeout != tc.want {
			t.Errorf("%s: healthcheck %+v, want timeout %v", tc.name, hc, tc.want)
		}
	}
}
