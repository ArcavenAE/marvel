package runtime

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
)

// gooseContext is a goose launch in the shape the manager hands Prepare: a
// private home that exists and is absolute, localhost placement, and no sandbox
// profile. mutate edits the runtime before the launch is built.
func gooseContext(t *testing.T, mutate func(rt *api.Runtime)) *LaunchContext {
	t.Helper()
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{
		Name:    "goose",
		Command: "goose",
		Goose:   &api.GooseRuntime{Posture: api.GoosePostureApprove},
	}
	if mutate != nil {
		mutate(&ctx.Session.Runtime)
	}
	// The manager copies the role's runtime into the session at spawn, so the two
	// agree; baseEnv reads the role's, the adapter's own rules the session's.
	ctx.Role.Runtime = ctx.Session.Runtime
	ctx.HarnessHomePath = t.TempDir()
	ctx.Placement = api.PlacementLocalhost
	return ctx
}

func headless(rt *api.Runtime) {
	rt.Mode = api.RuntimeModeHeadless
	rt.Prompt = "say ok"
}

func TestGooseIsRegisteredAndHasAStream(t *testing.T) {
	t.Parallel()
	a := NewRegistry().Resolve("goose")
	if a.Name() != "goose" {
		t.Fatalf("Resolve(goose) = %q", a.Name())
	}
	sc, ok := a.(StreamCapable)
	if !ok {
		t.Fatal("goose writes stream-json headless, so it must be StreamCapable")
	}
	if !sc.SupportsStream(gooseContext(t, headless)) {
		t.Error("a headless goose launch must support a stream")
	}
	if sc.SupportsStream(gooseContext(t, nil)) {
		t.Error("an interactive goose launch draws a terminal UI and has no stream")
	}
	if _, ok := a.(SessionHomeAssigner); !ok {
		t.Error("goose must give each seat a private root")
	}
	if _, ok := a.(SandboxRecommender); ok {
		t.Error("goose does not recommend a container in v1 (v1.1 section 6, decision 2)")
	}
}

// Each of the seven refusal reasons, from the launch that earns it. The stub's
// command line names the reason, and goose is never started.
func TestGooseRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*api.Runtime)
		adjust func(*LaunchContext)
		want   string
	}{
		{"no posture", func(rt *api.Runtime) { rt.Goose = nil }, nil, "no-posture"},
		{"empty table, no posture", func(rt *api.Runtime) { rt.Goose = &api.GooseRuntime{} }, nil, "no-posture"},
		{"smart_approve on a headless role", func(rt *api.Runtime) {
			headless(rt)
			rt.Goose.Posture = api.GoosePostureSmartApprove
		}, nil, "smart-approve-headless"},
		{"approve on a headless role", headless, nil, "approve-headless"},
		{"relative root", nil, func(c *LaunchContext) { c.HarnessHomePath = "relative/home" }, "relative-root"},
		{"no root", nil, func(c *LaunchContext) { c.HarnessHomePath = "" }, "relative-root"},
		{"role env disagrees on GOOSE_MODE", func(rt *api.Runtime) {
			rt.Env = map[string]string{"GOOSE_MODE": "auto"}
		}, nil, "role-env-conflict"},
		{"auto with a deny list", func(rt *api.Runtime) {
			rt.Goose = &api.GooseRuntime{Posture: api.GoosePostureAuto, ToolsDeny: []string{"fetch"}}
		}, nil, "auto-with-deny-list"},
		{"headless auto, no placement, no profile", func(rt *api.Runtime) {
			headless(rt)
			rt.Goose.Posture = api.GoosePostureAuto
		}, func(c *LaunchContext) { c.Placement = "" }, "auto-unsandboxed"},
		{"headless auto on localhost with no profile", func(rt *api.Runtime) {
			headless(rt)
			rt.Goose.Posture = api.GoosePostureAuto
		}, nil, "auto-unsandboxed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := gooseContext(t, tc.mutate)
			if tc.adjust != nil {
				tc.adjust(ctx)
			}
			if ctx.Session.Runtime.Mode == api.RuntimeModeHeadless {
				// A headless launch the posture rules allow goes on to the wrapper
				// (TestGooseHeadlessLaunchLine), so the rules are read directly.
				if got := gooseRefusal(ctx); got != tc.want {
					t.Errorf("gooseRefusal = %q, want %q", got, tc.want)
				}
				if tc.want != "" {
					res, err := (&Goose{}).Prepare(ctx)
					if err != nil {
						t.Fatalf("Prepare: %v (an error would launch the raw command)", err)
					}
					if !strings.Contains(res.Command, "'goose-refuse' '"+tc.want+"'") {
						t.Errorf("command %q is not the stub for %q", res.Command, tc.want)
					}
				}
				return
			}
			res, err := (&Goose{}).Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v (an error would launch the raw command)", err)
			}
			if !strings.Contains(res.Command, "'goose-refuse' '"+tc.want+"'") {
				t.Errorf("command %q is not the stub for %q", res.Command, tc.want)
			}
			if strings.Contains(res.Command, "goose '") || strings.HasPrefix(res.Command, "goose ") {
				t.Errorf("command %q starts goose", res.Command)
			}
		})
	}
}

