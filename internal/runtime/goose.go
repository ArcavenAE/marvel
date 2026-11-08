package runtime

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// gooseHomeEnv relocates goose's config, data and state under one root. goose
// ignores it unless it is absolute (goose-adapter-v1.md G9).
const gooseHomeEnv = "GOOSE_PATH_ROOT"

// Goose is the adapter for the goose CLI (docs/design/goose-adapter-v1.md and
// v1.1). It is the core of the adapter: the posture rules and their refusals,
// the private home with its seeded config and permission files, the projected
// env, and the launch line. A headless launch writes its stream-json to the
// sink marvel hands it, and internal/runtime/goose reads it (v1.1 C4).
//
// Interactive: goose launches plain, with the mode, the private home and the
// seeded files. Headless: `auto` inside a sandbox launches `goose run
// --output-format stream-json -q -t <prompt>` under the watchdog wrapper
// (`marvel goose-watch`, v1.1 C3), because goose streams without a bound (L4).
// `approve` and `smart_approve` stay refused because both fail there (v1.1 L5).
// A headless launch the wrapper cannot serve, a container placement or a marvel
// binary that cannot be found, is refused with `watchdog-unavailable` and never
// launched unbounded.
type Goose struct{}

func (g *Goose) Name() string { return "goose" }

// SupportsStream reports whether this launch produces a parseable stream. Only
// headless launches do: an interactive goose draws a terminal UI.
func (g *Goose) SupportsStream(ctx *LaunchContext) bool {
	return ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
}

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

// gooseRefuseNoPrompt is the slug of the refusal of a headless role that has no
// prompt. It stands apart from the build-phase refusal and survives its removal.
const gooseRefuseNoPrompt = "no-prompt"

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

	env := baseEnv(ctx)
	stderrFile := ""
	if filepath.IsAbs(ctx.HarnessHomePath) {
		stderrFile = filepath.Join(ctx.HarnessHomePath, gooseStderrName())
	}

	// A headless role with no prompt gets the refusal stub, never ErrNoPrompt: a
	// Prepare error is answered by the manager with the raw command, which would
	// launch goose at the host's GOOSE_MODE with none of the seeded files. This
	// guard stands on its own (operator ruling on goose empty prompts). Apply
	// refuses the same role earlier (api.ManifestRuntime, runtime.prompt).
	if headless && ctx.Session.Runtime.Prompt == "" {
		return gooseRefusalResult(ctx, env, stderrFile, gooseRefuseNoPrompt), nil
	}
	if reason := gooseRefusal(ctx); reason != "" {
		return gooseRefusalResult(ctx, env, stderrFile, reason), nil
	}
	// The watchdog is the bound on a headless run, so a launch it cannot serve
	// is refused and not launched without it. The wrapper is a subcommand of the
	// marvel binary, which is not in a container image, and it needs the binary
	// to be found.
	var watchExe string
	if headless && stderrFile == "" {
		// The wrapper requires its stderr file, so a launch with no absolute home
		// to put one in has no bound to run under.
		return gooseRefusalResult(ctx, env, stderrFile, gooseRefuseRelativeRoot), nil
	}
	if headless {
		exe, err := gooseExecutable()
		if ctx.Placement == api.PlacementContainer || err != nil {
			if err != nil && ctx.Placement != api.PlacementContainer {
				log.Printf("session %s: goose watchdog: marvel binary not found: %v", ctx.Session.Key(), err)
			}
			return gooseRefusalResult(ctx, env, stderrFile, gooseRefuseWatchdogUnavailable), nil
		}
		watchExe = exe
	}

	posture := goosePosture(ctx)
	env[gooseHomeEnv] = ctx.HarnessHomePath
	env[gooseModeEnv] = posture
	if dir, ok := gooseTempDir(ctx, ctx.HarnessHomePath); ok {
		env["TMPDIR"] = dir
	}
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

	// No `--resume`: every launch is a new goose session (v1.1 C2).
	full := []string{"run", "--output-format", "stream-json", "-q"}
	if b := gooseBuiltins(ctx.Session.Runtime.Goose); b != "" {
		full = append(full, "--no-profile", "--with-builtin", b)
	}
	full = append(full, args...)
	full = append(full, "-t", ctx.Session.Runtime.Prompt)
	cmd := redirectStdin(gooseWatchCommand(ctx, watchExe, binary, full, stderrFile), os.DevNull)
	result := &LaunchResult{
		Env:         env,
		FirstPrompt: newFirstPrompt(FirstPromptRole, ctx.Session.Runtime.Prompt),
	}
	// stdout is the stream sink and holds only goose's JSON lines, copied through
	// the wrapper. goose's stderr goes to the per-launch file, which the wrapper
	// opens and holds to its cap.
	if ctx.StreamPath != "" {
		cmd = redirectStdout(cmd, ctx.StreamPath)
		result.Stream = &StreamSpec{Format: StreamFormatGooseJSON, Path: ctx.StreamPath}
	}
	result.Command = cmd
	return result, nil
}

