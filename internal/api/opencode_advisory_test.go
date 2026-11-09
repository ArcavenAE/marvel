package api

import (
	"reflect"
	"testing"
)

func TestOpenCodeAdvisories(t *testing.T) {
	t.Parallel()
	role := func(name, image string, hc *ManifestHealthCheck, window int) ManifestRole {
		return ManifestRole{
			Name: name, Replicas: 1, HealthCheck: hc,
			Runtime: ManifestRuntime{Image: image, Command: image, ContextWindow: window},
		}
	}
	beat := &ManifestHealthCheck{Type: "heartbeat"}
	m := &Manifest{Teams: []ManifestTeam{{Name: "crew", Roles: []ManifestRole{
		role("beating", "opencode", beat, 0),
		role("windowed", "opencode", nil, 64000),
		role("both", "opencode", beat, 32000),
		role("alive", "opencode", &ManifestHealthCheck{Type: "process-alive"}, 0),
		role("plain", "opencode", nil, 0),
		role("claude-beating", "claude", beat, 64000),
	}}}}
	want := []string{
		`team crew role beating: opencode has no heartbeat feed, so healthcheck type "heartbeat" is not fed; advisory only`,
		`team crew role windowed: runtime.context_window on opencode replaces the model catalog the seat would read its window from; advisory only`,
		`team crew role both: opencode has no heartbeat feed, so healthcheck type "heartbeat" is not fed; advisory only`,
		`team crew role both: runtime.context_window on opencode replaces the model catalog the seat would read its window from; advisory only`,
	}
	if got := m.OpenCodeAdvisories(); !reflect.DeepEqual(got, want) {
		t.Errorf("advisories =\n%q\nwant\n%q", got, want)
	}
}
