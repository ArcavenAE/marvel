package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func testContext() *LaunchContext {
	return &LaunchContext{
		Session: &api.Session{
			Name:      "squad-worker-g1-0",
			Workspace: "acme",
			Team:      "squad",
			Role:      "worker",
			Runtime: api.Runtime{
				Name:    "forestage",
				Command: "/usr/local/bin/forestage",
				Args:    []string{"--model", "sonnet"},
			},
		},
		Role: &api.Role{
			Name:        "worker",
			Replicas:    3,
			Permissions: "plan",
			Persona:     "naomi-nagata",
			Identity:    "systems researcher",
			Runtime: api.Runtime{
				Name:    "forestage",
				Command: "/usr/local/bin/forestage",
				Args:    []string{"--model", "sonnet"},
			},
		},
		Team: &api.Team{
			Name:      "squad",
			Workspace: "acme",
		},
		Workspace: &api.Workspace{
			Name: "acme",
		},
		SocketPath: "/tmp/marvel.sock",
	}
}

func TestRegistryResolve(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	tests := []struct {
		name     string
		expected string
	}{
		{"forestage", "forestage"},
		{"claude", "claude"},
		{"generic", "generic"},
		{"unknown-binary", "generic"},
		{"python3", "generic"},
	}

	for _, tt := range tests {
		a := r.Resolve(tt.name)
		if a.Name() != tt.expected {
			t.Errorf("Resolve(%q) = %q, want %q", tt.name, a.Name(), tt.expected)
		}
	}
}

func TestForestagePrepare(t *testing.T) {
	t.Parallel()
	f := &Forestage{}
	ctx := testContext()

	result, err := f.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Command should contain the binary
	if !strings.HasPrefix(result.Command, "/usr/local/bin/forestage") {
		t.Errorf("command should start with binary, got: %s", result.Command)
	}

	// Should inject persona and identity (finding-019 taxonomy)
	if !strings.Contains(result.Command, "--persona naomi-nagata") {
		t.Errorf("command should contain --persona, got: %s", result.Command)
	}
	if !strings.Contains(result.Command, "--identity") || !strings.Contains(result.Command, "systems researcher") {
		t.Errorf("command should contain --identity with value, got: %s", result.Command)
	}
	// Role is now job assignment, not character lookup
	if !strings.Contains(result.Command, "--role worker") {
		t.Errorf("command should contain --role, got: %s", result.Command)
	}
	// Marvel identity flags
	if !strings.Contains(result.Command, "--name squad-worker-g1-0") {
		t.Errorf("command should contain --name, got: %s", result.Command)
	}
	if !strings.Contains(result.Command, "--workspace acme") {
		t.Errorf("command should contain --workspace, got: %s", result.Command)
	}
	if !strings.Contains(result.Command, "--team squad") {
		t.Errorf("command should contain --team, got: %s", result.Command)
	}
	if !strings.Contains(result.Command, "--socket /tmp/marvel.sock") {
		t.Errorf("command should contain --socket, got: %s", result.Command)
	}

	// Permission mode is a forestage flag (forestage passes it to claude)
	if !strings.Contains(result.Command, "--permission-mode plan") {
		t.Errorf("command should contain --permission-mode, got: %s", result.Command)
	}

	// Identity system prompt goes after "--" as claude passthrough
	if !strings.Contains(result.Command, "-- --append-system-prompt") {
		t.Errorf("command should pass --append-system-prompt after --, got: %s", result.Command)
	}

	// Should preserve original args
	if !strings.Contains(result.Command, "--model sonnet") {
		t.Errorf("command should contain original args, got: %s", result.Command)
	}

	// Env should have marvel identity
	if result.Env["MARVEL_SESSION"] != "squad-worker-g1-0" {
		t.Errorf("MARVEL_SESSION = %q, want %q", result.Env["MARVEL_SESSION"], "squad-worker-g1-0")
	}
	if result.Env["MARVEL_WORKSPACE"] != "acme" {
		t.Errorf("MARVEL_WORKSPACE = %q, want %q", result.Env["MARVEL_WORKSPACE"], "acme")
	}
}