func TestGooseAutoLaunchesOnlyWhereASandboxIsInForce(t *testing.T) {
	t.Parallel()
	auto := func(rt *api.Runtime) {
		headless(rt)
		rt.Goose.Posture = api.GoosePostureAuto
	}
	t.Run("profile path, no container", func(t *testing.T) {
		t.Parallel()
		ctx := gooseContext(t, auto)
		ctx.SandboxProfile = "/etc/marvel/goose.sb"
		res, err := (&Goose{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := res.Env["GOOSE_MODE"]; got != "auto" {
			t.Errorf("GOOSE_MODE = %q, want auto", got)
		}
		if strings.Contains(res.Command, "goose-refuse") || !strings.Contains(res.Command, "'goose-watch'") {
			t.Errorf("command %q is not the watched goose launch", res.Command)
		}
	})
	// The container case of v1.1 acceptance test 2, amended by L3: a container
	// still counts as a sandbox, so the posture rules pass, but the wrapper is a
	// subcommand of the marvel binary, which the image does not hold, so the
	// launch is refused with watchdog-unavailable and never runs unbounded.
	t.Run("container placement, no profile", func(t *testing.T) {
		t.Parallel()
		ctx := gooseContext(t, auto)
		ctx.Placement = api.PlacementContainer
		if got := gooseRefusal(ctx); got != "" {
			t.Fatalf("gooseRefusal = %q: a container must still count as a sandbox", got)
		}
		res, err := (&Goose{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if !strings.Contains(res.Command, "watchdog-unavailable") || strings.Contains(res.Command, "goose-watch") {
			t.Errorf("command %q is not the watchdog-unavailable stub", res.Command)
		}
	})
}

// With a sink offered, stdout goes to it and the spec names the goose format;
// stderr goes to the per-launch file through the wrapper, so the stream holds
// only goose's JSON lines.
func TestGooseHeadlessLaunchWiresTheStream(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) {
		headless(rt)
		rt.Goose = &api.GooseRuntime{Posture: api.GoosePostureAuto}
	})
	ctx.SandboxProfile = "/etc/marvel/goose.sb"
	ctx.StreamPath = "/run/marvel/goose.fifo"
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.Stream == nil || res.Stream.Format != StreamFormatGooseJSON || res.Stream.Path != ctx.StreamPath {
		t.Fatalf("stream = %+v, want goose/stream-json at %s", res.Stream, ctx.StreamPath)
	}
	if want := "< '/dev/null' > '/run/marvel/goose.fifo'"; !strings.HasSuffix(res.Command, want) {
		t.Errorf("command %q does not end with %q", res.Command, want)
	}
	if strings.Contains(res.Command, " 2> ") {
		t.Errorf("command %q redirects stderr in the shell; the wrapper holds it to the cap", res.Command)
	}
	// The format is registered, so the manager can read the sink.
	if _, err := NewStreamParser(res.Stream.Format, StreamParserConfig{}); err != nil {
		t.Errorf("NewStreamParser(%s): %v", res.Stream.Format, err)
	}
}

func TestGooseHeadlessLaunchLine(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) {
		headless(rt)
		rt.Goose = &api.GooseRuntime{Posture: api.GoosePostureAuto, Extensions: []string{"developer", "memory"}}
	})
	ctx.SandboxProfile = "/etc/marvel/goose.sb"
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, want := range []string{
		"'goose-watch' '--wall' '45m0s' '--idle' '5m0s' '--grace' '10s' '--stderr-file' '" + ctx.HarnessHomePath + "/stderr-",
		"'--stderr-cap' '65536' '--' 'goose' 'run' '--output-format' 'stream-json' '-q'",
		"'--no-profile' '--with-builtin' 'developer,memory'", "'-t' 'say ok'", "< '/dev/null'",
	} {
		if !strings.Contains(res.Command, want) {
			t.Errorf("command %q lacks %q", res.Command, want)
		}
	}
	if strings.Contains(res.Command, " 2> ") {
		t.Errorf("command %q redirects stderr in the shell", res.Command)
	}
	if strings.Contains(res.Command, "--resume") {
		t.Errorf("command %q carries --resume: every launch is a new session", res.Command)
	}
	if res.Stream != nil || strings.Contains(res.Command, "' > '") {
		t.Errorf("no StreamPath was offered, yet stream = %+v and command = %q", res.Stream, res.Command)
	}
	if res.FirstPrompt.Source != FirstPromptRole {
		t.Errorf("first prompt source = %q, want role", res.FirstPrompt.Source)
	}
}

