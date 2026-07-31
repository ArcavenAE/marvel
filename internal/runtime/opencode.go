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