// The watchdog limits of C3. The wall-clock limit is the role's active_deadline
// for a scheduled role and gooseWallDefault otherwise. The idle default is a
// guess above the largest step seen between stream lines (30 s in one
// uncommitted run, 4 s in the committed set); the wrapper records the longest
// gap of every run so it can be measured.
const (
	gooseWallDefault = 45 * time.Minute
	gooseIdleLimit   = 300 * time.Second
	gooseGrace       = 10 * time.Second
	gooseStderrCap   = 64 << 10
)

// gooseRefuseWatchdogUnavailable is the slug of the refusal of a headless launch
// the watchdog cannot serve.
const gooseRefuseWatchdogUnavailable = "watchdog-unavailable"

// gooseWallLimit is the run's wall-clock limit.
func gooseWallLimit(ctx *LaunchContext) time.Duration {
	if ctx.Role != nil && ctx.Role.Schedule != nil && ctx.Role.Schedule.ActiveDeadline > 0 {
		return ctx.Role.Schedule.ActiveDeadline
	}
	return gooseWallDefault
}

// gooseWatchCommand is goose's command line under the wrapper:
// `marvel goose-watch <limits> --stderr-file F -- goose run ...`.
func gooseWatchCommand(ctx *LaunchContext, exe, binary string, gooseArgs []string, stderrFile string) string {
	args := []string{
		"goose-watch",
		"--wall", gooseWallLimit(ctx).String(),
		"--idle", gooseIdleLimit.String(),
		"--grace", gooseGrace.String(),
	}
	args = append(args, "--stderr-file", stderrFile, "--stderr-cap", fmt.Sprint(gooseStderrCap))
	// The command may carry arguments of its own; each field is quoted apart.
	args = append(args, "--")
	args = append(args, strings.Fields(binary)...)
	args = append(args, gooseArgs...)
	return buildCommand(shellQuote(exe), args)
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
// which never runs goose. It returns no error, because a Prepare error would not
// do: the manager falls back to the raw command on one, and that starts goose at
// whatever GOOSE_MODE the host has (v1 section 4).
//
// On a host placement the stub is a hidden subcommand of this binary, found the
// way the codex hook finds it, and its stderr goes to the same per-launch file a
// goose failure would, so the reap finds the reason in one place. When the binary
// cannot be found, the stub is the plain /bin/sh line below, with the same
// reason and the same file.
//
// On a container placement the manager wraps the whole command in the container,
// and the marvel binary is not in the image, so the stub there is the plain
// /bin/sh line and names no marvel path. Its line goes to the container's
// stderr, and whether it reaches the per-launch file from inside a container is
// untested.
func gooseRefusalResult(ctx *LaunchContext, env map[string]string, stderrFile, reason string) *LaunchResult {
	if ctx.Placement == api.PlacementContainer {
		return &LaunchResult{Command: gooseShellStub(reason), Env: env}
	}
	exe, err := gooseExecutable()
	if err != nil {
		log.Printf("session %s: goose refusal stub: marvel binary not found, using sh: %v", ctx.Session.Key(), err)
		return &LaunchResult{Command: withStderr(gooseShellStub(reason), stderrFile), Env: env}
	}
	cmd := buildCommand(shellQuote(exe), []string{"goose-refuse", reason})
	return &LaunchResult{Command: withStderr(cmd, stderrFile), Env: env}
}

// gooseShellStub is the refusal stub that needs no marvel binary: it prints the
// reason the way the subcommand does and exits 78.
func gooseShellStub(reason string) string {
	script := fmt.Sprintf("printf 'marvel: goose launch refused: %%s\\n' %s >&2; exit 78", shellQuote(reason))
	return "/bin/sh -c " + shellQuote(script)
}

func init() {
	var _ Adapter = (*Goose)(nil)
	var _ SessionHomeAssigner = (*Goose)(nil)
	var _ StreamCapable = (*Goose)(nil)
}
