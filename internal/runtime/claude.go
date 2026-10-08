package runtime

import (
	"log"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// logLaunch is where the adapter reports a launch decision. A seam so a test
// reads the line without redirecting the process-wide logger.
var logLaunch = log.Printf

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

// StatuslineFeed renders marvel's context feed in Claude Code's settings
// schema. It lives here rather than in the session manager so the keys sit
// beside the --settings flag in Prepare that makes them reachable, and so
// a harness with a different schema cannot be handed these ones.
//
// refreshInterval keeps the feed beating while the session idles:
// statusline updates are event-driven and go quiet between prompts, which
// would otherwise starve a heartbeat healthcheck watching this session.
// The subagent hook carries no interval because a subagent turn is bounded.
func (c *Claude) StatuslineFeed(command string) map[string]any {
	return claudeStatuslineFeed(command)
}

// claudeStatuslineFeed is shared with the forestage adapter, which reaches
// the same Claude Code settings surface through its passthrough.
func claudeStatuslineFeed(command string) map[string]any {
	return map[string]any{
		"statusLine": map[string]any{
			"type":            "command",
			"command":         command,
			"refreshInterval": 15,
		},
		"subagentStatusLine": map[string]any{
			"type":    "command",
			"command": command,
		},
	}
}

// AssignsSessionID reports that Claude Code takes a caller-chosen session
// id, so marvel names this session instead of discovering it later.
//
// Verified against 2.1.271 on 2026-09-15 (the ticket's snapshot was
// 2.1.226 and says explicitly that it decays): --session-id <uuid> is
// accepted, and the transcript is then written to
// ~/.claude/projects/<cwd slug>/<uuid>.jsonl. That is the binding
// aae-orc-ca7y is about, and it is the one an lsof probe cannot recover,
// because the live harness holds no descriptor on the JSONL.
//
// It declines a launch whose own args already steer session identity.
// A manifest that passes --session-id has named the session itself and
// wins, the same precedence --append-system-prompt already has below; and
// --resume, -r, --continue or -c ask the harness to adopt an EXISTING
// session, which a freshly minted id contradicts. In both cases marvel
// mints nothing, so Session.HarnessSessionID stays empty rather than
// recording an id the harness was never given.
func (c *Claude) AssignsSessionID(ctx *LaunchContext) bool {
	return !hasAnyFlag(ctx.Session.Runtime.Args,
		"--session-id", "--resume", "-r", "--continue", "-c")
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
	// one for this launch. A later re-projection rewrites the file live, but
	// its permissions take effect on the next spawn; a running session keeps
	// the permissions it started with (marvel#313).
	if ctx.PolicyProjectionPath != "" {
		args = append(args, "--settings", ctx.PolicyProjectionPath)
	}

	if headless {
		// --verbose is not optional here: claude refuses stream-json
		// output under --print without it.
		args = append(args, "--print", "--output-format", "stream-json", "--verbose")
	}

	// Name the session marvel is starting. AssignsSessionID has already
	// refused the launches where this would fight the caller's own args,
	// so an id present here is one marvel minted and recorded.
	if ctx.HarnessSessionID != "" {
		args = append(args, "--session-id", ctx.HarnessSessionID)
	}

	// Inject permission mode — claude CLI accepts this directly.
	if ctx.Role.Permissions != "" {
		args = append(args, "--permission-mode", ctx.Role.Permissions)
	}

	// Inject system prompt with role context, unless the manifest already
	// supplies one or the command is a wrapper around the harness. Claude
	// Code keeps only the last --append-system-prompt it is given and
	// refuses the flag alongside --append-system-prompt-file, so a second
	// prompt never adds to the first: it replaces it. A wrapper (a launch
	// script, a container run) that sets its own prompt would lose it to
	// this one-liner, because marvel's args land after its own
	// (aae-orc-1vq6z). A wrapper owns the prompt; the identity it would
	// carry is in the constructed env (MARVEL_SESSION and siblings).
	//
	// The role may keep its own prompt in runtime.args or inline in its command
	// ("claude --append-system-prompt ..."), so both are read, by the same
	// helper that reads the setting-sources flag (marvel#745). A command whose
	// words cannot be read (a quote, an expansion, a pipe, a second line) may carry
	// the flag where nothing here can see it, so none is added and one line says
	// so.
	if isBareClaude(binary) {
		switch {
		case !api.CommandWordsReadable(binary):
			logLaunch("role %s: command is shell text; marvel's system-prompt line not added", ctx.Role.Name)
		case !carriesFlag(args, binary, "--append-system-prompt", "--append-system-prompt-file"):
			prompt := "You are " + ctx.Session.Name + " (role: " + ctx.Role.Name +
				", team: " + ctx.Team.Name + ", workspace: " + ctx.Workspace.Name + ")."
			args = append(args, "--append-system-prompt", prompt)
		}
	}

	// Tell the harness which settings sources to load, so what applies is a
	// declaration and not a consequence of the directory it starts in
	// (docs/design/seat-bootstrap.md section 4). Only for the bare harness: a
	// wrapper owns its command line and passes its own, and a role that already
	// names the sources, in args or inside its command string, keeps them. A
	// role that declares nothing keeps today's behavior, every source, so an
	// upgrade does not change what a seat loads from its directory; the placed
	// defaults arrive with SB-1.
	settingSources := ""
	if isBareClaude(binary) && !carriesFlag(args, binary, "--setting-sources") {
		settingSources = "user,project,local"
		if len(ctx.Role.SettingsSources) > 0 {
			settingSources = strings.Join(ctx.Role.SettingsSources, ",")
		}
		args = append(args, "--setting-sources", settingSources)
	}

	// The request goes last, as the positional argument.
	if headless {
		args = append(args, ctx.Session.Runtime.Prompt)
	}

	result := &LaunchResult{
		Command:        buildCommand(binary, args),
		Env:            baseEnv(ctx),
		SettingSources: settingSources,
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

// isBareClaude reports whether a resolved runtime command launches the
// Claude Code binary directly, by bare name or by path. Anything else
// (a launcher script, `docker run ... claude`, `npx ...`) is a wrapper:
// marvel cannot see what the wrapper passes to the harness, so it must
// not add a flag the wrapper's own would collide with. The command is
// shell text, so the first field is the program tmux will run.
func isBareClaude(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return filepath.Base(fields[0]) == "claude"
}

// commandArgs returns the words of a command string after the program, which is
// where a role that writes `command = "claude --flag value"` keeps its flags.
func commandArgs(command string) []string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil
	}
	return fields[1:]
}

// carriesFlag reports whether a role already supplies one of flags, in
// runtime.args or inline in the words of its command.
func carriesFlag(args []string, command string, flags ...string) bool {
	return hasAnyFlag(args, flags...) || hasAnyFlag(commandArgs(command), flags...)
}

// hasAnyFlag reports whether args carry any of the named flags, in either
// the separate-value form (--flag value) or the joined form (--flag=value).
// The joined form matters here: a manifest writing --session-id=<uuid> has
// named the session just as surely as --session-id <uuid>, and missing it
// would put two of the flag on one command line.
func hasAnyFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

func init() {
	var _ Adapter = (*Claude)(nil)
	var _ StreamCapable = (*Claude)(nil)
	var _ SessionIDAssigner = (*Claude)(nil)
}

// Composer is Claude Code's composer contract, measured on 2.1.288 (haiku).
// The clear is gated on a reader seeing text in an idle composer
// (internal/composer): C-c on an empty composer arms "press again to exit", and
// in a running turn it interrupts the turn.
func (*Claude) Composer() ComposerContract {
	return ComposerContract{
		Submit:   SubmitPasteEnter,
		ClearKey: "C-c",
		// Two presses on an empty composer 1 s apart did not exit; the reader gate
		// still keeps the key off an empty composer.
		ClearExitsWhenEmpty: false,
		ClearExitsMidTurn:   false,
		ClearSavesDraft:     false,
		InterruptKeys:       []string{"Escape"},
		Source: map[string]string{
			"submit":    "marvel#355 (2026-09-25): bracketed paste, then Enter; restated by the aae-orc-6vcr2 probe on Claude Code 2.1.288",
			"clear":     "aae-orc-6vcr2 (2026-10-02, Claude Code 2.1.288, haiku): one C-c cleared a staged 3-line draft and submitted nothing; C-u cleared about a line per two presses; Escape left the draft; C-c on an empty composer arms the exit prompt and in a turn interrupts it",
			"interrupt": "not measured: Escape is Claude Code's interrupt key; the aae-orc-6vcr2 probe measured only that Escape leaves a staged draft alone",
		},
	}
}

var claudeVersionCommand = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Foreground implements ForegroundRule. claude's binary runs under its version
// number, so tmux reports "2.1.285" as the pane's command; "claude" covers an
// install that does not. A rule keyed on one version would drop every seat on
// another, exactly when an update is likeliest to log seats out.
func (c *Claude) Foreground(paneCommand string) (string, bool) {
	if claudeVersionCommand.MatchString(paneCommand) {
		return paneCommand, true
	}
	return "", paneCommand == "claude"
}
