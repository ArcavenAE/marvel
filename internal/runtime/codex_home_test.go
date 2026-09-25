package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/arcavenae/marvel/internal/api"
)

// operatorCodexConfig is an operator's own config.toml carrying every key a
// session must NOT inherit: project trust that would make the target
// writable under -s read-only (finding-049), an approvals reviewer, and the
// operator's own director identity in the server's env.
const operatorCodexConfig = `
approvals_reviewer = "auto_review"
model = "gpt-5"

[projects."/work/target"]
trust_level = "trusted"

[mcp_servers.director]
command = "/opt/director/bin/director-mcp"
args = ["--stdio"]

[mcp_servers.director.env]
DIRECTOR_AGENT_ID = "ops/operator"
`

func writeOperatorConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, codexConfigFile), []byte(operatorCodexConfig), 0o600); err != nil {
		t.Fatalf("write operator config: %v", err)
	}
	return dir
}

// TestRenderCodexConfigIsAnAllowlist: the seeded file carries the hooks,
// the director server with forwarded names and approve mode, and the start
// directory declared untrusted, and nothing else from the operator's file.
func TestRenderCodexConfigIsAnAllowlist(t *testing.T) {
	source := writeOperatorConfig(t)
	seed := codexSeed{
		HookCommand: "/opt/homebrew/bin/marvel codex-ctx",
		Director:    operatorDirector(source),
		EnvVars:     []string{"DIRECTOR_AGENT_ID", "MARVEL_SESSION"},
		Untrusted:   "/work/start",
		Trusted:     map[string]string{"/h/config.toml:session_start:0:0": "sha256:abc"},
	}
	data, err := renderCodexConfig(seed)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var got map[string]any
	if _, err := toml.Decode(string(data), &got); err != nil {
		t.Fatalf("seeded config does not parse: %v\n%s", err, data)
	}
	for _, k := range []string{"approvals_reviewer", "model"} {
		if _, ok := got[k]; ok {
			t.Errorf("seeded config carries the operator's %s", k)
		}
	}

	projects, _ := got["projects"].(map[string]any)
	if _, ok := projects["/work/target"]; ok {
		t.Errorf("seeded config carries the operator's project trust: %v", projects)
	}
	start, _ := projects["/work/start"].(map[string]any)
	if start["trust_level"] != "untrusted" {
		t.Errorf("start directory trust = %v, want declared untrusted", start["trust_level"])
	}

	servers, _ := got["mcp_servers"].(map[string]any)
	director, _ := servers["director"].(map[string]any)
	if director["command"] != "/opt/director/bin/director-mcp" {
		t.Errorf("director command = %v", director["command"])
	}
	if director["default_tools_approval_mode"] != "approve" {
		t.Errorf("director approval mode = %v, want approve", director["default_tools_approval_mode"])
	}
	if _, ok := director["env"]; ok {
		t.Errorf("seeded config carries the operator's director identity: %v", director["env"])
	}
	if strings.Contains(string(data), "ops/operator") {
		t.Errorf("operator's director identity leaked into the seeded file:\n%s", data)
	}
	vars, _ := director["env_vars"].([]any)
	if len(vars) != 2 {
		t.Errorf("env_vars = %v, want the two forwarded names", vars)
	}

	hooks, _ := got["hooks"].(map[string]any)
	for _, event := range codexHookEvents {
		groups, _ := hooks[event].([]map[string]any)
		if len(groups) != 1 {
			t.Errorf("hooks.%s = %v, want one group", event, hooks[event])
			continue
		}
		handlers, _ := groups[0]["hooks"].([]map[string]any)
		if len(handlers) != 1 || handlers[0]["command"] != seed.HookCommand {
			t.Errorf("hooks.%s handler = %v", event, groups[0]["hooks"])
		}
	}
	state, _ := hooks["state"].(map[string]any)
	entry, _ := state["/h/config.toml:session_start:0:0"].(map[string]any)
	if entry["trusted_hash"] != "sha256:abc" {
		t.Errorf("trust record = %v", state)
	}
}

