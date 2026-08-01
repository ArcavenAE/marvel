package runtime

// Simulator is the adapter for marvel's built-in load-test simulator
// (cmd/simulator, image "simulator"). The simulator heartbeats the daemon,
// but only when it is given --socket, and its heartbeat session key is
// "<workspace>/<name>", so it also needs --name and --workspace to match
// the session marvel tracks. The generic adapter injects none of these as
// flags: it sets the MARVEL_* env vars, which the simulator does not read.
// A simulator launched through the generic fallback therefore never
// heartbeats, goes stale on its heartbeat healthcheck, and under the
// default restart_policy=always restart-loops instead of showing healthy.
// This adapter injects the identity flags the simulator's heartbeat path
// requires. It does not touch the generic path for any other image.
type Simulator struct{}

func (s *Simulator) Name() string { return "simulator" }

// ProjectionFor reports no projection surface. The simulator has no Claude
// Code settings mechanism, so a referenced policy is advisory — marvel logs
// it rather than writing a file the simulator would not read.
func (s *Simulator) ProjectionFor(_ *LaunchContext, _ string) ProjectionTarget {
	return ProjectionTarget{Supported: false}
}

func (s *Simulator) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	// Inject the identity flags the simulator's heartbeat path needs.
	// --name and --workspace form the "<workspace>/<name>" session key the
	// daemon matches; --team and --role feed the simulator's lua env.
	args = append(
		args,
		"--name", ctx.Session.Name,
		"--workspace", ctx.Workspace.Name,
		"--team", ctx.Team.Name,
		"--role", ctx.Role.Name,
	)
	// --socket is what enables the heartbeat RPC at all; without it the
	// simulator never wires OnHeartbeat and the session stays stale.
	if ctx.SocketPath != "" {
		args = append(args, "--socket", ctx.SocketPath)
	}
	// The simulator accepts a lua --script; the generic path dropped it.
	if ctx.Session.Runtime.Script != "" {
		args = append(args, "--script", ctx.Session.Runtime.Script)
	}

	return &LaunchResult{
		Command: buildCommand(binary, args),
		Env:     baseEnv(ctx),
	}, nil
}

func init() {
	// Ensure Simulator implements Adapter at compile time.
	var _ Adapter = (*Simulator)(nil)
}
