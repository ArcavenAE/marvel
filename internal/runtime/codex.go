package runtime

import (
	"os"

	"github.com/arcavenae/marvel/internal/api"
)

// Codex is the adapter for the Codex CLI. In headless mode
// (api.RuntimeModeHeadless) it launches `codex exec --json`, whose JSONL
// event stream marvel redirects to a FIFO and parses; the parser lives in
// internal/runtime/codex.
//
// In interactive mode it launches codex as given (its own TUI owns the
// pane) and marvel observes via capture-pane, exactly as the bare-claude
// interactive path does.
//
// Codex has no --append-system-prompt or --permission-mode equivalent, so
// marvel identity travels only through the environment (baseEnv). Sandbox
// and approval policy are the operator's to set through runtime.Args; the
// adapter injects neither, to avoid widening a harness's authority by
// default.
type Codex struct{}

func (c *Codex) Name() string { return "codex" }

// SupportsStream reports whether this launch produces a parseable stream.
// Only headless launches do: interactive codex renders a TUI and has no
// structured output to redirect.
func (c *Codex) SupportsStream(ctx *LaunchContext) bool {
	return ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
}

// ProjectionFor reports no projection surface. Codex configures itself
// through its own config file and -c overrides, not a Claude Code
// settings fragment, so a policy is advisory for this runtime — marvel
// logs it rather than writing a file codex would not read.
func (c *Codex) ProjectionFor(_ *LaunchContext, _ string) ProjectionTarget {
	return ProjectionTarget{Supported: false}
}

func (c *Codex) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	if ctx.Session.Runtime.Mode != api.RuntimeModeHeadless {
		// Interactive: launch codex as-is; the TUI owns the pane.
		return &LaunchResult{
			Command: buildCommand(binary, args),
			Env:     baseEnv(ctx),
		}, nil
	}

	if ctx.Session.Runtime.Prompt == "" {
		return nil, ErrNoPrompt
	}

	// `codex exec [OPTIONS] [PROMPT]`: options first, prompt last.
	// --skip-git-repo-check keeps codex from refusing a non-repo workspace.
	full := []string{"exec", "--json", "--skip-git-repo-check"}
	full = append(full, args...)
	full = append(full, ctx.Session.Runtime.Prompt)

	cmd := buildCommand(binary, full)
	// stdin must be closed even without a sink: codex exec appends piped
	// stdin to its prompt and would otherwise hang on the pane tty.
	cmd = redirectStdin(cmd, os.DevNull)

	result := &LaunchResult{
		Command: cmd,
		Env:     baseEnv(ctx),
	}
	if ctx.StreamPath != "" {
		result.Command = redirectStdout(result.Command, ctx.StreamPath)
		result.Stream = &StreamSpec{
			Format: StreamFormatCodexJSON,
			Path:   ctx.StreamPath,
		}
	}
	return result, nil
}

func init() {
	var _ Adapter = (*Codex)(nil)
	var _ StreamCapable = (*Codex)(nil)
}
