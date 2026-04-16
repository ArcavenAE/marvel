package runtime

import "fmt"

// Forestage is the adapter for the forestage BYOA console. It provides
// deep integration: identity injection, permission mode, and system prompt
// context for team awareness.
//
// forestage accepts these top-level flags (per finding-019 taxonomy):
//
//	--persona           character slug from theme roster (e.g. "naomi-nagata")
//	--identity          professional lens (e.g. "homicide detective")
//	--role              job assignment(s) on this team (e.g. "reviewer,troubleshooter")
//	--name              agent session name (marvel identity)
//	--workspace         marvel workspace
//	--team              marvel team
//	--socket            marvel daemon socket path
//	--permission-mode   Claude Code permission mode
//	--script            lua script path (future: native lua)
//
// Claude-specific flags go after "--" as passthrough:
//
//	--append-system-prompt   team context for the agent
type Forestage struct{}

func (f *Forestage) Name() string { return "forestage" }

func (f *Forestage) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	// Inject persona (character slug) if specified in the role.
	if ctx.Role.Persona != "" {
		args = append(args, "--persona", ctx.Role.Persona)
	}

	// Inject identity (professional lens) if specified in the role.
	if ctx.Role.Identity != "" {
		args = append(args, "--identity", ctx.Role.Identity)
	}

	// Inject role as job assignment — this is the team function, not a character lookup.
	args = append(args, "--role", ctx.Role.Name)

	// Inject marvel identity flags — forestage accepts these natively.
	args = append(args,
		"--name", ctx.Session.Name,
		"--workspace", ctx.Workspace.Name,
		"--team", ctx.Team.Name,
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

	// Claude passthrough: team context as system prompt.
	teamContext := fmt.Sprintf(
		"You are %s (role: %s, team: %s, workspace: %s).",
		ctx.Session.Name, ctx.Role.Name, ctx.Team.Name, ctx.Workspace.Name,
	)
	args = append(args, "--", "--append-system-prompt", teamContext)

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
