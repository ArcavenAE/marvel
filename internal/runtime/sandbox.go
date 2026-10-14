package runtime

import "github.com/arcavenae/marvel/internal/api"

// Red-first stub: wrong on purpose until the tests fail for the right reason.

// SandboxRecommender is implemented by an adapter whose harness recommends a sandbox.
type SandboxRecommender interface {
	RecommendsSandbox() bool
}

// RecommendedPlacement is the harness's recommendation for the resolution order.
func (r *Registry) RecommendedPlacement(runtimeName string) api.Placement {
	return ""
}
