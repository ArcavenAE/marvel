package runtime

import (
	"os"
	"path/filepath"

	"github.com/arcavenae/marvel/internal/api"
)

// Codex is the adapter for the Codex CLI. In headless mode
// (api.RuntimeModeHeadless) it launches `codex exec --json`, whose JSONL
// event stream marvel redirects to a FIFO and parses; the parser lives in
// internal/runtime/codex.
//
// In interactive mode it launches codex as given (its own TUI owns the
// pane) and marvel observes via capture-pane, exactly as the bare-claude
// interactive path does.
//
// Codex has no --append-system-prompt or --permission-mode equivalent, so
// marvel identity travels only through the environment (baseEnv). Sandbox
// and approval policy are the operator's to set through runtime.Args; the
// adapter injects neither, to avoid widening a harness's authority by
// default.
type Codex struct{}

func (c *Codex) Name() string { return "codex" }

// SupportsStream reports whether this launch produces a parseable stream.
// Only headless launches do: interactive codex renders a TUI and has no
// structured output to redirect.
func (c *Codex) SupportsStream(ctx *LaunchContext) bool {
	return ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
}

// ProjectionFor reports no projection surface. Codex configures itself
// through its own config file and -c overrides, not a Claude Code
// settings fragment, so a policy is advisory for this runtime — marvel
// logs it rather than writing a file codex would not read.
func (c *Codex) ProjectionFor(_ *LaunchContext, _ string) ProjectionTarget {
	return ProjectionTarget{Supported: false}
}

// codexHomeEnv is the lever that relocates codex's entire state tree.
const codexHomeEnv = "CODEX_HOME"

// codexAuthFile is the credential codex keeps under its home. A private
// home without it is a session that cannot authenticate, which is the
// failure mode a naive per-pane home walks into: measured on 0.153.4, a
// fresh CODEX_HOME produces `401 Unauthorized` on the first model call
// even for an operator who is logged in.
const codexAuthFile = "auth.json"

// codexControlSocket is where codex 0.157's interactive TUI opens its
// app-server control socket under CODEX_HOME. Headless `exec` opens none.
// A path past the sun_path limit makes the TUI exit 1 at start with "path
// must be shorter than SUN_LEN" (aae-orc-pt8k).
const codexControlSocket = "app-server-control/app-server-control.sock"

// SessionHome gives this launch a private CODEX_HOME.
//
// codex offers no session-id pin, so marvel assigns the container instead
// (aae-orc-ca7y). Measured on codex 0.153.4, a private home holds the
// whole state tree and nothing of anyone else's: sessions/, state_5.sqlite,
// logs_2.sqlite, thread_history_1.sqlite, memories_1.sqlite, queue_1.sqlite,
// goals_1.sqlite, skills/, installation_id, shell_snapshots/ and tmp/ all
// land under it, and ~/.codex is untouched. One session run under a fresh
// home left exactly one rollout in it,
// sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<thread id>.jsonl, so the
// pane-to-rollout binding is a directory marvel chose.
//
// The ticket's file list is from 0.146.0 and has partly decayed:
// state_5.sqlite still exists, history.jsonl does not, and the sqlite set
// is larger. The claim that survives, and the only one this depends on, is
// that the lever relocates ALL of it.
//
// The operator's own home is the seed source, and auth.json is symlinked
// rather than copied; see SessionHomeSpec.LinkIn for why that distinction
// is load-bearing here.
//
// config.toml is not linked, and that is deliberate (marvel#308). The
// operator's file carries their project trust, which makes the target
// writable under `-s read-only` (finding-049), their approvals reviewer,
// and their own director identity. A session gets a file marvel writes
// instead: the codex-ctx hooks with their trust record, the director server
// with the names codex must forward to it, and the start directory declared
// untrusted rather than left untrusted by absence (orc finding-179 §5).
func (c *Codex) SessionHome(ctx *LaunchContext) (SessionHomeSpec, bool) {
	// An operator who set CODEX_HOME for the daemon has named the home
	// they want seeded from; otherwise it is codex's own default.
	source := os.Getenv(codexHomeEnv)
	if source == "" {
		if home, err := os.UserHomeDir(); err == nil {
			source = filepath.Join(home, ".codex")
		}
	}
	return SessionHomeSpec{
		EnvVar: codexHomeEnv,
		Source: source,
		LinkIn: []string{codexAuthFile},
		Seed:   codexSeeder(ctx, source),
		Socket: codexControlSocket,
	}, true
}

func (c *Codex) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	if ctx.Session.Runtime.Mode != api.RuntimeModeHeadless {
		// Interactive: launch codex as-is; the TUI owns the pane.
		return &LaunchResult{
			Command: buildCommand(binary, args),
			Env:     codexEnv(ctx),
		}, nil
	}

	if ctx.Session.Runtime.Prompt == "" {
		return nil, ErrNoPrompt
	}

	// `codex exec [OPTIONS] [PROMPT]`: options first, prompt last.
	// --skip-git-repo-check keeps codex from refusing a non-repo workspace.
	full := []string{"exec", "--json", "--skip-git-repo-check"}
	full = append(full, args...)
	full = append(full, ctx.Session.Runtime.Prompt)

	cmd := buildCommand(binary, full)
	// stdin must be closed even without a sink: codex exec appends piped
	// stdin to its prompt and would otherwise hang on the pane tty.
	cmd = redirectStdin(cmd, os.DevNull)

	result := &LaunchResult{
		Command: cmd,
		Env:     codexEnv(ctx),
	}
	if ctx.StreamPath != "" {
		result.Command = redirectStdout(result.Command, ctx.StreamPath)
		result.Stream = &StreamSpec{
			Format: StreamFormatCodexJSON,
			Path:   ctx.StreamPath,
		}
	}
	return result, nil
}

// codexEnv is baseEnv plus the private state home when the manager made
// one. Set here rather than in baseEnv because the variable is this
// harness's, and a generic stamp would hand CODEX_HOME to runtimes that do
// not read it.
func codexEnv(ctx *LaunchContext) map[string]string {
	env := baseEnv(ctx)
	if ctx.HarnessHomePath != "" {
		env[codexHomeEnv] = ctx.HarnessHomePath
	}
	return env
}

func init() {
	var _ Adapter = (*Codex)(nil)
	var _ StreamCapable = (*Codex)(nil)
	var _ SessionHomeAssigner = (*Codex)(nil)
}
