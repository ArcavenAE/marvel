package runtime

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

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

// OffersHandoffTool reports false until the opencode half is built. opencode
// reaches an MCP server through the role's own environment
// (read-only-seat-handoff.md, section 2), but section 5 check 2 has not run: it
// must show the MCP call is not prompted for, so apply refuses handoff_via =
// "tool" here as it does for crush.
func (o *OpenCode) OffersHandoffTool() bool { return false }

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

// opencodeProvider returns the provider a role declares and the base URL
// configured for it. Runtime has no model or provider field until marvel#411,
// so today every role declares none and every seat shares the operator's home;
// the field's change gives this its body. It is a variable so a test can name a
// provider.
//
// Owner reading, proposed for review before merge: the name and the shape.
var opencodeProvider = func(ctx *LaunchContext) (provider, baseURL string) { return "", "" }

// openCodeNoPrivateHome is the reason of the refusal stub for a seat that is to
// have a private home and has none to put it in.
const openCodeNoPrivateHome = "no-private-home"

// privateHomeEligible reports whether a declared provider gets a private home:
// only lmstudio or ollama, and only when the base URL configured for it names
// the loopback host (127.0.0.1, ::1 or localhost). Every other role keeps the
// operator's home: the provider named opencode, any provider priced at zero, a
// local provider pointed off the host, and a role that declares no provider.
// Price is never the test, and nothing here reads a credential file.
//
// Owner reading, proposed for review before merge. The evidence is a probe of
// opencode's built-in providers: the free one, opencode, is remote, and
// lmstudio is the only built-in loopback provider (it needs a configuration
// entry or a key), while ollama is local only through a full entry.
func privateHomeEligible(provider, baseURL string) bool {
	if provider != "lmstudio" && provider != "ollama" {
		return false
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// OpenCodeCacheHome is the XDG_CACHE_HOME a private home gives a seat, which is
// where opencode keeps the model catalog marvel reads the window from.
func OpenCodeCacheHome(home string) string { return filepath.Join(home, "cache") }

// SessionHome gives a private home to a seat whose declared provider is local
// and on this host, and to no other (see privateHomeEligible). Nothing is linked
// in from the operator's home: a seat that needs a sign-in keeps the shared one.
func (o *OpenCode) SessionHome(ctx *LaunchContext) (SessionHomeSpec, bool) {
	if !privateHomeEligibleFor(ctx) {
		return SessionHomeSpec{}, false
	}
	return SessionHomeSpec{EnvVar: "XDG_DATA_HOME", Seed: seedOpenCodeHome}, true
}

func privateHomeEligibleFor(ctx *LaunchContext) bool {
	provider, baseURL := opencodeProvider(ctx)
	return privateHomeEligible(provider, baseURL)
}

// seedOpenCodeHome makes the data, cache and state directories of a private
// home. The config home is not one of them: it stays the operator's.
func seedOpenCodeHome(dir string) error {
	for _, d := range []string{"data", "cache", "state"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// openCodeRefusal is the launch of a refused opencode seat: a stub that prints
// the reason and exits 78 and never runs opencode. It returns no error, because
// a Prepare error is answered by the manager with the raw command, which would
// start opencode in the operator's home.
func openCodeRefusal(env map[string]string, reason string) *LaunchResult {
	script := fmt.Sprintf("printf 'marvel: opencode launch refused: %%s\\n' %s >&2; exit 78", shellQuote(reason))
	return &LaunchResult{Command: "/bin/sh -c " + shellQuote(script), Env: env}
}

func (o *OpenCode) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	env := baseEnv(ctx)
	if privateHomeEligibleFor(ctx) {
		switch {
		case filepath.IsAbs(ctx.HarnessHomePath):
			env["XDG_DATA_HOME"] = filepath.Join(ctx.HarnessHomePath, "data")
			env["XDG_CACHE_HOME"] = OpenCodeCacheHome(ctx.HarnessHomePath)
			env["XDG_STATE_HOME"] = filepath.Join(ctx.HarnessHomePath, "state")
		case ctx.Session.Runtime.Mode == api.RuntimeModeHeadless:
			return openCodeRefusal(env, openCodeNoPrivateHome), nil
		}
	}

	if ctx.Session.Runtime.Mode != api.RuntimeModeHeadless {
		return &LaunchResult{
			Command: buildCommand(binary, args),
			Env:     env,
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
		Env:     env,
		// The role prompt is on the command line, so the launch passed it.
		FirstPrompt: newFirstPrompt(FirstPromptRole, ctx.Session.Runtime.Prompt),
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
	var _ SessionHomeAssigner = (*OpenCode)(nil)
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
