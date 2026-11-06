package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/arcavenae/marvel/internal/api"
)

// handoff_via = "tool" (docs/design/read-only-seat-handoff.md section 6): only
// a harness that can be given the submit_handoff tool may be asked to use it.

// codex has a seeder that renders the marvel-handoff entry. opencode reaches
// an MCP server through its role's environment (section 2), so the entry needs
// no seeder. Every other harness has no way to be given the tool: crush has no
// seeder yet and resolves to the generic adapter, and claude, goose and
// forestage are not seeded with it.
func TestOnlyHarnessesThatCanBeGivenTheHandoffToolSayYes(t *testing.T) {
	reg := NewRegistry()
	for name, want := range map[string]bool{
		"codex":     true,
		"opencode":  true,
		"crush":     false,
		"claude":    false,
		"goose":     false,
		"forestage": false,
		"generic":   false,
		"unknown":   false,
	} {
		o, ok := reg.Resolve(name).(HandoffToolOfferer)
		got := ok && o.OffersHandoffTool()
		if got != want {
			t.Errorf("%s offers the handoff tool = %v, want %v", name, got, want)
		}
	}
}

// seededServers runs the codex seeder for ctx against a scratch home with no
// codex binary on PATH, which leaves the config written and the trust step
// unrecorded, and returns the mcp_servers table it wrote.
func seededServers(t *testing.T, ctx *LaunchContext) map[string]any {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	seed := codexSeeder(ctx, t.TempDir())
	if seed == nil {
		t.Fatal("no seeder for the launch")
	}
	dir := t.TempDir()
	_ = seed(dir) // the missing codex binary is reported after the file is written
	body, err := os.ReadFile(filepath.Join(dir, codexConfigFile))
	if err != nil {
		t.Fatalf("seeded config: %v", err)
	}
	var doc map[string]any
	if _, err := toml.Decode(string(body), &doc); err != nil {
		t.Fatalf("seeded config does not parse: %v\n%s", err, body)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	return servers
}

func codexViaCtx(via string) *LaunchContext {
	ctx := codexCtx(api.RuntimeModeInteractive, "")
	ctx.Role.Shift = &api.ShiftPolicy{
		Any:           []api.ShiftCondition{{On: api.ShiftTriggerMaxAge, MaxAge: 72 * 3600e9}},
		Handoff:       "/tmp/h/{session}.md",
		HandoffMarker: "END",
		HandoffVia:    via,
	}
	ctx.Workspace = &api.Workspace{Name: "acme"}
	return ctx
}

func TestCodexSeedsTheHandoffServerWhenTheRoleAsksForTheTool(t *testing.T) {
	servers := seededServers(t, codexViaCtx(api.ShiftHandoffViaTool))
	h, ok := servers[codexHandoffServer].(map[string]any)
	if !ok {
		t.Fatalf("no %s entry in %v", codexHandoffServer, servers)
	}
	if h["default_tools_approval_mode"] != "approve" {
		t.Errorf("approval = %v, want approve", h["default_tools_approval_mode"])
	}
	cmd, _ := h["command"].(string)
	if !filepath.IsAbs(cmd) {
		t.Errorf("command = %q, want the absolute path of the marvel binary", cmd)
	}
	env, _ := h["env_vars"].([]any)
	if len(env) != 2 || env[0] != "MARVEL_SOCKET" || env[1] != api.HeartbeatTokenEnv {
		t.Errorf("env_vars = %v, want the socket and the token variable", env)
	}
}

func TestCodexSeedsNoHandoffServerWhenTheRoleDoesNotAsk(t *testing.T) {
	servers := seededServers(t, codexViaCtx(""))
	if _, ok := servers[codexHandoffServer]; ok {
		t.Errorf("%s entry seeded for a role that did not ask: %v", codexHandoffServer, servers)
	}
}

// A launch that delivers the token by file names the file's variable instead.
func TestCodexHandoffServerNamesTheTokenFileWhenTheLaunchUsesOne(t *testing.T) {
	ctx := codexViaCtx(api.ShiftHandoffViaTool)
	ctx.TokenFile = "/tmp/tok"
	h, _ := seededServers(t, ctx)[codexHandoffServer].(map[string]any)
	env, _ := h["env_vars"].([]any)
	if len(env) != 2 || env[1] != api.HeartbeatTokenFileEnv {
		t.Errorf("env_vars = %v, want the token file variable", env)
	}
}
