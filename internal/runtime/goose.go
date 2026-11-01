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
// seeded files. Headless: every launch is refused for now (goose_buildphase.go),
// because nothing yet reads the stream or bounds the run. The headless posture
// rules and the headless launch line are written and tested behind that refusal:
// `auto` inside a sandbox will launch `goose run -q -t <prompt>`, and `approve`
// and `smart_approve` stay refused because both fail there (v1.1 L5).
type Goose struct {
	// headlessBuilt lifts the build-phase refusal of headless launches. Only
	// tests set it; see goose_buildphase.go.
	headlessBuilt bool
}

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

	env := baseEnv(ctx)
	stderrFile := ""
	if filepath.IsAbs(ctx.HarnessHomePath) {
		stderrFile = filepath.Join(ctx.HarnessHomePath, gooseStderrName())
	}

	// Build phase, before everything else that can refuse: a Prepare error is
	// answered by the manager with the raw command, so an error return here
	// would launch goose at the host's GOOSE_MODE. That includes the empty
	// prompt check below. See goose_buildphase.go.
	if reason := gooseBuildPhaseRefusal(headless, g.headlessBuilt); reason != "" {
		return gooseRefusalResult(ctx, env, stderrFile, reason), nil
	}
	if headless && ctx.Session.Runtime.Prompt == "" {
		return nil, ErrNoPrompt
	}
	if reason := gooseRefusal(ctx); reason != "" {
		return gooseRefusalResult(ctx, env, stderrFile, reason), nil
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
}
