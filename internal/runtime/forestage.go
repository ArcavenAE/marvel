package runtime

// Forestage is the adapter for the forestage BYOA console. It provides
// deep integration: persona injection, heartbeat configuration, and
// cooperative stream attachment (when cluster.rs is built).
type Forestage struct{}

func (f *Forestage) Name() string { return "forestage" }

func (f *Forestage) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	// Inject marvel identity flags — forestage accepts these for cluster awareness.
	args = append(args,
		"--name", ctx.Session.Name,
		"--workspace", ctx.Workspace.Name,
		"--team", ctx.Team.Name,
		"--role", ctx.Role.Name,
	)
	if ctx.SocketPath != "" {
		args = append(args, "--socket", ctx.SocketPath)
	}

	// Inject script if specified in the role's runtime config.
	if ctx.Session.Runtime.Script != "" {
		args = append(args, "--script", ctx.Session.Runtime.Script)
	}

	// Inject permission mode if specified on the role.
	if ctx.Role.Permissions != "" {
		args = append(args, "--permission-mode", ctx.Role.Permissions)
	}

	env := baseEnv(ctx)

	return &LaunchResult{
		Command: buildCommand(binary, args),
		Env:     env,
	}, nil
}

func init() {
	// Ensure Forestage implements Adapter at compile time.
	var _ Adapter = (*Forestage)(nil)
}