func TestGooseInteractiveLaunchesPlainWithTheModeAndTheHome(t *testing.T) {
	t.Parallel()
	for _, posture := range []string{api.GoosePostureApprove, api.GoosePostureSmartApprove, api.GoosePostureAuto} {
		t.Run(posture, func(t *testing.T) {
			t.Parallel()
			ctx := gooseContext(t, func(rt *api.Runtime) { rt.Goose.Posture = posture })
			res, err := (&Goose{}).Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if !strings.HasPrefix(res.Command, "goose") || strings.Contains(res.Command, "goose-refuse") {
				t.Errorf("command %q is not a plain goose launch", res.Command)
			}
			if res.Env["GOOSE_MODE"] != posture || res.Env["GOOSE_PATH_ROOT"] != ctx.HarnessHomePath {
				t.Errorf("env GOOSE_MODE=%q GOOSE_PATH_ROOT=%q", res.Env["GOOSE_MODE"], res.Env["GOOSE_PATH_ROOT"])
			}
		})
	}
}

func TestGooseProjectedEnv(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) {
		rt.Env = map[string]string{"GOOSE_MODE": "approve", "GOOSE_PROVIDER": "ollama", "GOOSE_MODEL": "qwen3:0.6b"}
	})
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for k, want := range map[string]string{
		"GOOSE_PATH_ROOT":              ctx.HarnessHomePath,
		"GOOSE_MODE":                   "approve",
		"GOOSE_PROVIDER":               "ollama",
		"GOOSE_MODEL":                  "qwen3:0.6b",
		"OTEL_SDK_DISABLED":            "true",
		"GOOSE_TELEMETRY_ENABLED":      "false",
		"GOOSE_DISABLE_SESSION_NAMING": "1",
		"GOOSE_CONTEXT_LIMIT":          "4096",
	} {
		if got := res.Env[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestGooseContextLimitProjection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		window int
		model  string
		want   string
	}{
		{"role lower than the table", 2048, "qwen3:0.6b", "2048"},
		{"role higher than the table", 32768, "qwen3:0.6b", "4096"},
		{"table only", 0, "qwen3:0.6b", "4096"},
		{"model with no entry", 8192, "qwen3:8b", "8192"},
		{"nothing set", 0, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := gooseContext(t, func(rt *api.Runtime) {
				rt.ContextWindow = tc.window
				if tc.model != "" {
					rt.Env = map[string]string{"GOOSE_MODEL": tc.model}
				}
			})
			res, err := (&Goose{}).Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := res.Env["GOOSE_CONTEXT_LIMIT"]; got != tc.want {
				t.Errorf("GOOSE_CONTEXT_LIMIT = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGoosePrepareErrors(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) { rt.Command = ""; rt.Name = "" })
	if _, err := (&Goose{}).Prepare(ctx); !errors.Is(err, ErrNoCommand) {
		t.Errorf("no command: err = %v, want ErrNoCommand", err)
	}
}

// A headless role with no prompt is refused with its own slug. The reason is
// never an error, because the manager would answer one with the raw command.
func TestGooseHeadlessNoPromptIsRefusedWithItsOwnSlug(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) { rt.Mode = api.RuntimeModeHeadless })
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare returned %v: the manager would launch the raw command", err)
	}
	if !strings.Contains(res.Command, "no-prompt") {
		t.Errorf("command %q does not name no-prompt", res.Command)
	}
	if strings.Contains(res.Command, "'goose' 'run'") {
		t.Errorf("command %q starts goose", res.Command)
	}
}

func TestGooseSeedsTheConfigAndPermissionFiles(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) {
		rt.Env = map[string]string{"GOOSE_PROVIDER": "ollama", "GOOSE_MODEL": "qwen3:0.6b"}
		rt.Goose = &api.GooseRuntime{
			Posture:    api.GoosePostureApprove,
			ToolsAllow: []string{"developer__text_editor", "developer__text_editor"},
			ToolsDeny:  []string{"developer__shell"},
			Extensions: []string{"developer"},
		}
	})
	spec, ok := (&Goose{}).SessionHome(ctx)
	if !ok || spec.EnvVar != "GOOSE_PATH_ROOT" || spec.Seed == nil {
		t.Fatalf("SessionHome = %+v, %v", spec, ok)
	}
	dir := t.TempDir()
	if err := spec.Seed(dir); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	var perm map[string]struct {
		AlwaysAllow []string `yaml:"always_allow"`
		AskBefore   []string `yaml:"ask_before"`
		NeverAllow  []string `yaml:"never_allow"`
	}
	raw := readFile(t, filepath.Join(dir, "config", "permission.yaml"))
	if err := yaml.Unmarshal(raw, &perm); err != nil {
		t.Fatalf("permission.yaml: %v", err)
	}
	u := perm["user"]
	if got := strings.Join(u.AlwaysAllow, ","); got != "developer__text_editor" {
		t.Errorf("always_allow = %q", got)
	}
	if got := strings.Join(u.NeverAllow, ","); got != "developer__shell,extensionmanager__manage_extensions" {
		t.Errorf("never_allow = %q, want the role's deny list then manage_extensions", got)
	}
	// goose requires all three lists; an empty one must be a list and not null.
	if !strings.Contains(string(raw), "ask_before: []") {
		t.Errorf("ask_before is not an empty list in:\n%s", raw)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(readFile(t, filepath.Join(dir, "config", "config.yaml")), &cfg); err != nil {
		t.Fatalf("config.yaml: %v", err)
	}
	if cfg["GOOSE_MODE"] != "approve" || cfg["GOOSE_PROVIDER"] != "ollama" || cfg["GOOSE_MODEL"] != "qwen3:0.6b" {
		t.Errorf("config.yaml = %v", cfg)
	}
	exts, _ := cfg["extensions"].(map[string]any)
	dev, _ := exts["developer"].(map[string]any)
	if dev["enabled"] != true || dev["type"] != "builtin" || dev["name"] != "developer" {
		t.Errorf("extensions = %v", exts)
	}
}