// TestOperatorDirectorAbsent: no file or no director entry yields no server
// rather than an error, and the render omits the block.
func TestOperatorDirectorAbsent(t *testing.T) {
	if got := operatorDirector(t.TempDir()); got != nil {
		t.Errorf("missing config gave %+v, want nil", got)
	}
	if got := operatorDirector(""); got != nil {
		t.Errorf("empty source gave %+v, want nil", got)
	}
	data, err := renderCodexConfig(codexSeed{HookCommand: "marvel codex-ctx"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(data), "mcp_servers") {
		t.Errorf("render with no director wrote a server block:\n%s", data)
	}
}

// TestCodexForwardedEnvNamesConstructedValues: the names codex forwards to
// the director server are the ones marvel constructs, including the four a
// heartbeat needs, and never a role's own additions.
func TestCodexForwardedEnvNamesConstructedValues(t *testing.T) {
	ctx := &LaunchContext{
		Session:    &api.Session{Name: "t-r-g1-0", Workspace: "ws", Team: "t", HeartbeatToken: "tok"},
		Role:       &api.Role{Name: "r", Runtime: api.Runtime{Env: map[string]string{"ROLE_ONLY": "x"}}},
		Team:       &api.Team{Name: "t"},
		Workspace:  &api.Workspace{Name: "ws"},
		SocketPath: "/tmp/marvel.sock",
	}
	names := codexForwardedEnv(ctx)
	for _, want := range []string{"MARVEL_SOCKET", "MARVEL_WORKSPACE", "MARVEL_SESSION", api.HeartbeatTokenEnv, "DIRECTOR_AGENT_ID"} {
		if !slices.Contains(names, want) {
			t.Errorf("forwarded names %v lack %s", names, want)
		}
	}
	if slices.Contains(names, "ROLE_ONLY") {
		t.Errorf("forwarded names %v include a role-declared key", names)
	}
	if !slices.IsSorted(names) {
		t.Errorf("forwarded names %v are not sorted", names)
	}
}

// TestSeedCodexHomeRecordsTrust runs the installed codex: after seeding,
// codex itself reports all three marvel hooks trusted in the private home,
// which is what makes it run them without a TUI review or a bypass flag
// (the pt8k spike, outcome A). Skipped where codex is not installed.
func TestSeedCodexHomeRecordsTrust(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed")
	}
	home := t.TempDir()
	seed := codexSeed{HookCommand: "/usr/local/bin/marvel codex-ctx", Untrusted: t.TempDir()}
	if err := seedCodexHome(home, bin, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, codexConfigFile))
	if err != nil {
		t.Fatalf("stat seeded config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("seeded config mode = %v, want 0600", info.Mode().Perm())
	}

	hooks, err := codexListHooks(bin, home)
	if err != nil {
		t.Fatalf("hooks/list: %v", err)
	}
	trusted := 0
	for _, h := range hooks {
		if h.Command != seed.HookCommand {
			continue
		}
		if h.TrustStatus != "trusted" {
			t.Errorf("hook %s trust = %s, want trusted", h.Key, h.TrustStatus)
			continue
		}
		trusted++
	}
	if trusted != len(codexHookEvents) {
		t.Errorf("codex reports %d marvel hooks trusted, want %d", trusted, len(codexHookEvents))
	}
}

// TestSeedCodexHomeWithoutCodexStillWrites: with no codex to ask, the file
// is still written, without trust records, and the reason is returned.
func TestSeedCodexHomeWithoutCodexStillWrites(t *testing.T) {
	home := t.TempDir()
	err := seedCodexHome(home, filepath.Join(t.TempDir(), "no-codex"), codexSeed{HookCommand: "marvel codex-ctx"})
	if err == nil {
		t.Fatal("seed with no codex binary reported success")
	}
	data, rerr := os.ReadFile(filepath.Join(home, codexConfigFile))
	if rerr != nil {
		t.Fatalf("config not written: %v", rerr)
	}
	if strings.Contains(string(data), "trusted_hash") {
		t.Errorf("config claims trust codex never reported:\n%s", data)
	}
}
