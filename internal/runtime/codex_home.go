package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

// codexConfigFile is the config file codex reads from its home.
const codexConfigFile = "config.toml"

// codexHookEvents are the hook events that run `marvel codex-ctx`, under
// codex's config spelling. SessionStart gives a reading before the first
// turn, Stop one per turn, and PostToolUse keeps it moving inside a long
// turn. codex-ctx prints nothing, so a fire adds nothing to the context it
// measures (cmd/marvel/codexctx.go).
var codexHookEvents = []string{"SessionStart", "Stop", "PostToolUse"}

// codexDirectorServer is the name of the director MCP server entry.
const codexDirectorServer = "director"

// codexTrustTimeout bounds the one codex call a seed makes. Measured at
// about 0.1s on codex-cli 0.157.0; the bound is for a codex that hangs.
const codexTrustTimeout = 10 * time.Second

// codexMCPServer is the part of an MCP server entry marvel carries from the
// operator's config: how to start it, and nothing about who it runs as.
// Only these two fields are declared, so the operator's env table (their
// own director identity) is dropped by the decoder and never held.
type codexMCPServer struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// codexSeed is everything a seeded config.toml says. Every key marvel
// writes comes from a field here; nothing is copied from the operator's
// file wholesale.
type codexSeed struct {
	// HookCommand runs codex-ctx, with the daemon's own binary path so the
	// pane needs no PATH assumption.
	HookCommand string
	// Director is how to start the director MCP server, or nil when the
	// operator has none configured.
	Director *codexMCPServer
	// EnvVars names the variables codex forwards to the director server.
	// Codex does not pass its environment to an MCP child, so without
	// this the server starts with no identity and no broker.
	EnvVars []string
	// Untrusted is the directory the session starts in, declared not
	// trusted (finding-049): a trusted project lets `-s read-only` write
	// it anyway.
	Untrusted string
	// Trusted maps a hook key codex reports to the hash it reports for it.
	Trusted map[string]string
}

// renderCodexConfig renders a seed as config.toml. Pure, so the allowlist
// is testable without codex.
func renderCodexConfig(s codexSeed) ([]byte, error) {
	doc := map[string]any{}

	hooks := map[string]any{}
	for _, event := range codexHookEvents {
		hooks[event] = []map[string]any{{
			"hooks": []map[string]any{{
				"type":    "command",
				"command": s.HookCommand,
			}},
		}}
	}
	if len(s.Trusted) > 0 {
		state := map[string]any{}
		for key, hash := range s.Trusted {
			state[key] = map[string]any{"trusted_hash": hash}
		}
		hooks["state"] = state
	}
	doc["hooks"] = hooks

	if s.Director != nil && s.Director.Command != "" {
		server := map[string]any{
			"command": s.Director.Command,
			// Unattended roles have no one to answer an approval prompt,
			// and a prompt nobody answers is a tool call that never runs.
			"default_tools_approval_mode": "approve",
		}
		if len(s.Director.Args) > 0 {
			server["args"] = s.Director.Args
		}
		if len(s.EnvVars) > 0 {
			server["env_vars"] = s.EnvVars
		}
		doc["mcp_servers"] = map[string]any{codexDirectorServer: server}
	}

	if s.Untrusted != "" {
		doc["projects"] = map[string]any{
			s.Untrusted: map[string]any{"trust_level": "untrusted"},
		}
	}

	var buf bytes.Buffer
	buf.WriteString("# Written by marvel for one session. Rewritten at every launch; edit the\n" +
		"# role or the operator's own ~/.codex/config.toml instead (marvel#308).\n\n")
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return nil, fmt.Errorf("encode codex config: %w", err)
	}
	return buf.Bytes(), nil
}

