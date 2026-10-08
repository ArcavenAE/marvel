package api

import (
	"maps"
	"slices"
)

// Spec states a session can carry against its role's current runtime
// (docs/design/drift-view.md section 2). Empty means there is no role to
// compare against.
const (
	SpecCurrent = "current"
	SpecBehind  = "behind"
)

// RuntimeDrift names the Runtime fields that differ between a session's
// stored runtime and its role's current one, in the Runtime's field order.
// It returns names and never values. A nil and an empty slice or map are
// equal, and Args compares in order.
func RuntimeDrift(session, role Runtime) []string {
	var out []string
	add := func(differs bool, name string) {
		if differs {
			out = append(out, name)
		}
	}
	add(session.Name != role.Name, "name")
	add(session.Command != role.Command, "command")
	add(!slices.Equal(session.Args, role.Args), "args")
	add(session.Script != role.Script, "script")
	add(session.Mode != role.Mode, "mode")
	add(session.Prompt != role.Prompt, "prompt")
	add(session.ContextWindow != role.ContextWindow, "context_window")
	add(session.ContextFeed != role.ContextFeed, "context_feed")
	add(!maps.Equal(session.Env, role.Env), "env")
	add(session.Backend != role.Backend, "backend")
	return out
}
