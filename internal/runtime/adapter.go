// Package runtime provides adapter interfaces and implementations for
// launching BYOA agent sessions. Each adapter knows how to construct the
// execution environment (command, args, env vars) for a specific runtime
// (forestage, bare claude CLI, or any generic command).
//
// A constructed environment can leak. Measured on Crush v0.88.1
// (finding-020): its server serves the client's entire process
// environment on GET /v1/workspaces, 76 entries including a database
// password, to any client of a host-shared socket. Environment
// construction is marvel's one built enforcement locus, so a harness that
// republishes it turns the enforcement surface into a disclosure surface.
// Adapters put identity, paths and flags in the environment; secrets
// belong somewhere a harness cannot serialize.
//
// No Crush adapter exists yet. When one is written it needs
// CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1 unconditionally, because without
// it every spawned session rewrites the host-global provider cache, and
// CRUSH_GLOBAL_DATA does not contain that write. Relocating
// CRUSH_GLOBAL_DATA is safe (caches and model selections);
// CRUSH_GLOBAL_CONFIG is the operator's credential store and is not.
//
// It also needs a stated position on crushrc. Crush executes a project
// directory's .crushrc as bash at config load, ahead of any permission
// list, and marvel workspaces are checkouts of arbitrary repositories.
// Measured firing on `crush run` with no trust prompt (finding-020 §9).
package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/arcavenae/marvel/internal/api"
)

// LaunchContext holds the information an adapter needs to construct the
// execution environment for a session.
type LaunchContext struct {
	Session    *api.Session
	Role       *api.Role
	Team       *api.Team
	Workspace  *api.Workspace
	SocketPath string
	// StreamPath is a sink the session manager created for this launch —
	// a FIFO the harness's structured output can be redirected into.
	// Empty means marvel is not observing this session's stream, either
	// because the adapter declined SupportsStream or because the manager
	// has no sink directory. An adapter that finds it empty must produce
	// a working command anyway.
	StreamPath string
	// PolicyProjectionPath is the file the session manager wrote this
	// session's projected Claude Code settings fragment to (see
	// finding-024). It is non-empty only when the role references a policy
	// AND the adapter reported ProjectionFor().Supported. Prepare appends
	// the adapter's own read-flag pointing at it (claude: a top-level
	// --settings; forestage: --settings in the claude passthrough). Empty
	// means no projection for this launch and Prepare injects nothing.
	PolicyProjectionPath string
}

// ProjectionTarget is an adapter's answer to "where does this session's
// projected policy settings file go, and can this harness read one?".
// The session manager calls ProjectionFor at spawn and on every live
// re-projection; it writes Settings JSON to Path only when Supported.
type ProjectionTarget struct {
	// Supported reports whether this harness reads a settings fragment
	// from a path marvel chooses. False for harnesses that do not (codex,
	// opencode, generic today); marvel then logs the referenced policy as
	// advisory for that runtime rather than dropping it silently.
	//
	// The bar is deliberately "accepts a settings path as a launch
	// argument", not "has a settings file". A harness with a config file
	// but no way to be handed one can only be configured by writing into
	// the user's own config directory, where the blast radius is their
	// interactive use of that tool in every project and it outlives
	// marvel's uninstall. Returning false and logging the policy as
	// advisory is the correct answer for that harness.
	Supported bool
	// Path is the file marvel writes the resolved settings fragment to.
	// Set only when Supported, and it must be inside the dir passed to
	// ProjectionFor; the session manager refuses a path outside it.
	Path string
}

// StatuslineFeeder is the optional half of the projection contract. An
// adapter implements it when its harness can express marvel's context
// feed as keys in its own settings schema, and the session manager asks
// the adapter for those keys rather than assuming any one harness's.
//
// Splitting it from Adapter keeps two things apart that a single
// Supported flag ran together: whether a harness reads a projected
// settings file at all, and whether it has somewhere to put a CTX% hook.
// A harness can do the first without the second, and an adapter that does
// not implement this gets no feed keys rather than another harness's.
type StatuslineFeeder interface {
	// StatuslineFeed renders the context feed in this harness's own
	// settings schema. command is the full shell command the hook must
	// run. The manager merges the returned keys into the projected
	// settings only where the policy has not already defined them, so a
	// policy always wins over the feed.
	StatuslineFeed(command string) map[string]any
}

// LaunchResult is what the adapter returns: the fully resolved command,
// arguments, and environment variables ready for the tmux driver.
type LaunchResult struct {
	Command string
	Env     map[string]string
	// Stream is set only when the adapter actually wired its harness's
	// structured output into LaunchContext.StreamPath. Nil means the
	// session produces no parseable stream and is observed by
	// capture-pane alone.
	Stream *StreamSpec
}

// Adapter knows how to prepare the execution environment for a specific
// runtime type. Adapters are stateless — all session-specific state is
// in the LaunchContext.
type Adapter interface {
	// Name returns the adapter identifier (e.g., "forestage", "claude", "generic").
	Name() string

	// Prepare constructs the command, args, and environment for launching
	// a session. The returned command string is passed to tmux new-window.
	Prepare(ctx *LaunchContext) (*LaunchResult, error)

	// ProjectionFor reports where marvel writes this session's projected
	// Claude Code settings fragment and whether the harness can read one.
	// dir is the manager-owned directory projected files live under. The
	// session manager calls this at spawn (before Prepare) and on every
	// live re-projection, so the answer must depend only on the runtime
	// and the launch identity, never on prior Prepare state. A harness
	// with no settings surface returns Supported=false.
	ProjectionFor(ctx *LaunchContext, dir string) ProjectionTarget
}

