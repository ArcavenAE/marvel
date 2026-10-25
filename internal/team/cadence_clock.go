package team

import "github.com/arcavenae/marvel/internal/api"

// reconcileCadenceClock is the slot clock for interactive roles' declared
// cadences (docs/design/declared-cadence.md V2). It opens slot records and
// nothing else: it never spawns, types or claims.
func (c *Controller) reconcileCadenceClock(teams []api.Team) {}
