package team

import (
	"slices"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// TeamLoad is the per-team load rows the last evaluation tick computed, a copy
// the caller may keep. It is empty before the first tick.
func (c *Controller) TeamLoad() []api.TeamLoad {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	out := make([]api.TeamLoad, len(c.load))
	for i, r := range c.load {
		r.Agents = slices.Clone(r.Agents)
		out[i] = r
	}
	return out
}

// refreshTeamLoad computes one row per team from the sessions as the
// evaluation tick left them, with each session's tick-ring reading stamped the
// way `get sessions` stamps it. Local only: nothing here leaves the daemon.
func (c *Controller) refreshTeamLoad(now time.Time) {
	sessions := c.store.ListSessions()
	for i := range sessions {
		sessions[i].ActiveTicks = c.ActivityOf(sessions[i], now)
	}
	teams := c.store.ListTeams()
	rows := make([]api.TeamLoad, 0, len(teams))
	for _, t := range teams {
		rows = append(rows, api.RollTeamLoad(t, sessions, now))
	}
	c.loadMu.Lock()
	c.load = rows
	c.loadMu.Unlock()
}
