package api

import (
	"strings"
	"testing"
)

// marvel#391, docs/design/interactive-first-prompt.md T5 (D6): apply says what
// will not happen for the first prompt of an interactive seat, as an advisory
// and never a refusal. For now only claude takes runtime.prompt as a first
// message (T1); the check narrows as codex and opencode support lands.

func firstPromptRole(name, image string, mode RuntimeMode, prompt string, shift *ManifestShift) ManifestRole {
	return ManifestRole{
		Name:     name,
		Replicas: 1,
		Runtime:  ManifestRuntime{Image: image, Command: image, Mode: mode, Prompt: prompt},
		Shift:    shift,
	}
}

func TestFirstPromptAdvisories(t *testing.T) {
	t.Parallel()
	handoff := &ManifestShift{Handoff: "/var/handoff/{session}.md", HandoffMarker: "DONE"}
	tests := []struct {
		name  string
		roles []ManifestRole
		want  []string
	}{
		{
			name:  "claude with a prompt says nothing",
			roles: []ManifestRole{firstPromptRole("sup", "claude", RuntimeModeInteractive, "start here", nil)},
		},
		{
			name:  "claude with a shift handoff and no prompt says nothing",
			roles: []ManifestRole{firstPromptRole("sup", "claude", RuntimeModeInteractive, "", handoff)},
		},
		{
			name:  "a headless role is never reported",
			roles: []ManifestRole{firstPromptRole("job", "codex", RuntimeModeHeadless, "run it", nil), firstPromptRole("job2", "claude", RuntimeModeHeadless, "", nil)},
		},
		{
			name:  "codex with a prompt: the prompt is not delivered yet",
			roles: []ManifestRole{firstPromptRole("rev", "codex", RuntimeModeInteractive, "review it", nil)},
			want:  []string{`team crew role rev: interactive runtime "codex" cannot take runtime.prompt as a first message yet, so the prompt is not delivered; advisory only, and it narrows as first-prompt support lands for more harnesses (marvel#391)`},
		},
		{
			name:  "opencode, forestage and simulator are reported the same way",
			roles: []ManifestRole{firstPromptRole("a", "opencode", RuntimeModeInteractive, "p", nil), firstPromptRole("b", "forestage", RuntimeModeInteractive, "p", nil), firstPromptRole("c", "simulator", RuntimeModeInteractive, "p", nil)},
			want: []string{
				`team crew role a: interactive runtime "opencode" cannot take runtime.prompt as a first message yet, so the prompt is not delivered; advisory only, and it narrows as first-prompt support lands for more harnesses (marvel#391)`,
				`team crew role b: interactive runtime "forestage" cannot take runtime.prompt as a first message yet, so the prompt is not delivered; advisory only, and it narrows as first-prompt support lands for more harnesses (marvel#391)`,
				`team crew role c: interactive runtime "simulator" cannot take runtime.prompt as a first message yet, so the prompt is not delivered; advisory only, and it narrows as first-prompt support lands for more harnesses (marvel#391)`,
			},
		},
		{
			name:  "an interactive role with no prompt and no handoff starts idle",
			roles: []ManifestRole{firstPromptRole("sup", "claude", RuntimeModeInteractive, "", nil)},
			want:  []string{`team crew role sup: interactive role declares neither runtime.prompt nor shift.handoff, so a fresh seat of this role starts idle at an empty prompt (marvel#391)`},
		},
		{
			name:  "a shift block with no handoff does not count as a predecessor path",
			roles: []ManifestRole{firstPromptRole("sup", "claude", RuntimeModeInteractive, "", &ManifestShift{})},
			want:  []string{`team crew role sup: interactive role declares neither runtime.prompt nor shift.handoff, so a fresh seat of this role starts idle at an empty prompt (marvel#391)`},
		},
		{
			name:  "codex with neither gets only the idle line, not an undelivered-prompt line",
			roles: []ManifestRole{firstPromptRole("rev", "codex", RuntimeModeInteractive, "", nil)},
			want:  []string{`team crew role rev: interactive role declares neither runtime.prompt nor shift.handoff, so a fresh seat of this role starts idle at an empty prompt (marvel#391)`},
		},
		{
			name:  "a command path names the harness by its base name",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Command: "/usr/local/bin/claude", Prompt: "go"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{Teams: []ManifestTeam{{Name: "crew", Roles: tt.roles}}}
			got := m.FirstPromptAdvisories()
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("advisories =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
