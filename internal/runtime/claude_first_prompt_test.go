package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#391, docs/design/interactive-first-prompt.md T1 (D1, D4): an
// interactive claude seat gets the role's runtime.prompt as its first message,
// the trailing positional argument. A wrapper owns its own command line, so it
// gets no positional and gets the same text in MARVEL_FIRST_PROMPT.

const firstPromptEnv = "MARVEL_FIRST_PROMPT"

func claudePromptCtx(command string, mode api.RuntimeMode, prompt string, args []string) *LaunchContext {
	ctx := claudeCtx(command)
	ctx.Session.Runtime.Mode = mode
	ctx.Session.Runtime.Prompt = prompt
	ctx.Session.Runtime.Args = args
	return ctx
}

func TestClaudeInteractivePromptIsTheTrailingPositional(t *testing.T) {
	t.Parallel()
	const prompt = "Read your handoff, then drain your inbox."
	result, err := (&Claude{}).Prepare(claudePromptCtx("claude", api.RuntimeModeInteractive, prompt, nil))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasSuffix(result.Command, " "+shellQuote(prompt)) {
		t.Errorf("command does not end with the quoted prompt: %s", result.Command)
	}
	if got := strings.Count(result.Command, prompt); got != 1 {
		t.Errorf("prompt appears %d times, want once: %s", got, result.Command)
	}
	if _, set := result.Env[firstPromptEnv]; set {
		t.Errorf("a bare harness got %s; only a wrapper does", firstPromptEnv)
	}
}

func TestClaudeInteractiveWithoutAPromptGetsNoPositional(t *testing.T) {
	t.Parallel()
	result, err := (&Claude{}).Prepare(claudePromptCtx("claude", api.RuntimeModeInteractive, "", nil))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// Without a prompt the last argument is whatever the launch ended on
	// before this change, a flag value, never an empty quoted word.
	if strings.HasSuffix(result.Command, " ''") {
		t.Errorf("an empty positional was appended: %s", result.Command)
	}
	if _, set := result.Env[firstPromptEnv]; set {
		t.Errorf("%s set with no prompt", firstPromptEnv)
	}
}

func TestClaudeHeadlessPromptIsUnchanged(t *testing.T) {
	t.Parallel()
	const prompt = "summarize the repo"
	result, err := (&Claude{}).Prepare(claudePromptCtx("claude", api.RuntimeModeHeadless, prompt, nil))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasSuffix(result.Command, " "+shellQuote(prompt)) || !strings.Contains(result.Command, "--print") {
		t.Errorf("headless launch changed: %s", result.Command)
	}
	if _, set := result.Env[firstPromptEnv]; set {
		t.Errorf("a headless launch got %s", firstPromptEnv)
	}
}

// A wrapper (a launch script, a container run) owns its command line, so
// marvel adds no positional and hands the text over in the environment.
func TestClaudeWrapperGetsTheFirstPromptInTheEnvironment(t *testing.T) {
	t.Parallel()
	const prompt = "start with the handoff"
	cases := []struct {
		name    string
		command string
	}{
		{"a launch script", "/usr/local/bin/seat-launch.sh"},
		{"a container run", "docker run --rm img claude"},
		{"npx", "npx claude"},
		{"claude with a pipe, whose words cannot be read", "claude | tee /tmp/x"},
		{"claude with an expansion", "claude $EXTRA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := (&Claude{}).Prepare(claudePromptCtx(tc.command, api.RuntimeModeInteractive, prompt, nil))
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if strings.Contains(result.Command, prompt) {
				t.Errorf("wrapper command carries the prompt: %s", result.Command)
			}
			if got := result.Env[firstPromptEnv]; got != prompt {
				t.Errorf("%s = %q, want %q", firstPromptEnv, got, prompt)
			}
		})
	}
}

// The positional is the last argument whatever the earlier ones are, so a role
// that resumes keeps its resume flag and gets the message as the next turn
// (design D5, claude row).
func TestClaudeFirstPromptComposesWithResume(t *testing.T) {
	t.Parallel()
	const prompt = "carry on"
	cases := [][]string{
		{"--resume", "abc123"},
		{"--continue"},
		{"-c"},
		{"-r", "abc123"},
	}
	for _, args := range cases {
		result, err := (&Claude{}).Prepare(claudePromptCtx("claude", api.RuntimeModeInteractive, prompt, args))
		if err != nil {
			t.Fatalf("Prepare(%v): %v", args, err)
		}
		for _, a := range args {
			if !strings.Contains(result.Command, shellQuote(a)) {
				t.Errorf("args %v: resume word %q missing from %s", args, a, result.Command)
			}
		}
		if !strings.HasSuffix(result.Command, " "+shellQuote(prompt)) {
			t.Errorf("args %v: prompt is not last: %s", args, result.Command)
		}
	}
}

// The prompt is one shell word: a quote or a leading dash in it neither ends
// the word nor reads as a flag to claude.
func TestClaudeFirstPromptIsOneQuotedWord(t *testing.T) {
	t.Parallel()
	for _, prompt := range []string{"it's the handoff", "--print the handoff", "a; rm -rf /", "line one\nline two"} {
		result, err := (&Claude{}).Prepare(claudePromptCtx("claude", api.RuntimeModeInteractive, prompt, nil))
		if err != nil {
			t.Fatalf("Prepare(%q): %v", prompt, err)
		}
		if !strings.HasSuffix(result.Command, " "+shellQuote(prompt)) {
			t.Errorf("prompt %q is not the quoted last word: %s", prompt, result.Command)
		}
	}
}
