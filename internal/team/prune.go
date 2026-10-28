package team

import (
	"errors"
	"log"
	"sort"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// DeleteTeam deletes the team at key with its sessions, and forgets its roles'
// crash-loop state so a later re-apply starts fresh (ArcavenAE/marvel#29). It
// is marvel delete team, and the delete a prune ends with. The caller revokes
// the team's broker user after, as both do.
func (c *Controller) DeleteTeam(t api.Team) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteTeamLocked(t)
}

// deleteTeamLocked is DeleteTeam for a caller that holds c.mu.
func (c *Controller) deleteTeamLocked(t api.Team) error {
	var keys []string
	for _, s := range c.store.ListSessionsByTeam(t.Workspace, t.Name) {
		keys = append(keys, s.Key())
	}
	c.deleteSessionsLocked(keys)
	err := c.store.DeleteTeam(t.Key())
	c.clearRoleHealthForTeamLocked(t.Workspace, t.Name)
	return err
}

// deleteSessionsLocked deletes the sessions named by keys. Caller holds c.mu.
func (c *Controller) deleteSessionsLocked(keys []string) {
	for _, k := range keys {
		_ = c.sessMgr.Delete(k)
	}
}

// Prune marks, re-checks and deletes each team of a prune set in one hold of
// c.mu, so no reconcile pass runs between the mark and the delete
// (docs/design/work-prune.md section 5). check is the prune's rule set for one
// team: "" when it may be deleted, else why it is kept or deferred. It runs at
// the mark, in the store's lock, and again in the store hold that deletes the
// team's record, so an apply that claims the team before that hold keeps it.
// The team's sessions are deleted after the record, exactly those the last
// check saw. A team that fails either check is not deleted, its Pruning is
// cleared, and it is returned in skipped with the reason. Deleted teams are
// returned in key order.
func (c *Controller) Prune(keys []string, p api.TeamPruning, check func(*api.Team, []api.Session) string) (deleted []string, skipped []api.PruneSkipped) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deleted = []string{}
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	for _, key := range keys {
		reason, err := c.store.MarkPruning(key, p, check)
		if err != nil {
			skipped = append(skipped, api.PruneSkipped{Team: key, Reason: err.Error()})
			continue
		}
		if reason != "" {
			skipped = append(skipped, api.PruneSkipped{Team: key, Reason: reason})
			continue
		}
		if c.pruneHook != nil {
			c.pruneHook(key, "delete")
		}
		reason, sessions, err := c.store.DeleteTeamIf(key, check)
		if err != nil {
			reason = err.Error()
		}
		if reason != "" {
			c.unmarkPruning(key)
			skipped = append(skipped, api.PruneSkipped{Team: key, Reason: reason})
			continue
		}
		if c.pruneHook != nil {
			c.pruneHook(key, "sessions")
		}
		c.deleteSessionsLocked(sessions)
		ws, name, _ := strings.Cut(key, "/")
		c.clearRoleHealthForTeamLocked(ws, name)
		deleted = append(deleted, key)
		events.Emit(c.Events, events.Event{
			Kind:      events.KindTeamPruned,
			Severity:  events.SeverityInfo,
			Workspace: ws,
			Team:      name,
			Message:   "team pruned by " + p.Source,
		})
	}
	return deleted, skipped
}

// unmarkPruning clears a team's Pruning, so a team the prune did not delete
// converges as before. A team already gone has nothing to clear.
func (c *Controller) unmarkPruning(key string) {
	if err := c.store.UpdateTeam(key, func(live *api.Team) error {
		live.Pruning = nil
		return nil
	}); err != nil && !errors.Is(err, api.ErrNotFound) {
		log.Printf("prune: clear the mark on %s: %v", key, err)
	}
}
