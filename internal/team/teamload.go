package team

import "github.com/arcavenae/marvel/internal/api"

// TeamLoad is the per-team load rows the last evaluation tick computed, a copy
// the caller may keep. It is empty before the first tick.
func (c *Controller) TeamLoad() []api.TeamLoad {
	return nil
}
