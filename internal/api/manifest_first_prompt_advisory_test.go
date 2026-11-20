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
			name:  "an explicit mode of interactive with neither prompt nor handoff starts idle",
			roles: []ManifestRole{firstPromptRole("sup", "claude", RuntimeMode("interactive"), "", nil)},
			want:  []string{`team crew role sup: interactive role declares neither runtime.prompt nor shift.handoff, so a fresh seat of this role starts idle at an empty prompt (marvel#391)`},
		},
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
			name:  "codex with a prompt: refused at apply, so no advisory",
			roles: []ManifestRole{firstPromptRole("rev", "codex", RuntimeModeInteractive, "review it", nil)},
		},
		{
			name:  "opencode, forestage and simulator with a prompt are refused at apply, so no advisory",
			roles: []ManifestRole{firstPromptRole("a", "opencode", RuntimeModeInteractive, "p", nil), firstPromptRole("b", "forestage", RuntimeModeInteractive, "p", nil), firstPromptRole("c", "simulator", RuntimeModeInteractive, "p", nil)},
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
			// The adapter is picked by image, else by the whole command string, so a
			// path or a command with arguments gets the generic adapter and no prompt.
			name:  "a command path with no image is not claude to the adapter registry",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Command: "/usr/local/bin/claude", Mode: RuntimeModeInteractive, Prompt: "go"}}},
		},
		{
			name:  "a command with arguments and no image is not claude to the adapter registry",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Command: "claude --flag", Mode: RuntimeModeInteractive, Prompt: "go"}}},
		},
		{
			name:  "the bare command claude with no image is claude",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Command: "claude", Mode: RuntimeModeInteractive, Prompt: "go"}}},
		},
		{
			name:  "an image wins over the command",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Image: "claude", Command: "/opt/wrap/run", Mode: RuntimeModeInteractive, Prompt: "go"}}},
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

// An interactive role that declares runtime.prompt on a runtime that cannot
// deliver it is refused at apply, naming the role and the runtime (aae-orc-3nzxx,
// which supersedes D6 for this case). Only claude takes a first message for
// now; the runtime test narrows as codex (T2) and opencode (T3) support lands.
func TestFirstPromptRefusals(t *testing.T) {
	t.Parallel()
	refusal := func(role, runtime string) string {
		return `team crew role ` + role + `: interactive runtime "` + runtime + `" cannot take runtime.prompt as a first message, so apply refuses it instead of starting a seat that never gets its prompt (marvel#391)`
	}
	tests := []struct {
		name  string
		roles []ManifestRole
		want  []string
	}{
		{
			// mode = "interactive" is a value a manifest may write, and it
			// means the same as no mode at all.
			name:  "an explicit mode of interactive is refused like the default",
			roles: []ManifestRole{firstPromptRole("rev", "codex", RuntimeMode("interactive"), "review it", nil)},
			want:  []string{refusal("rev", "codex")},
		},
		{
			name:  "claude with a prompt is accepted",
			roles: []ManifestRole{firstPromptRole("sup", "claude", RuntimeModeInteractive, "start here", nil)},
		},
		{
			name:  "an interactive role with no prompt is not refused",
			roles: []ManifestRole{firstPromptRole("rev", "codex", RuntimeModeInteractive, "", nil)},
		},
		{
			name:  "a headless role carries its request in runtime.prompt and is never refused",
			roles: []ManifestRole{firstPromptRole("job", "codex", RuntimeModeHeadless, "run it", nil)},
		},
		{
			name:  "codex with a prompt is refused",
			roles: []ManifestRole{firstPromptRole("rev", "codex", RuntimeModeInteractive, "review it", nil)},
			want:  []string{refusal("rev", "codex")},
		},
		{
			name:  "opencode, forestage and simulator are refused the same way",
			roles: []ManifestRole{firstPromptRole("a", "opencode", RuntimeModeInteractive, "p", nil), firstPromptRole("b", "forestage", RuntimeModeInteractive, "p", nil), firstPromptRole("c", "simulator", RuntimeModeInteractive, "p", nil)},
			want:  []string{refusal("a", "opencode"), refusal("b", "forestage"), refusal("c", "simulator")},
		},
		{
			name:  "a command path with no image is not claude to the adapter registry",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Command: "/usr/local/bin/claude", Mode: RuntimeModeInteractive, Prompt: "go"}}},
			want:  []string{refusal("sup", "/usr/local/bin/claude")},
		},
		{
			name:  "a command with arguments and no image is not claude to the adapter registry",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Command: "claude --flag", Mode: RuntimeModeInteractive, Prompt: "go"}}},
			want:  []string{refusal("sup", "claude --flag")},
		},
		{
			name:  "the bare command claude with no image is claude",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Command: "claude", Mode: RuntimeModeInteractive, Prompt: "go"}}},
		},
		{
			name:  "an image wins over the command",
			roles: []ManifestRole{{Name: "sup", Replicas: 1, Runtime: ManifestRuntime{Image: "claude", Command: "/opt/wrap/run", Mode: RuntimeModeInteractive, Prompt: "go"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{Teams: []ManifestTeam{{Name: "crew", Roles: tt.roles}}}
			got := m.FirstPromptRefusals()
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("refusals =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
