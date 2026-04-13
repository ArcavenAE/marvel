package runtime

// Generic is the fallback adapter for any CLI that accepts a prompt on
// stdin. Minimal integration: environment-based identity only. Marvel
// observes the session via capture-pane scraping.
type Generic struct{}

func (g *Generic) Name() string { return "generic" }

func (g *Generic) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	env := baseEnv(ctx)

	return &LaunchResult{
		Command: buildCommand(binary, args),
		Env:     env,
	}, nil
}

func init() {
	var _ Adapter = (*Generic)(nil)
}