// Registry maps runtime names to adapters.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
	fallback Adapter
}

// NewRegistry creates a registry with the standard adapters pre-registered.
func NewRegistry() *Registry {
	r := &Registry{
		adapters: make(map[string]Adapter),
		fallback: &Generic{},
	}
	r.Register(&Forestage{})
	r.Register(&Claude{})
	r.Register(&Codex{})
	r.Register(&OpenCode{})
	r.Register(&Simulator{})
	r.Register(&Generic{})
	return r
}

// Register adds an adapter to the registry.
func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Name()] = a
}

// Resolve finds the adapter for a runtime name. Falls back to the generic
// adapter for unknown runtimes — marvel manages any process in a tmux pane.
func (r *Registry) Resolve(runtimeName string) Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a, ok := r.adapters[runtimeName]; ok {
		return a
	}
	return r.fallback
}

// baseEnv returns the environment variables common to all adapters.
func baseEnv(ctx *LaunchContext) map[string]string {
	env := map[string]string{
		"MARVEL_SESSION":   ctx.Session.Name,
		"MARVEL_ROLE":      ctx.Role.Name,
		"MARVEL_TEAM":      ctx.Team.Name,
		"MARVEL_WORKSPACE": ctx.Workspace.Name,
	}
	if ctx.SocketPath != "" {
		env["MARVEL_SOCKET"] = ctx.SocketPath
		// The socket is what makes a heartbeat possible, so the token
		// travels with it and a session that cannot reach the daemon
		// carries no secret. This is enforcement locus 1: marvel
		// constructs the environment, and what the agent can prove about
		// itself is what marvel put there.
		if ctx.Session.HeartbeatToken != "" {
			env[api.HeartbeatTokenEnv] = ctx.Session.HeartbeatToken
		}
	}
	return env
}

// buildCommand joins a binary and its args into the single command string
// that tmux new-window expects.
func buildCommand(binary string, args []string) string {
	cmd := binary
	for _, arg := range args {
		cmd += " " + shellQuote(arg)
	}
	return cmd
}

// shellQuote wraps an argument in single quotes if it contains spaces or
// shell metacharacters. Empty strings become ”.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if c == ' ' || c == '\'' || c == '"' || c == '\\' || c == '$' ||
			c == '`' || c == '!' || c == '&' || c == '|' || c == ';' ||
			c == '(' || c == ')' || c == '{' || c == '}' || c == '<' ||
			c == '>' || c == '*' || c == '?' || c == '[' || c == ']' ||
			c == '#' || c == '~' {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	// Single-quote the string, escaping embedded single quotes.
	result := "'"
	for _, c := range s {
		if c == '\'' {
			result += `'\''`
		} else {
			result += string(c)
		}
	}
	result += "'"
	return result
}

// settingsProjectionPath is the per-session projected settings file for
// adapters that read a Claude Code settings fragment (claude, forestage).
// One shape, shared, so the two supporting adapters agree on where the
// manager writes and re-writes the file. Session keys contain a slash
// (workspace/name); it is folded to a dash so the key is one path segment.
func settingsProjectionPath(dir, sessionKey string) string {
	name := strings.ReplaceAll(sessionKey, "/", "-") + ".settings.json"
	return filepath.Join(dir, name)
}

// resolveCommand returns the command to execute. If Runtime.Command is set,
// use it. Otherwise fall back to the runtime name (image) as the binary name.
func resolveCommand(rt *api.Runtime) string {
	if rt.Command != "" {
		return rt.Command
	}
	return rt.Name
}

// redirectStdout appends a stdout redirection to a command string. Safe
// because tmux runs a single-argument shell-command through `sh -c`,
// which is the same property buildCommand's quoting already relies on.
// stderr deliberately stays in the pane: it is the operator's window
// into a harness that failed before it produced any structured output.
func redirectStdout(cmd, path string) string {
	return cmd + " > " + shellQuote(path)
}

// redirectStdin appends a stdin redirection to a command string. A harness
// launched in a tmux pane inherits the pane's tty as stdin; a harness that
// reads stdin (codex exec appends piped stdin to its prompt, opencode run
// likewise) blocks forever on a tty nobody types into. Redirecting from
// /dev/null closes that mouth. Applied before redirectStdout so the two
// compose as `cmd < /dev/null > sink`.
func redirectStdin(cmd, path string) string {
	return cmd + " < " + shellQuote(path)
}

// ErrNoCommand is returned when a runtime has no command or image specified.
var ErrNoCommand = fmt.Errorf("runtime has no command or image")

// ErrNoPrompt is returned when a headless launch carries no prompt. A
// prompt-less headless harness reads stdin and hangs on a pane tty
// nobody is typing into, so this is a manifest error, not a default.
var ErrNoPrompt = fmt.Errorf("headless runtime has no prompt")