// The reason carries no credential: nothing the seed writes comes from outside
// the role's own declared values.
func TestGooseSeedWritesNoFileForARefusedSeat(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) { rt.Goose = nil })
	spec, _ := (&Goose{}).SessionHome(ctx)
	dir := t.TempDir()
	if err := spec.Seed(dir); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a seat with no posture got files: %v", entries)
	}
}

func TestGooseStderrFileIsPerLaunch(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, nil)
	first, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file := func(cmd string) string { return cmd[strings.LastIndex(cmd, " 2> "):] }
	if file(first.Command) == file(second.Command) {
		t.Errorf("two launches share one stderr file: %s", file(first.Command))
	}
}

// The refusal's reason lands in the per-launch stderr file in the private home,
// where the reap reads a failure from, and the stub exits 78. This runs the real
// stub: the marvel binary is built and the launch command is run through sh.
func TestGooseRefusalReasonLandsInThePerLaunchStderrFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the marvel binary")
	}
	bin := filepath.Join(t.TempDir(), "marvel")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/marvel")
	var out bytes.Buffer
	build.Stdout, build.Stderr = &out, &out
	if err := build.Run(); err != nil {
		t.Fatalf("build marvel: %v\n%s", err, out.String())
	}
	old := gooseExecutable
	gooseExecutable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { gooseExecutable = old })

	// auto with a deny list on an interactive role: the host-placement stub.
	ctx := gooseContext(t, func(rt *api.Runtime) {
		rt.Goose = &api.GooseRuntime{Posture: api.GoosePostureAuto, ToolsDeny: []string{"fetch"}}
	})
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cmd := exec.Command("sh", "-c", res.Command)
	runErr := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(runErr, &ee) || ee.ExitCode() != 78 {
		t.Fatalf("stub exit = %v, want 78", runErr)
	}
	files, _ := filepath.Glob(filepath.Join(ctx.HarnessHomePath, "stderr-*.log"))
	if len(files) != 1 {
		t.Fatalf("stderr files = %v, want exactly one", files)
	}
	if got := string(readFile(t, files[0])); got != "marvel: goose launch refused: auto-with-deny-list\n" {
		t.Errorf("stderr file = %q", got)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// A headless launch the watchdog cannot serve is refused and never launched
// without it. On a container placement the whole command runs in the image,
// where the marvel binary is not, so the stub there is a plain sh line that names
// no marvel path and ends in exit 78 (v1.1 section 6 decision 2, as amended by
// L3). The seam points the host stub at a sentinel path so a leak of it into the
// container command shows.
func TestGooseHeadlessInAContainerIsRefusedWithoutTheWatchdog(t *testing.T) {
	old := gooseExecutable
	gooseExecutable = func() (string, error) { return "/MARVEL-BIN/marvel", nil }
	t.Cleanup(func() { gooseExecutable = old })

	ctx := gooseContext(t, func(rt *api.Runtime) {
		headless(rt)
		rt.Goose.Posture = api.GoosePostureAuto
	})
	ctx.Placement = api.PlacementContainer
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, bad := range []string{"MARVEL-BIN", "goose-refuse", "goose-watch", ctx.HarnessHomePath} {
		if strings.Contains(res.Command, bad) {
			t.Errorf("container stub %q names %q", res.Command, bad)
		}
	}
	if !strings.HasPrefix(res.Command, "/bin/sh -c ") || !strings.HasSuffix(res.Command, "exit 78'") {
		t.Errorf("container stub %q is not a plain sh line ending exit 78", res.Command)
	}
	out, runErr := exec.Command("sh", "-c", res.Command).CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(runErr, &ee) || ee.ExitCode() != 78 {
		t.Fatalf("exit = %v, want 78", runErr)
	}
	if string(out) != "marvel: goose launch refused: watchdog-unavailable\n" {
		t.Errorf("output = %q", out)
	}
	if res.Stream != nil {
		t.Errorf("a refused launch wired a stream: %+v", res.Stream)
	}
}

// When the marvel binary cannot be found the wrapper cannot run, so a headless
// launch gets the same refusal, and an interactive launch is unaffected.
func TestGooseHeadlessWithoutAMarvelBinaryIsRefusedWithoutTheWatchdog(t *testing.T) {
	old := gooseExecutable
	gooseExecutable = func() (string, error) { return "", errors.New("no executable") }
	t.Cleanup(func() { gooseExecutable = old })

	ctx := gooseContext(t, func(rt *api.Runtime) {
		headless(rt)
		rt.Goose.Posture = api.GoosePostureAuto
	})
	ctx.SandboxProfile = "/etc/marvel/goose.sb"
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasPrefix(res.Command, "/bin/sh -c ") || !strings.Contains(res.Command, "watchdog-unavailable") || strings.Contains(res.Command, "goose-watch") {
		t.Errorf("command %q is not the watchdog-unavailable stub", res.Command)
	}

	inter, err := (&Goose{}).Prepare(gooseContext(t, nil))
	if err != nil {
		t.Fatalf("interactive Prepare: %v", err)
	}
	if strings.Contains(inter.Command, "watchdog-unavailable") || strings.Contains(inter.Command, "goose-watch") {
		t.Errorf("an interactive launch was refused or wrapped: %q", inter.Command)
	}
}

// The wall-clock limit is the role's active_deadline for a scheduled role and
// 45 minutes otherwise (v1.1 C3).
func TestGooseWallLimitFollowsTheScheduleDeadline(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		sched *api.SchedulePolicy
		want  string
	}{
		"unscheduled role":       {nil, "'--wall' '45m0s'"},
		"scheduled, no deadline": {&api.SchedulePolicy{}, "'--wall' '45m0s'"},
		"scheduled, a deadline":  {&api.SchedulePolicy{ActiveDeadline: 20 * time.Minute}, "'--wall' '20m0s'"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := gooseContext(t, func(rt *api.Runtime) {
				headless(rt)
				rt.Goose.Posture = api.GoosePostureAuto
			})
			ctx.SandboxProfile = "/etc/marvel/goose.sb"
			ctx.Role.Schedule = tc.sched
			res, err := (&Goose{}).Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if !strings.Contains(res.Command, tc.want) {
				t.Errorf("command %q lacks %q", res.Command, tc.want)
			}
		})
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadGooseStderrTailReadsOnlyTheEnd(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "stderr.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// A file far larger than any read would be if it were read whole: sparse, so
	// a read of the whole file would take a gigabyte of memory.
	if err := f.Truncate(1 << 30); err != nil {
		t.Skipf("no sparse files here: %v", err)
	}
	tail := "first partial line fragment\nsecond line\nlast line\n"
	if _, err := f.WriteAt([]byte(tail), 1<<30-int64(len(tail))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, n, err := readGooseStderrTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("redacted %d, want 0", n)
	}
	if len(got) > gooseStderrTailBytes {
		t.Errorf("tail is %d bytes, over the %d cap", len(got), gooseStderrTailBytes)
	}
	if !strings.HasSuffix(got, "second line\nlast line") {
		t.Errorf("tail = %q, does not end with the last lines", got)
	}
}

func TestReadGooseStderrTailSmallFileIsKeptWhole(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "stderr.log")
	writeFile(t, path, []byte("marvel: goose launch refused: no-posture\n"))
	got, _, err := readGooseStderrTail(path)
	if err != nil || got != "marvel: goose launch refused: no-posture" {
		t.Errorf("got %q, %v", got, err)
	}
}

