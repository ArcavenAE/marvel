package runtime

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// gooseHomeEnv relocates goose's config, data and state under one root. goose
// ignores it unless it is absolute (goose-adapter-v1.md G9).
const gooseHomeEnv = "GOOSE_PATH_ROOT"

// Goose is the adapter for the goose CLI (docs/design/goose-adapter-v1.md and
// v1.1). It is the core of the adapter: the posture rules and their refusals,
// the private home with its seeded config and permission files, the projected
// env, and the launch line. It is not stream-capable yet: the stream parser and
// its wiring are a later change, and nothing here reads a stream.
//
// Interactive: goose launches plain, with the mode, the private home and the
// seeded files. Headless: only `auto` inside a sandbox launches, as
// `goose run -q -t <prompt>`; `approve` and `smart_approve` are refused because
// both fail there (v1.1 L5), and `auto` with no sandbox is refused.
type Goose struct{}

func (g *Goose) Name() string { return "goose" }

// ProjectionFor reports no projection surface: goose takes no settings-path
// flag, and its config is seeded into the private home instead.
func (g *Goose) ProjectionFor(_ *LaunchContext, _ string) ProjectionTarget {
	return ProjectionTarget{Supported: false}
}

// SessionHome gives every goose seat a private root. The operator's own goose
// directories are not a seed source in this change.
func (g *Goose) SessionHome(ctx *LaunchContext) (SessionHomeSpec, bool) {
	return SessionHomeSpec{EnvVar: gooseHomeEnv, Seed: gooseSeeder(ctx)}, true
}

// gooseNow is the clock the per-launch stderr file name reads, a seam for tests.
var gooseNow = func() time.Time { return time.Now().UTC() }

// gooseExecutable is the path of the binary that hosts the refusal stub, a seam
// for tests: under `go test` the running binary is the test binary, which has no
// goose-refuse subcommand.
var gooseExecutable = os.Executable

// gooseStderrName is the per-launch stderr file under the private home. Each
// launch gets its own, so a relaunch cannot overwrite the reason a refusal or a
// failure left (goose-adapter-v1.md section 3).
func gooseStderrName() string {
	return "stderr-" + gooseNow().Format("20060102T150405.000000000Z") + ".log"
}

func (g *Goose) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}
	headless := ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
	if headless && ctx.Session.Runtime.Prompt == "" {
		return nil, ErrNoPrompt
	}

	env := baseEnv(ctx)
	stderrFile := ""
	if filepath.IsAbs(ctx.HarnessHomePath) {
		stderrFile = filepath.Join(ctx.HarnessHomePath, gooseStderrName())
	}

	if reason := gooseRefusal(ctx); reason != "" {
		return gooseRefusalResult(env, stderrFile, reason)
	}

	posture := goosePosture(ctx)
	env[gooseHomeEnv] = ctx.HarnessHomePath
	env[gooseModeEnv] = posture
	env["OTEL_SDK_DISABLED"] = "true"
	env["GOOSE_TELEMETRY_ENABLED"] = "false"
	env["GOOSE_DISABLE_SESSION_NAMING"] = "1"
	limit, note := gooseContextLimit(ctx.Session.Runtime.ContextWindow, ctx.Session.Runtime.Env["GOOSE_MODEL"])
	if limit > 0 {
		env["GOOSE_CONTEXT_LIMIT"] = fmt.Sprint(limit)
	}
	if note != "" {
		log.Printf("session %s: NOTE: %s", ctx.Session.Key(), note)
	}

	args := append([]string(nil), ctx.Session.Runtime.Args...)
	if !headless {
		cmd := buildCommand(binary, args)
		return &LaunchResult{Command: withStderr(cmd, stderrFile), Env: env}, nil
	}

	// No `--resume`: every launch is a new goose session (v1.1 C2). No
	// `--output-format stream-json` yet, since nothing here reads the stream.
	full := []string{"run", "-q"}
	if b := gooseBuiltins(ctx.Session.Runtime.Goose); b != "" {
		full = append(full, "--no-profile", "--with-builtin", b)
	}
	full = append(full, args...)
	full = append(full, "-t", ctx.Session.Runtime.Prompt)
	cmd := redirectStdin(buildCommand(binary, full), os.DevNull)
	return &LaunchResult{
		Command:     withStderr(cmd, stderrFile),
		Env:         env,
		FirstPrompt: newFirstPrompt(FirstPromptRole, ctx.Session.Runtime.Prompt),
	}, nil
}

// withStderr sends the command's stderr to file, or leaves it in the pane when
// there is no file.
func withStderr(cmd, file string) string {
	if file == "" {
		return cmd
	}
	return cmd + " 2> " + shellQuote(file)
}

// gooseRefusalResult is the launch of a refused goose seat: the exit-78 stub,
// which never runs goose. A Prepare error would not do, because the manager
// falls back to the raw command on one and that starts goose at whatever
// GOOSE_MODE the host has (v1 section 4). The stub's stderr goes to the same
// per-launch file a goose failure would, so the reap finds the reason in one
// place. The stub is a hidden subcommand of this binary, found the way the
// codex hook finds it.
func gooseRefusalResult(env map[string]string, stderrFile, reason string) (*LaunchResult, error) {
	exe, err := gooseExecutable()
	if err != nil {
		return nil, fmt.Errorf("resolve marvel binary for the goose refusal stub: %w", err)
	}
	cmd := buildCommand(shellQuote(exe), []string{"goose-refuse", reason})
	return &LaunchResult{Command: withStderr(cmd, stderrFile), Env: env}, nil
}

func init() {
	var _ Adapter = (*Goose)(nil)
	var _ SessionHomeAssigner = (*Goose)(nil)
}
