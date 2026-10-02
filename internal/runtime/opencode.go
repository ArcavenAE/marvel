package runtime

import (
	"os"

	"github.com/arcavenae/marvel/internal/api"
)

// OpenCode is the adapter for the OpenCode CLI. In headless mode
// (api.RuntimeModeHeadless) it launches `opencode run --format json`,
// whose line-delimited JSON event stream marvel redirects to a FIFO and
// parses; the parser lives in internal/runtime/opencode.
//
// In interactive mode it launches opencode as given (its own TUI owns the
// pane) and marvel observes via capture-pane.
//
// The adapter injects no --auto flag: without it OpenCode auto-rejects
// tool-permission requests rather than blocking, which is the safe default
// for an unattended launch. An operator wanting autonomous tool execution
// adds --auto through runtime.Args. The richer `serve` + `attach` surface
// (real session lifecycle and permission events over SSE) is a later
// adapter, not this one.
type OpenCode struct{}

func (o *OpenCode) Name() string { return "opencode" }

// SupportsStream reports whether this launch produces a parseable stream.
// Only headless launches do.
func (o *OpenCode) SupportsStream(ctx *LaunchContext) bool {
	return ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
}

// ProjectionFor reports no projection surface. OpenCode reads its own
// opencode.json config, not a Claude Code settings fragment, so a policy
// is advisory for this runtime — marvel logs it rather than writing a
// file opencode would not read.
func (o *OpenCode) ProjectionFor(_ *LaunchContext, _ string) ProjectionTarget {
	return ProjectionTarget{Supported: false}
}

func (o *OpenCode) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	if ctx.Session.Runtime.Mode != api.RuntimeModeHeadless {
		return &LaunchResult{
			Command: buildCommand(binary, args),
			Env:     baseEnv(ctx),
		}, nil
	}

	if ctx.Session.Runtime.Prompt == "" {
		return nil, ErrNoPrompt
	}

	// `opencode run --format json [options] <message>`: message positional.
	full := []string{"run", "--format", "json"}
	full = append(full, args...)
	full = append(full, ctx.Session.Runtime.Prompt)

	cmd := buildCommand(binary, full)
	cmd = redirectStdin(cmd, os.DevNull)

	result := &LaunchResult{
		Command: cmd,
		Env:     baseEnv(ctx),
	}
	if ctx.StreamPath != "" {
		result.Command = redirectStdout(result.Command, ctx.StreamPath)
		result.Stream = &StreamSpec{
			Format: StreamFormatOpenCodeJSON,
			Path:   ctx.StreamPath,
		}
	}
	return result, nil
}

func init() {
	var _ Adapter = (*OpenCode)(nil)
	var _ StreamCapable = (*OpenCode)(nil)
}

// Composer is opencode's composer contract, measured on 1.18.15. C-c on an empty
// composer, or in a running turn, exits opencode at once, so no clear is sent
// until a reader can see a staged draft in an idle composer.
func (*OpenCode) Composer() ComposerContract {
	return ComposerContract{
		Submit:              SubmitPasteEnter,
		ClearKey:            "C-c",
		ClearExitsWhenEmpty: true,
		ClearExitsMidTurn:   true,
		ClearSavesDraft:     false,
		InterruptKeys:       []string{"Escape", "Escape"},
		Source: map[string]string{
			"submit":    "aae-orc-g88i1 probe (2026-10-02, opencode 1.18.15): bracketed paste, then Enter; literal newlines stay in the draft and do not submit",
			"clear":     "aae-orc-g88i1 probe (2026-10-02, opencode 1.18.15): one C-c clears a staged draft of either form and submits nothing; one C-c on an empty composer or in a running turn exits opencode with no confirmation; C-u clears a pasted draft but not a typed multi-line one",
			"interrupt": "measured, aae-orc-g88i1 probe (2026-10-02, opencode 1.18.15): Escape twice interrupts a running turn; one Escape only arms it",
		},
	}
}
