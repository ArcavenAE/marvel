package runtime

import "fmt"

// Forestage is the adapter for the forestage BYOA console. It provides
// deep integration: identity injection, permission mode, and system prompt
// context for team awareness.
//
// forestage accepts these top-level flags:
//
//	--role              role override (persona role within theme)
//	--name              agent session name (marvel identity)
//	--workspace         marvel workspace
//	--team              marvel team
//	--socket            marvel daemon socket path
//	--permission-mode   Claude Code permission mode
//	--script            lua script path (future: native lua)
//
// Claude-specific flags go after "--" as passthrough:
//
//	--append-system-prompt   identity context for the agent
type Forestage struct{}

func (f *Forestage) Name() string { return "forestage" }

func (f *Forestage) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	// Inject marvel identity flags — forestage accepts these natively.
	args = append(args,
		"--name", ctx.Session.Name,
		"--workspace", ctx.Workspace.Name,
		"--team", ctx.Team.Name,
		"--role", ctx.Role.Name,
	)
	if ctx.SocketPath != "" {
		args = append(args, "--socket", ctx.SocketPath)
	}

	// Inject permission mode — forestage passes this through to claude.
	if ctx.Role.Permissions != "" {
		args = append(args, "--permission-mode", ctx.Role.Permissions)
	}

	// Inject script if specified in the role's runtime config.
	if ctx.Session.Runtime.Script != "" {
		args = append(args, "--script", ctx.Session.Runtime.Script)
	}

	// Claude passthrough: identity context as system prompt.
	identity := fmt.Sprintf(
		"You are %s (role: %s, team: %s, workspace: %s).",
		ctx.Session.Name, ctx.Role.Name, ctx.Team.Name, ctx.Workspace.Name,
	)
	args = append(args, "--", "--append-system-prompt", identity)

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
