package runtime

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#391, docs/design/interactive-first-prompt.md T6 (D7): a headless
// codex or opencode spawn puts the role prompt on its command line, so its
// launch records that prompt as the role source. Interactive launches pass
// nothing yet and keep recording none.

func TestHeadlessCodexAndOpenCodeRecordTheRolePrompt(t *testing.T) {
	t.Parallel()
	const prompt = "say ok"
	for name, adapter := range map[string]Adapter{"codex": &Codex{}, "opencode": &OpenCode{}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result, err := adapter.Prepare(headlessContext(name, name, ""))
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if result.FirstPrompt.Source != FirstPromptRole || result.FirstPrompt.Bytes != len(prompt) {
				t.Errorf("first prompt = %+v, want %s, %d bytes", result.FirstPrompt, FirstPromptRole, len(prompt))
			}
		})
	}
}

func TestInteractiveCodexAndOpenCodeStillRecordNone(t *testing.T) {
	t.Parallel()
	for name, adapter := range map[string]Adapter{"codex": &Codex{}, "opencode": &OpenCode{}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := testContext()
			ctx.Session.Runtime = api.Runtime{Name: name, Command: name, Prompt: "ignored"}
			result, err := adapter.Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if result.FirstPrompt.Source != "" && result.FirstPrompt.Source != FirstPromptNone {
				t.Errorf("first prompt = %+v, want none", result.FirstPrompt)
			}
			if result.FirstPrompt.Bytes != 0 {
				t.Errorf("bytes = %d, want 0", result.FirstPrompt.Bytes)
			}
		})
	}
}
