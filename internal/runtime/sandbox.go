package runtime

import (
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// SandboxRecommender is implemented by an adapter whose harness recommends
// running in a sandbox, the way ComposerCapable marks a harness with a
// measured composer. It is step two of the placement resolution order
// (docs/design/workspace-sandbox-shape.md, section 5): a workspace that names
// no placement takes the harness's recommendation, and every other harness
// starts on the host. A harness without the capability recommends nothing.
type SandboxRecommender interface {
	// RecommendsSandbox reports whether this harness defaults to a container.
	RecommendsSandbox() bool
}

// sandboxRecommendedHarnesses declares the harnesses that recommend a sandbox
// and have no adapter of their own to say so: pi has none yet, and
// mini-swe-agent runs under generic. A harness that gets an adapter declares it
// there, by implementing SandboxRecommender, and its entry here is deleted. An
// adapter that implements SandboxRecommender beats this table, in either
// direction; an adapter that does not leaves the table in force.
var sandboxRecommendedHarnesses = map[string]bool{
	"pi":             true,
	"mini-swe-agent": true,
}

// RecommendedPlacement is the harness's recommendation for the placement
// resolution order: api.PlacementContainer where it recommends a sandbox, and
// the empty value where it recommends nothing. It never returns localhost, so a
// recommendation cannot override the operator's explicit word or stand in for
// the default. runtimeName is the session's Runtime.Name, the key Registry.Resolve
// takes, and not the command's basename: a harness that runs under generic needs
// a Runtime.Name equal to its table entry. The harness is the program, so a path
// or a stray space does not hide it, the way ComposerFor finds it.
func (r *Registry) RecommendedPlacement(runtimeName string) api.Placement {
	name := filepath.Base(strings.TrimSpace(runtimeName))
	r.mu.RLock()
	a, registered := r.adapters[name]
	r.mu.RUnlock()
	if registered {
		if s, ok := a.(SandboxRecommender); ok {
			if s.RecommendsSandbox() {
				return api.PlacementContainer
			}
			return ""
		}
	}
	if sandboxRecommendedHarnesses[name] {
		return api.PlacementContainer
	}
	return ""
}
