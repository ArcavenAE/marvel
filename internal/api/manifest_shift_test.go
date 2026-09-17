package api

import (
	"strings"
	"testing"
)

func TestParseManifestWithShiftPolicy(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"

    [team.role.shift]
    on = "context-pressure"
    headroom_tokens = 120000
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	role := team.Roles[0]
	if role.Shift == nil {
		t.Fatal("expected shift policy")
	}
	if role.Shift.On != ShiftTriggerContextPressure {
		t.Fatalf("on = %q, want %q", role.Shift.On, ShiftTriggerContextPressure)
	}
	if role.Shift.HeadroomTokens != 120000 {
		t.Fatalf("headroom_tokens = %d, want 120000", role.Shift.HeadroomTokens)
	}
}

func TestParseManifestShiftDefaultsOff(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	if team.Roles[0].Shift != nil {
		t.Fatal("a role with no shift block must default to nil (operator-shifted only)")
	}
}

func TestParseManifestShiftRejectsUnknownTrigger(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"

    [team.role.shift]
    on = "cpu"
    headroom_tokens = 1000
`))
	if err == nil {
		t.Fatal("expected an error for an unknown shift.on")
	}
	if !strings.Contains(err.Error(), "shift.on") {
		t.Fatalf("error should name shift.on, got: %v", err)
	}
}

func TestExampleAutoShiftManifestsParse(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"../../examples/auto-shift.toml", "../../examples/auto-shift.yaml"} {
		m, err := ParseManifest(p)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		role := m.Teams[0].Roles[0]
		if role.Shift == nil {
			t.Fatalf("%s: shift block did not parse", p)
		}
		if role.Shift.On != ShiftTriggerContextPressure || role.Shift.HeadroomTokens != 120000 {
			t.Fatalf("%s: shift block did not round-trip: %+v", p, role.Shift)
		}
	}
}

func TestParseManifestShiftRejectsNonPositiveHeadroom(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"

    [team.role.shift]
    on = "context-pressure"
    headroom_tokens = 0
`))
	if err == nil {
		t.Fatal("expected an error for headroom_tokens = 0")
	}
	if !strings.Contains(err.Error(), "headroom_tokens") {
		t.Fatalf("error should name headroom_tokens, got: %v", err)
	}
}
