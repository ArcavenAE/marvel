package team

import (
	"sort"
	"strconv"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// crashedPredecessors returns the keys of the seats of role, in one
// generation, that crash repair is about to replace, in index order. The spawn
// loop clears a role's Crashed rows just before it builds the replacements, so
// this reads them first; a replacement takes one as its predecessor, so its
// MARVEL_PREDECESSOR names the seat whose handoff, mail and lock it inherits
// (marvel#541, marvel#1402). A key a live seat already names is spent, as a
// wake spends it, so a replacement started over several ticks never claims the
// same seat twice. A Crashed row of another generation or role is left alone.
func (c *Controller) crashedPredecessors(t *api.Team, role string, generation int64) []string {
	rows := c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role)
	named := map[string]bool{}
	for _, s := range rows {
		if s.State.CountsAsAlive() && s.Predecessor != "" {
			named[s.Predecessor] = true
		}
	}
	type crashed struct {
		key   string
		index int
	}
	var found []crashed
	for _, s := range rows {
		if s.State != api.SessionCrashed || s.Generation != generation || named[s.Key()] {
			continue
		}
		// A session name ends in its index: <team>-<role>-g<generation>-<index>.
		idx, err := strconv.Atoi(s.Name[strings.LastIndex(s.Name, "-")+1:])
		if err != nil {
			continue
		}
		found = append(found, crashed{key: s.Key(), index: idx})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].index < found[j].index })
	keys := make([]string, 0, len(found))
	for _, f := range found {
		keys = append(keys, f.key)
	}
	return keys
}

// pendingCrashPredecessors and crashPredecessorTaken are red-phase stubs.
func (c *Controller) pendingCrashPredecessors(t *api.Team, role string, generation int64) []string {
	return c.crashedPredecessors(t, role, generation)
}

func (c *Controller) crashPredecessorTaken(t *api.Team, role string, generation int64, key string) {}