func TestClaudePrepare(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "claude"
	ctx.Session.Runtime.Command = "claude"

	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Should inject permission mode
	if !strings.Contains(result.Command, "--permission-mode plan") {
		t.Errorf("command should contain --permission-mode, got: %s", result.Command)
	}

	// Should inject system prompt with identity
	if !strings.Contains(result.Command, "--append-system-prompt") {
		t.Errorf("command should contain --append-system-prompt, got: %s", result.Command)
	}

	// Should NOT inject --name/--workspace/--team/--role (those are forestage flags)
	if strings.Contains(result.Command, " --name ") {
		t.Errorf("bare claude should not get --name flag, got: %s", result.Command)
	}
}

func TestClaudePreparePreservesExistingSystemPrompt(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "claude"
	ctx.Session.Runtime.Command = "claude"
	ctx.Session.Runtime.Args = []string{"--append-system-prompt", "custom prompt"}

	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Should contain the user's prompt, not an injected one
	count := strings.Count(result.Command, "--append-system-prompt")
	if count != 1 {
		t.Errorf("expected exactly 1 --append-system-prompt, got %d in: %s", count, result.Command)
	}
}

func TestGenericPrepare(t *testing.T) {
	t.Parallel()
	g := &Generic{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "python3"
	ctx.Session.Runtime.Command = "python3"
	ctx.Session.Runtime.Args = []string{"agent.py"}

	result, err := g.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Should just be command + args, no identity injection
	if result.Command != "python3 agent.py" {
		t.Errorf("command = %q, want %q", result.Command, "python3 agent.py")
	}

	// Env should still have marvel identity
	if result.Env["MARVEL_SESSION"] != "squad-worker-g1-0" {
		t.Errorf("MARVEL_SESSION = %q, want %q", result.Env["MARVEL_SESSION"], "squad-worker-g1-0")
	}
}

func TestNoCommandError(t *testing.T) {
	t.Parallel()
	adapters := []Adapter{&Forestage{}, &Claude{}, &Generic{}}
	ctx := testContext()
	ctx.Session.Runtime.Command = ""
	ctx.Session.Runtime.Name = ""

	for _, a := range adapters {
		_, err := a.Prepare(ctx)
		if err == nil {
			t.Errorf("%s.Prepare should fail with no command", a.Name())
		}
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"has space", "'has space'"},
		{"has'quote", `'has'\''quote'`},
		{"$var", "'$var'"},
		{"", "''"},
		{"no-special-chars", "no-special-chars"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestForestagePrepareWithScript(t *testing.T) {
	t.Parallel()
	f := &Forestage{}
	ctx := testContext()
	ctx.Session.Runtime.Script = "review-code.lua"

	result, err := f.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if !strings.Contains(result.Command, "--script review-code.lua") {
		t.Errorf("command should contain --script, got: %s", result.Command)
	}
}

func TestForestagePrepareNoPermissions(t *testing.T) {
	t.Parallel()
	f := &Forestage{}
	ctx := testContext()
	ctx.Role.Permissions = ""

	result, err := f.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if strings.Contains(result.Command, "--permission-mode") {
		t.Errorf("command should not contain --permission-mode when unset, got: %s", result.Command)
	}

	// Should still have identity system prompt
	if !strings.Contains(result.Command, "--append-system-prompt") {
		t.Errorf("command should still inject identity prompt, got: %s", result.Command)
	}
}

func TestForestagePrepareDangerousPermissions(t *testing.T) {
	t.Parallel()
	f := &Forestage{}
	ctx := testContext()
	ctx.Role.DangerousPermissions = true

	result, err := f.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if !strings.Contains(result.Command, "--dangerously-skip-permissions") {
		t.Errorf("command should contain --dangerously-skip-permissions when Role.DangerousPermissions=true, got: %s", result.Command)
	}
}

func TestForestagePrepareDangerousPermissionsDefaultOff(t *testing.T) {
	t.Parallel()
	f := &Forestage{}
	ctx := testContext()
	// Role.DangerousPermissions not set — default false

	result, err := f.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if strings.Contains(result.Command, "--dangerously-skip-permissions") {
		t.Errorf("command must NOT contain --dangerously-skip-permissions when Role.DangerousPermissions is false (default), got: %s", result.Command)
	}
}

// headlessClaudeContext is the shape the manager builds for a
// stream-capable launch: a headless claude runtime plus the sink path.
func headlessClaudeContext(streamPath string) *LaunchContext {
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{
		Name:    "claude",
		Command: "claude",
		Mode:    api.RuntimeModeHeadless,
		Prompt:  "summarize the diff",
	}
	ctx.StreamPath = streamPath
	return ctx
}

func TestClaudeSupportsStreamOnlyWhenHeadless(t *testing.T) {
	t.Parallel()
	c := &Claude{}

	interactive := testContext()
	interactive.Session.Runtime.Name = "claude"
	interactive.Session.Runtime.Command = "claude"
	if c.SupportsStream(interactive) {
		t.Error("interactive claude should not claim a stream")
	}
	if !c.SupportsStream(headlessClaudeContext("")) {
		t.Error("headless claude should claim a stream")
	}
}

func TestClaudePrepareHeadlessRedirectsToSink(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	result, err := c.Prepare(headlessClaudeContext("/tmp/marvel-streams/acme-agent.ndjson"))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	for _, want := range []string{
		"--print", "--output-format stream-json", "--verbose",
		"> /tmp/marvel-streams/acme-agent.ndjson",
	} {
		if !strings.Contains(result.Command, want) {
			t.Errorf("command missing %q: %s", want, result.Command)
		}
	}
	// The prompt is positional and must be the last thing before the
	// redirection, or claude reads it as a flag value.
	if !strings.Contains(result.Command, "'summarize the diff' > ") {
		t.Errorf("prompt not positioned before the redirect: %s", result.Command)
	}
	if result.Stream == nil {
		t.Fatal("expected a StreamSpec")
	}
	if result.Stream.Format != StreamFormatClaudeCodeJSON {
		t.Errorf("format = %q, want %q", result.Stream.Format, StreamFormatClaudeCodeJSON)
	}
}

func TestClaudePrepareHeadlessWithoutSinkStillRuns(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	result, err := c.Prepare(headlessClaudeContext(""))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if strings.Contains(result.Command, ">") {
		t.Errorf("no sink offered, so no redirect belongs in: %s", result.Command)
	}
	if result.Stream != nil {
		t.Error("adapter reported a stream it was never given a sink for")
	}
}

func TestClaudePrepareHeadlessRequiresPrompt(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	ctx := headlessClaudeContext("/tmp/x.ndjson")
	ctx.Session.Runtime.Prompt = ""
	if _, err := c.Prepare(ctx); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("error = %v, want ErrNoPrompt", err)
	}
}

func TestInteractiveAdaptersReportNoStream(t *testing.T) {
	t.Parallel()
	// Every pre-existing adapter must keep launching exactly as before,
	// sink offered or not.
	for _, a := range []Adapter{&Forestage{}, &Generic{}, &Claude{}} {
		ctx := testContext()
		ctx.StreamPath = "/tmp/should-be-ignored.ndjson"
		ctx.Session.Runtime.Name = a.Name()
		ctx.Session.Runtime.Command = "/usr/local/bin/" + a.Name()
		result, err := a.Prepare(ctx)
		if err != nil {
			t.Fatalf("%s Prepare: %v", a.Name(), err)
		}
		if result.Stream != nil {
			t.Errorf("%s reported a stream for an interactive launch", a.Name())
		}
		if strings.Contains(result.Command, "should-be-ignored") {
			t.Errorf("%s redirected an interactive launch: %s", a.Name(), result.Command)
		}
	}
}

func TestNewStreamParserRejectsUnknownFormat(t *testing.T) {
	t.Parallel()
	if _, err := NewStreamParser("codex/json", StreamParserConfig{}); err == nil {
		t.Fatal("expected an error for a format with no parser")
	}
	p, err := NewStreamParser(StreamFormatClaudeCodeJSON, StreamParserConfig{AgentID: "a"})
	if err != nil {
		t.Fatalf("claude-code parser: %v", err)
	}
	if p == nil {
		t.Fatal("expected a parser")
	}
}

// TestAdaptersInjectHeartbeatToken: the heartbeat token is enforcement
// locus 1 in practice: marvel constructs the environment, so what an
// agent can prove about itself is what marvel put there. An adapter that
// forwards MARVEL_SOCKET without the token hands its agent a channel it
// cannot authenticate on, and the daemon refuses every beat.
func TestBaseEnvStampsBeadsActor(t *testing.T) {
	t.Parallel()

	env := baseEnv(testContext())
	want := "marvel/acme/squad-worker-g1-0"
	if got := env["BEADS_ACTOR"]; got != want {
		t.Errorf("BEADS_ACTOR = %q, want %q", got, want)
	}
}

func TestAdaptersStampBeadsActor(t *testing.T) {
	t.Parallel()

	const want = "marvel/acme/squad-worker-g1-0"

	for _, a := range []Adapter{&Forestage{}, &Claude{}, &Codex{}, &OpenCode{}, &Simulator{}, &Generic{}} {
		t.Run(a.Name(), func(t *testing.T) {
			t.Parallel()

			ctx := testContext()
			ctx.Session.Runtime.Name = a.Name()
			ctx.Session.Runtime.Command = "/usr/local/bin/" + a.Name()

			result, err := a.Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := result.Env["BEADS_ACTOR"]; got != want {
				t.Errorf("BEADS_ACTOR = %q, want %q", got, want)
			}
		})
	}
}

func TestBaseEnvBeadsActorOverrideSeam(t *testing.T) {
	t.Parallel()

	// The returned map is the override seam: an adapter, or a future
	// manifest env surface, that writes its own value after baseEnv
	// returns must win over the composed default.
	env := baseEnv(testContext())
	env["BEADS_ACTOR"] = "operator/explicit"
	if got := env["BEADS_ACTOR"]; got != "operator/explicit" {
		t.Errorf("override lost: %q", got)
	}
}

func TestAdaptersInjectHeartbeatToken(t *testing.T) {
	t.Parallel()

	const token = "3f1c0d5e" // shape does not matter here, presence does

	for _, a := range []Adapter{&Forestage{}, &Claude{}, &Codex{}, &OpenCode{}, &Simulator{}, &Generic{}} {
		t.Run(a.Name(), func(t *testing.T) {
			t.Parallel()

			ctx := testContext()
			ctx.Session.Runtime.Name = a.Name()
			ctx.Session.Runtime.Command = "/usr/local/bin/" + a.Name()
			ctx.Session.HeartbeatToken = token

			result, err := a.Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := result.Env[api.HeartbeatTokenEnv]; got != token {
				t.Errorf("%s = %q, want %q", api.HeartbeatTokenEnv, got, token)
			}
			// argv is world-readable from the process table; the token
			// must not be there for every agent on the host to copy.
			if strings.Contains(result.Command, token) {
				t.Errorf("token appears in argv: %s", result.Command)
			}

			// No socket, no heartbeat channel, so no secret in the pane.
			ctx.SocketPath = ""
			result, err = a.Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare without socket: %v", err)
			}
			if got, ok := result.Env[api.HeartbeatTokenEnv]; ok {
				t.Errorf("%s = %q on a session with no socket, want absent", api.HeartbeatTokenEnv, got)
			}
		})
	}
}
