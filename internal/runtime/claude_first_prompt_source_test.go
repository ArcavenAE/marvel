package runtime

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#391, docs/design/interactive-first-prompt.md T6 (D7): the launch says
// which first message it carried, as a source and a byte length, never the text.

func TestClaudeLaunchRecordsItsFirstPromptSource(t *testing.T) {
	t.Parallel()
	const role = "triage the queue"
	succ := func(withRole bool, command string) *LaunchContext {
		p := ""
		if withRole {
			p = role
		}
		return successorCtx(command, p)
	}
	first := func(command, prompt string) *LaunchContext {
		return claudePromptCtx(command, api.RuntimeModeInteractive, prompt, nil)
	}
	headless := claudePromptCtx("claude", api.RuntimeModeHeadless, "summarize", nil)

	cases := []struct {
		name string
		ctx  *LaunchContext
		want string
		// bytes is the length of what marvel passed; -1 means "the composed text".
		bytes int
	}{
		{"a first spawn with no prompt", first("claude", ""), FirstPromptNone, 0},
		{"a first spawn with a role prompt", first("claude", role), FirstPromptRole, len(role)},
		{"a successor with no role prompt", succ(false, "claude"), FirstPromptSuccessor, -1},
		{"a successor with a role prompt", succ(true, "claude"), FirstPromptSuccessorRole, -1},
		{"a wrapper with a role prompt", first("/usr/local/bin/seat-launch.sh", role), FirstPromptWrapperEnv, len(role)},
		{"a wrapper successor", succ(true, "/usr/local/bin/seat-launch.sh"), FirstPromptWrapperEnv, -1},
		{"a headless launch", headless, FirstPromptRole, len("summarize")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := (&Claude{}).Prepare(tc.ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if result.FirstPrompt.Source != tc.want {
				t.Errorf("source = %q, want %q", result.FirstPrompt.Source, tc.want)
			}
			want := tc.bytes
			if want < 0 {
				text, _, _ := composeFirstPrompt(tc.ctx)
				want = len(text)
			}
			if result.FirstPrompt.Bytes != want {
				t.Errorf("bytes = %d, want %d", result.FirstPrompt.Bytes, want)
			}
		})
	}
}

// An opted-out successor passed only the role's prompt, so that is its source.
func TestOptedOutSuccessorRecordsTheRoleSource(t *testing.T) {
	t.Parallel()
	off := false
	ctx := successorCtx("claude", "role words")
	ctx.Session.Runtime.SuccessorPrompt = &off
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if result.FirstPrompt.Source != FirstPromptRole || result.FirstPrompt.Bytes != len("role words") {
		t.Errorf("first prompt = %+v, want role, %d bytes", result.FirstPrompt, len("role words"))
	}
}

// The six source values are the design's, spelled as the design spells them.
func TestFirstPromptSourcesAreTheDesignsSix(t *testing.T) {
	t.Parallel()
	got := []string{FirstPromptRole, FirstPromptSuccessor, FirstPromptSuccessorRole, FirstPromptWrapperEnv, FirstPromptSkippedResume, FirstPromptNone}
	want := []string{"role", "successor", "successor+role", "wrapper-env", "skipped-resume", "none"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("source %d = %q, want %q", i, got[i], want[i])
		}
	}
}