// operatorDirector reads how the operator starts the director MCP server
// from their own codex config. Only command and args are read; a missing
// file or entry yields nil, because an operator without director is not an
// error.
func operatorDirector(source string) *codexMCPServer {
	if source == "" {
		return nil
	}
	var cfg struct {
		MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(filepath.Join(source, codexConfigFile), &cfg); err != nil {
		return nil
	}
	server, ok := cfg.MCPServers[codexDirectorServer]
	if !ok || server.Command == "" {
		return nil
	}
	return &server
}

// seedCodexHome writes the session's config.toml, then asks codex which of
// its hooks it would skip and records the trust codex itself reports for
// them.
//
// Codex keeps an accepted hook as `[hooks.state."<key>"] trusted_hash`,
// where the key is the resolved config path plus the event and position.
// Marvel does not compute either value: it asks the installed codex over
// app-server `hooks/list`, so the record matches whatever codex is on this
// host. A hook codex reports as untrusted is skipped silently at run time,
// which is why the record is written rather than left to the TUI review
// nobody answers in an unattended seat. Only hooks running marvel's own
// command are trusted.
//
// If the codex call fails, the config stays without trust records and the
// error is returned for the log: the session still runs, and CTX% reads
// `-` as it did before.
func seedCodexHome(dir, codexBin string, seed codexSeed) error {
	path := filepath.Join(dir, codexConfigFile)
	seed.Trusted = nil
	if err := writeSeedFile(path, seed); err != nil {
		return err
	}
	hooks, err := codexListHooks(codexBin, dir)
	if err != nil {
		return fmt.Errorf("hook trust not recorded: %w", err)
	}
	seed.Trusted = map[string]string{}
	for _, h := range hooks {
		if h.Command == seed.HookCommand && h.CurrentHash != "" {
			seed.Trusted[h.Key] = h.CurrentHash
		}
	}
	if len(seed.Trusted) != len(codexHookEvents) {
		return fmt.Errorf("hook trust not recorded: codex listed %d of marvel's %d hooks", len(seed.Trusted), len(codexHookEvents))
	}
	return writeSeedFile(path, seed)
}

// writeSeedFile renders and replaces path, owner-only, through a rename so
// a codex reading the file never sees half of it.
func writeSeedFile(path string, seed codexSeed) error {
	data, err := renderCodexConfig(seed)
	if err != nil {
		return err
	}
	tmp := path + ".marvel-tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// codexHook is one entry of codex's hooks/list answer.
type codexHook struct {
	Key         string `json:"key"`
	Command     string `json:"command"`
	CurrentHash string `json:"currentHash"`
	TrustStatus string `json:"trustStatus"`
}

// codexListHooks runs `codex app-server` against home and returns the
// hooks it reports. The child gets only HOME, PATH and CODEX_HOME: it is
// asked a question about a file, and needs none of the daemon's secrets.
func codexListHooks(codexBin, home string) ([]codexHook, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexTrustTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexBin, "app-server")
	cmd.Env = []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		codexHomeEnv + "=" + home,
	}
	cmd.Dir = home
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("app-server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", codexBin, err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	enc := json.NewEncoder(stdin)
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	await := func(id int) (json.RawMessage, error) {
		for lines.Scan() {
			var msg struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(lines.Bytes(), &msg) != nil || msg.ID == nil || *msg.ID != id {
				continue
			}
			if len(msg.Error) > 0 {
				return nil, fmt.Errorf("app-server request %d: %s", id, msg.Error)
			}
			return msg.Result, nil
		}
		if err := lines.Err(); err != nil {
			return nil, fmt.Errorf("app-server read: %w", err)
		}
		return nil, errors.New("app-server closed before answering")
	}

	if err := enc.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"clientInfo": map[string]string{"name": "marvel", "version": "1"},
	}}); err != nil {
		return nil, fmt.Errorf("app-server initialize: %w", err)
	}
	if _, err := await(1); err != nil {
		return nil, err
	}
	if err := enc.Encode(map[string]any{"method": "initialized"}); err != nil {
		return nil, fmt.Errorf("app-server initialized: %w", err)
	}
	if err := enc.Encode(map[string]any{"id": 2, "method": "hooks/list", "params": map[string]any{
		"cwds": []string{home},
	}}); err != nil {
		return nil, fmt.Errorf("app-server hooks/list: %w", err)
	}
	raw, err := await(2)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []struct {
			Hooks []codexHook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode hooks/list: %w", err)
	}
	var hooks []codexHook
	for _, d := range result.Data {
		hooks = append(hooks, d.Hooks...)
	}
	return hooks, nil
}

// codexForwardedEnv names the constructed variables codex should hand the
// director server, sorted so the seeded file is stable across launches.
func codexForwardedEnv(ctx *LaunchContext) []string {
	env := constructedEnv(ctx)
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// codexBinary picks the codex that answers the trust question: the role's
// own command when it is codex, else codex on PATH. A role that runs codex
// through a wrapper still has one codex installed, and it is the one whose
// hash scheme matters.
func codexBinary(command string) (string, error) {
	if filepath.Base(command) == "codex" {
		if p, err := exec.LookPath(command); err == nil {
			return p, nil
		}
	}
	return exec.LookPath("codex")
}

// codexSeeder returns the Seed step for one launch. The inputs are resolved
// here, at spawn, so the file names this daemon's binary and this session's
// constructed env.
func codexSeeder(ctx *LaunchContext, source string) func(dir string) error {
	if ctx == nil || ctx.Session == nil || ctx.Role == nil || ctx.Team == nil || ctx.Workspace == nil {
		return nil
	}
	return func(dir string) error {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve marvel binary for the codex-ctx hook: %w", err)
		}
		// The pane starts where the tmux session did, which is the
		// daemon's working directory until a role can name one
		// (aae-orc-g71ad).
		start, err := os.Getwd()
		if err != nil {
			start = ""
		}
		seed := codexSeed{
			HookCommand: buildCommand(shellQuote(exe), []string{"codex-ctx"}),
			Director:    operatorDirector(source),
			EnvVars:     codexForwardedEnv(ctx),
			Untrusted:   start,
		}
		bin, err := codexBinary(ctx.Role.Runtime.Command)
		if err != nil {
			if werr := writeSeedFile(filepath.Join(dir, codexConfigFile), seed); werr != nil {
				return werr
			}
			return fmt.Errorf("hook trust not recorded, no codex binary found: %w", err)
		}
		return seedCodexHome(dir, bin, seed)
	}
}
