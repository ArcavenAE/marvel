package runtime

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The record says how the appended flag reached the command, so describe can
// tell a flag in the argument list from one appended to shell text whose
// destination marvel cannot see (marvel#748). The same predicate as the prompt
// and sources gates decides, so the two cannot disagree.
func TestClaudeSettingSourcesDeliveryFollowsTheCommandForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		command      string
		declared     []string
		wantSources  string
		wantDelivery string
	}{
		{"plain claude, default sources", "claude", nil, "user,project,local", api.SettingSourcesArgv},
		{"readable command with a declaration", "claude --model sonnet", []string{"project", "local"}, "project,local", api.SettingSourcesArgv},
		{"a pipe", "claude --x | tee out", []string{"project"}, "project", api.SettingSourcesShellText},
		{"a semicolon", "claude --x ; true", []string{"project"}, "project", api.SettingSourcesShellText},
		{"a comment", "claude --x # note", []string{"project"}, "project", api.SettingSourcesShellText},
		{"a quote", `claude "--x"`, []string{"user", "project"}, "user,project", api.SettingSourcesShellText},
		{"shell text, nothing declared", "claude --x | tee out", nil, "", ""},
		{"a flag already in the readable command", "claude --setting-sources user", nil, "", ""},
		{"a wrapper", "wrapper --run claude", []string{"project"}, "", ""},
	}
	for _, tc := range cases {
		ctx := claudeCtx(tc.command)
		ctx.Role.SettingsSources = tc.declared
		result, err := (&Claude{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", tc.name, err)
		}
		if result.SettingSources != tc.wantSources || result.SettingSourcesDelivery != tc.wantDelivery {
			t.Errorf("%s: sources %q delivery %q, want %q and %q", tc.name,
				result.SettingSources, result.SettingSourcesDelivery, tc.wantSources, tc.wantDelivery)
		}
	}
}