// Every shape is built from parts at run time, from characters that are not a
// credential, so no scanner reads this file as holding one.
func TestReadGooseStderrTailRedactsCredentialShapes(t *testing.T) {
	t.Parallel()
	shapes := map[string]string{
		"aws access key":    "AKIA" + strings.Repeat("Q", 16),
		"sk key":            "sk-" + strings.Repeat("z", 30),
		"github token":      "ghp_" + strings.Repeat("a", 30),
		"github fine token": "github_pat_" + strings.Repeat("b", 30),
		"slack token":       "xoxb-" + strings.Repeat("1", 12),
		"bearer":            "Authorization: Bearer " + strings.Repeat("c", 24),
		"key assignment":    "api_key=" + strings.Repeat("d", 12),
		"private key":       "-----BEGIN " + "PRIVATE KEY-----\n" + strings.Repeat("e", 20) + "\n-----END " + "PRIVATE KEY-----",
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "stderr.log")
			writeFile(t, path, []byte("before\n"+shape+"\nafter\n"))
			got, n, err := readGooseStderrTail(path)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 || !strings.Contains(got, "[redacted]") {
				t.Errorf("nothing redacted in %q", got)
			}
			secret := strings.Repeat("c", 24)
			if name != "bearer" {
				fields := strings.FieldsFunc(shape, func(r rune) bool { return r == '=' || r == '\n' })
				secret = fields[len(fields)-1]
				if name == "private key" {
					secret = strings.Repeat("e", 20)
				}
			}
			if strings.Contains(got, secret) || !strings.Contains(got, "before") || !strings.Contains(got, "after") {
				t.Errorf("tail = %q", got)
			}
		})
	}
}

