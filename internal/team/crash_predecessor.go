package team

import "github.com/arcavenae/marvel/internal/api"

// crashedPredecessors is the red-phase stub of the crash-repair lookup: the
// gate is not implemented yet, so the tests in crash_predecessor_test.go fail
// on their assertions.
func (c *Controller) crashedPredecessors(t *api.Team, role string, generation int64) []string {
	return nil
}
