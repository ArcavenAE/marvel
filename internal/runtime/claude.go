package runtime

import "github.com/arcavenae/marvel/internal/api"

// Claude is the adapter for the bare Claude Code CLI. Medium integration:
// permission mode injection via CLI flag, environment-based identity,
// capture-pane fallback for observability.
//
// In headless mode (api.RuntimeModeHeadless) it is also the first
// stream-capable adapter: the harness runs `--print --output-format
// stream-json --verbose` and marvel parses the resulting NDJSON.
type Claude struct{}

func (c *Claude) Name() string { return "claude" }

// SupportsStream reports whether this launch will produce a parseable
// stream. Only headless launches do: interactive Claude Code renders a
// TUI to the pane and has no structured output to redirect.
func (c *Claude) SupportsStream(ctx *LaunchContext) bool {
	return ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
}

// ProjectionFor reports where marvel writes claude's projected settings
// fragment. Claude Code reads it via a top-level --settings flag
// (injected in Prepare), so this runtime supports projection.
func (c *Claude) ProjectionFor(ctx *LaunchContext, dir string) ProjectionTarget {
	return ProjectionTarget{
		Supported: true,
		Path:      settingsProjectionPath(dir, ctx.Session.Key()),
	}
}

func (c *Claude) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}
	headless := ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
	if headless && ctx.Session.Runtime.Prompt == "" {
		return nil, ErrNoPrompt
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	// Point claude at the projected policy settings file when marvel wrote
	// one for this launch. Claude Code re-reads it mid-session, so a later
	// re-projection changes the running agent's contract without a restart.
	if ctx.PolicyProjectionPath != "" {
		args = append(args, "--settings", ctx.PolicyProjectionPath)
	}

	if headless {
		// --verbose is not optional here: claude refuses stream-json
		// output under --print without it.
		args = append(args, "--print", "--output-format", "stream-json", "--verbose")
	}

	// Inject permission mode — claude CLI accepts this directly.
	if ctx.Role.Permissions != "" {
		args = append(args, "--permission-mode", ctx.Role.Permissions)
	}

	// Inject system prompt with role context if no --append-system-prompt
	// is already present.
	if !hasFlag(args, "--append-system-prompt") {
		prompt := "You are " + ctx.Session.Name + " (role: " + ctx.Role.Name +
			", team: " + ctx.Team.Name + ", workspace: " + ctx.Workspace.Name + ")."
		args = append(args, "--append-system-prompt", prompt)
	}

	// The request goes last, as the positional argument.
	if headless {
		args = append(args, ctx.Session.Runtime.Prompt)
	}

	result := &LaunchResult{
		Command: buildCommand(binary, args),
		Env:     baseEnv(ctx),
	}
	if headless && ctx.StreamPath != "" {
		result.Command = redirectStdout(result.Command, ctx.StreamPath)
		result.Stream = &StreamSpec{
			Format: StreamFormatClaudeCodeJSON,
			Path:   ctx.StreamPath,
		}
	}
	return result, nil
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func init() {
	var _ Adapter = (*Claude)(nil)
	var _ StreamCapable = (*Claude)(nil)
}
