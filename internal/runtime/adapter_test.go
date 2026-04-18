package runtime

import (
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
