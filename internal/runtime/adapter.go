// Package runtime provides adapter interfaces and implementations for
// launching BYOA agent sessions. Each adapter knows how to construct the
// execution environment (command, args, env vars) for a specific runtime
// (forestage, bare claude CLI, or any generic command).
package runtime

import (
	"fmt"
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
}

// LaunchResult is what the adapter returns: the fully resolved command,
// arguments, and environment variables ready for the tmux driver.
type LaunchResult struct {
	Command string
	Env     map[string]string
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
// shell metacharacters. Empty strings become ''.
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

// resolveCommand returns the command to execute. If Runtime.Command is set,
// use it. Otherwise fall back to the runtime name (image) as the binary name.
func resolveCommand(rt *api.Runtime) string {
	if rt.Command != "" {
		return rt.Command
	}
	return rt.Name
}

// ErrNoCommand is returned when a runtime has no command or image specified.
var ErrNoCommand = fmt.Errorf("runtime has no command or image")