func TestReadGooseStderrTailRefusesANonRegularFile(t *testing.T) {
	t.Parallel()
	if _, _, err := readGooseStderrTail(t.TempDir()); err == nil {
		t.Error("a directory was read")
	}
	if _, _, err := readGooseStderrTail(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("an absent file was read")
	}
}

// The production setting is `&Goose{}`. A Prepare error is answered by the
// manager with the raw command, so a headless role with an empty prompt must get
// the stub, named no-prompt, and not ErrNoPrompt (the manager's fallback would
// start goose at the host's GOOSE_MODE, with none of the seeded files).
func TestGooseHeadlessEmptyPromptIsRefused(t *testing.T) {
	t.Parallel()
	for _, placement := range []api.Placement{api.PlacementLocalhost, api.PlacementContainer, ""} {
		t.Run(string(placement), func(t *testing.T) {
			t.Parallel()
			ctx := gooseContext(t, func(rt *api.Runtime) { rt.Mode = api.RuntimeModeHeadless })
			ctx.Placement = placement
			res, err := (&Goose{}).Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare returned %v: the manager would launch the raw command", err)
			}
			if !strings.Contains(res.Command, "no-prompt") {
				t.Errorf("command %q does not name no-prompt", res.Command)
			}
			if strings.Contains(res.Command, "goose 'run'") {
				t.Errorf("command %q starts goose", res.Command)
			}
		})
	}
}

