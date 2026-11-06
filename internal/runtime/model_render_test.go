package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// runtime.model rendering (docs/design/role-runtime-model.md). Each adapter
// that can be handed a model passes it as its harness's own flag; without a
// declared model the command is exactly what it was.

func modelCtx(name string, mode api.RuntimeMode, model string) *LaunchContext {
	ctx := testContext()
	ctx.Session.Runtime.Name = name
	ctx.Session.Runtime.Command = name
	ctx.Session.Runtime.Args = nil
	ctx.Session.Runtime.Mode = mode
	ctx.Session.Runtime.Prompt = "do it"
	ctx.Session.Runtime.Model = model
	ctx.Role.Runtime = ctx.Session.Runtime
	return ctx
}

func prepared(t *testing.T, a Adapter, ctx *LaunchContext) string {
	t.Helper()
	r, err := a.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return r.Command
}

// without removes the one model flag pair from a command, so the rest can be
// compared with the command of a role that declared no model.
func without(cmd, flag, model string) string {
	return strings.Replace(cmd, " "+shellQuote(flag)+" "+shellQuote(model), "", 1)
}

func TestClaudePrepareRendersModel(t *testing.T) {
	t.Parallel()
	for label, mode := range map[string]api.RuntimeMode{"interactive": api.RuntimeModeInteractive, "headless": api.RuntimeModeHeadless} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			base := prepared(t, &Claude{}, modelCtx("claude", mode, ""))
			got := prepared(t, &Claude{}, modelCtx("claude", mode, "sonnet"))
			pair := shellQuote("--model") + " " + shellQuote("sonnet")
			if n := strings.Count(got, pair); n != 1 {
				t.Fatalf("%q appears %d times, want once: %s", pair, n, got)
			}
			if strings.Index(got, pair) > strings.Index(got, shellQuote("do it")) {
				t.Errorf("the model flag comes after the positional prompt: %s", got)
			}
			if rest := without(got, "--model", "sonnet"); rest != base {
				t.Errorf("a declared model changed more than the model flag:\n got  %s\n base %s", rest, base)
			}
		})
	}
}

func TestCodexPrepareRendersModel(t *testing.T) {
	t.Parallel()
	inter := prepared(t, &Codex{}, modelCtx("codex", api.RuntimeModeInteractive, "gpt-5"))
	if !strings.Contains(inter, shellQuote("-m")+" "+shellQuote("gpt-5")) {
		t.Errorf("interactive codex has no -m gpt-5: %s", inter)
	}
	head := prepared(t, &Codex{}, modelCtx("codex", api.RuntimeModeHeadless, "gpt-5"))
	pair := shellQuote("-m") + " " + shellQuote("gpt-5")
	i := strings.Index(head, pair)
	if i < 0 {
		t.Fatalf("headless codex has no -m gpt-5: %s", head)
	}
	if i < strings.Index(head, shellQuote("--skip-git-repo-check")) || i > strings.Index(head, shellQuote("do it")) {
		t.Errorf("-m is not after --skip-git-repo-check and before the prompt: %s", head)
	}
}

func TestOpenCodePrepareRendersModel(t *testing.T) {
	t.Parallel()
	const model = "anthropic/claude-sonnet-5-5"
	pair := shellQuote("-m") + " " + shellQuote(model)
	if got := prepared(t, &OpenCode{}, modelCtx("opencode", api.RuntimeModeInteractive, model)); !strings.Contains(got, pair) {
		t.Errorf("interactive opencode has no %s: %s", pair, got)
	}
	head := prepared(t, &OpenCode{}, modelCtx("opencode", api.RuntimeModeHeadless, model))
	i := strings.Index(head, pair)
	if i < 0 {
		t.Fatalf("opencode run has no %s: %s", pair, head)
	}
	if i < strings.Index(head, shellQuote("run")) || i > strings.Index(head, shellQuote("do it")) {
		t.Errorf("-m is not after run and before the message: %s", head)
	}
}

// The capability is optional and per adapter: the harnesses that cannot be
// handed a model do not implement it, and the registry answers for a name it
// does not know as it does for the fallback.
func TestRefusingAdaptersReportNoModelCapability(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	for _, a := range []Adapter{&Generic{}, &Simulator{}, &Forestage{}} {
		if _, ok := a.(ModelCapable); ok {
			t.Errorf("%s implements ModelCapable", a.Name())
		}
		if reg.CanRenderModel(a.Name()) {
			t.Errorf("registry says %s can render a model", a.Name())
		}
	}
	if reg.CanRenderModel("no-such-harness") {
		t.Error("registry says an unregistered name can render a model")
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		if !reg.CanRenderModel(name) {
			t.Errorf("registry says %s cannot render a model", name)
		}
	}
}

// Today's commands, pinned: with no model declared nothing about a launch
// changes. Green at 06e94526, and it stays green through the build.
func TestPrepareWithoutModelIsUnchanged(t *testing.T) {
	t.Parallel()
	const (
		interactive = api.RuntimeModeInteractive
		headless    = api.RuntimeModeHeadless
	)
	desc := "'--append-system-prompt' 'You are squad-worker-g1-0 (role: worker, team: squad, workspace: acme).'"
	cases := []struct {
		name string
		mode api.RuntimeMode
		want string
	}{
		{"claude", interactive, "claude '--permission-mode' 'plan' " + desc + " '--setting-sources' 'user,project,local' 'do it'"},
		{"claude", headless, "claude '--print' '--output-format' 'stream-json' '--verbose' '--permission-mode' 'plan' " + desc + " '--setting-sources' 'user,project,local' 'do it'"},
		{"codex", interactive, "codex"},
		{"codex", headless, "codex 'exec' '--json' '--skip-git-repo-check' 'do it' < '/dev/null'"},
		{"opencode", interactive, "opencode"},
		{"opencode", headless, "opencode 'run' '--format' 'json' 'do it' < '/dev/null'"},
		{"generic", interactive, "generic"},
		{"simulator", interactive, "simulator '--name' 'squad-worker-g1-0' '--workspace' 'acme' '--team' 'squad' '--role' 'worker' '--socket' '/tmp/marvel.sock'"},
		{"forestage", interactive, "forestage '--persona' 'naomi-nagata' '--identity' 'systems researcher' '--role' 'worker' '--name' 'squad-worker-g1-0' '--workspace' 'acme' '--team' 'squad' '--socket' '/tmp/marvel.sock' '--permission-mode' 'plan' '--' " + desc},
	}
	for _, c := range cases {
		t.Run(c.name+"/"+modeLabel(c.mode), func(t *testing.T) {
			t.Parallel()
			a := NewRegistry().Resolve(c.name)
			if got := prepared(t, a, modelCtx(c.name, c.mode, "")); got != c.want {
				t.Errorf("command changed:\n got  %s\n want %s", got, c.want)
			}
		})
	}
}

func modeLabel(m api.RuntimeMode) string {
	if m == api.RuntimeModeHeadless {
		return "headless"
	}
	return "interactive"
}
