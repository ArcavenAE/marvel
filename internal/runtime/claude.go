package runtime

// Claude is the adapter for the bare Claude Code CLI. Medium integration:
// permission mode injection via CLI flag, environment-based identity,
// capture-pane fallback for observability.
type Claude struct{}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

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

	env := baseEnv(ctx)

	return &LaunchResult{
		Command: buildCommand(binary, args),
		Env:     env,
	}, nil
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
}
