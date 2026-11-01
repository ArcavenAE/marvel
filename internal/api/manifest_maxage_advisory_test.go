package api

import (
	"strings"
	"testing"
)

// docs/design/host-health-guard.md section 4.4 item 1: apply says which
// single-replica interactive roles declare no max-age shift trigger, as an
// advisory and never a refusal. A role with more than one replica cannot
// declare max-age today (marvel#452), so it is not reported.

func maxAgeRole(replicas int, mode RuntimeMode, shift *ManifestShift) ManifestRole {
	return ManifestRole{
		Name:     "sup",
		Replicas: replicas,
		Runtime:  ManifestRuntime{Image: "claude", Command: "claude", Mode: mode},
		Shift:    shift,
	}
}

func TestMaxAgeAdvisories(t *testing.T) {
	t.Parallel()
	const line = `team crew role sup: interactive single-replica role declares no max-age shift trigger, so a seat of it runs until something else ends it; advisory only (marvel#452 covers roles with more than one replica)`
	tests := []struct {
		name  string
		roles []ManifestRole
		want  []string
	}{
		{
			name:  "interactive, one replica, no shift block is reported",
			roles: []ManifestRole{maxAgeRole(1, RuntimeModeInteractive, nil)},
			want:  []string{line},
		},
		{
			name:  "a shift block with another trigger is reported",
			roles: []ManifestRole{maxAgeRole(1, RuntimeModeInteractive, &ManifestShift{On: ShiftTriggerContextPressure, HeadroomTokens: 1000})},
			want:  []string{line},
		},
		{
			name:  "a shift block with no trigger is reported",
			roles: []ManifestRole{maxAgeRole(1, RuntimeModeInteractive, &ManifestShift{Handoff: "/h/{session}.md"})},
			want:  []string{line},
		},
		{
			name:  "the single form max-age says nothing",
			roles: []ManifestRole{maxAgeRole(1, RuntimeModeInteractive, &ManifestShift{On: ShiftTriggerMaxAge, MaxAge: "72h"})},
		},
		{
			name: "max-age inside the list form says nothing",
			roles: []ManifestRole{maxAgeRole(1, RuntimeModeInteractive, &ManifestShift{Any: []ManifestShiftCondition{
				{On: ShiftTriggerContextPressure, HeadroomTokens: 1000},
				{On: ShiftTriggerMaxAge, MaxAge: "72h"},
			}})},
		},
		{
			name:  "a headless role is never reported",
			roles: []ManifestRole{maxAgeRole(1, RuntimeModeHeadless, nil)},
		},
		{
			name:  "two replicas are not reported",
			roles: []ManifestRole{maxAgeRole(2, RuntimeModeInteractive, nil)},
		},
		{
			name:  "a parked role is not reported",
			roles: []ManifestRole{maxAgeRole(0, RuntimeModeInteractive, nil)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{Teams: []ManifestTeam{{Name: "crew", Roles: tt.roles}}}
			got := m.MaxAgeAdvisories()
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("advisories =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