// When the marvel binary cannot be found the stub is the plain sh line, with the
// same reason and the same per-launch file, and Prepare still returns no error.
func TestGooseRefusalWithoutAMarvelBinaryUsesTheShellStub(t *testing.T) {
	old := gooseExecutable
	gooseExecutable = func() (string, error) { return "", errors.New("no executable") }
	t.Cleanup(func() { gooseExecutable = old })

	ctx := gooseContext(t, func(rt *api.Runtime) {
		rt.Goose = &api.GooseRuntime{Posture: api.GoosePostureAuto, ToolsDeny: []string{"fetch"}}
	})
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare returned %v: the manager would launch the raw command", err)
	}
	if !strings.HasPrefix(res.Command, "/bin/sh -c ") || strings.Contains(res.Command, "goose-refuse") {
		t.Fatalf("command %q is not the plain sh stub", res.Command)
	}
	runErr := exec.Command("sh", "-c", res.Command).Run()
	var ee *exec.ExitError
	if !errors.As(runErr, &ee) || ee.ExitCode() != 78 {
		t.Fatalf("exit = %v, want 78", runErr)
	}
	files, _ := filepath.Glob(filepath.Join(ctx.HarnessHomePath, "stderr-*.log"))
	if len(files) != 1 {
		t.Fatalf("stderr files = %v, want exactly one", files)
	}
	if got := string(readFile(t, files[0])); got != "marvel: goose launch refused: auto-with-deny-list\n" {
		t.Errorf("stderr file = %q", got)
	}
}

// GooseStderrTail reads the newest per-launch file of a session home: the stamp
// in the name sorts in launch order. A home with no file, or a relative home, is
// not a read of anything.
func TestGooseStderrTailReadsTheNewestLaunchAndRedacts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for name, body := range map[string]string{
		"stderr-20261103T040000.000000000Z.log": "older launch\n",
		"stderr-20261103T050000.000000000Z.log": "newest launch\ntoken=abcdef0123456789\n",
		"other.log":                             "not a launch file\n",
	} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	got, err := GooseStderrTail(home)
	if err != nil {
		t.Fatalf("GooseStderrTail: %v", err)
	}
	if !strings.Contains(got, "newest launch") || strings.Contains(got, "older") || strings.Contains(got, "abcdef0123456789") {
		t.Errorf("tail = %q, want the newest launch with the token replaced", got)
	}
	if empty, err := GooseStderrTail(t.TempDir()); err != nil || empty != "" {
		t.Errorf("empty home: tail %q, err %v, want neither", empty, err)
	}
	if _, err := GooseStderrTail("relative/home"); err == nil {
		t.Error("a relative home was read")
	}
}
