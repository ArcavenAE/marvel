package team

import (
	"log"
	"sort"

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
	for _, s := range c.store.ListSessionsByTeam(t.Workspace, t.Name) {
		_ = c.sessMgr.Delete(s.Key())
	}
	err := c.store.DeleteTeam(t.Key())
	c.clearRoleHealthForTeamLocked(t.Workspace, t.Name)
	return err
}

// Prune marks, re-checks and deletes each team of a prune set in one hold of
// c.mu, so no reconcile pass runs between the mark and the delete
// (docs/design/work-prune.md section 5). check is the prune's rule set for one
// team: "" when it may be deleted, else why it is kept or deferred. It runs at
// the mark, in the store's lock, and again just before the delete; a team that
// fails either is not deleted, its Pruning is cleared, and it is returned in
// skipped with the reason. Deleted teams are returned in key order.
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
		t, err := c.store.GetTeam(key)
		if err != nil {
			skipped = append(skipped, api.PruneSkipped{Team: key, Reason: err.Error()})
			continue
		}
		if reason := c.recheckPrune(&t, check); reason != "" {
			c.unmarkPruning(key)
			skipped = append(skipped, api.PruneSkipped{Team: key, Reason: reason})
			continue
		}
		if err := c.deleteTeamLocked(t); err != nil {
			c.unmarkPruning(key)
			skipped = append(skipped, api.PruneSkipped{Team: key, Reason: err.Error()})
			continue
		}
		deleted = append(deleted, key)
		events.Emit(c.Events, events.Event{
			Kind:      events.KindTeamPruned,
			Severity:  events.SeverityInfo,
			Workspace: t.Workspace,
			Team:      t.Name,
			Message:   "team pruned by " + p.Source,
		})
	}
	return deleted, skipped
}

// recheckPrune runs check once more just before the delete. A team an apply
// named since the mark has lost its Pruning, and is kept.
func (c *Controller) recheckPrune(t *api.Team, check func(*api.Team, []api.Session) string) string {
	if t.Pruning == nil {
		return "kept: named again by an apply"
	}
	sessions := c.store.ListSessionsByTeam(t.Workspace, t.Name)
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Key() < sessions[j].Key() })
	return check(t, sessions)
}

// unmarkPruning clears a team's Pruning, so a team the prune did not delete
// converges as before.
func (c *Controller) unmarkPruning(key string) {
	if err := c.store.UpdateTeam(key, func(live *api.Team) error {
		live.Pruning = nil
		return nil
	}); err != nil {
		log.Printf("prune: clear the mark on %s: %v", key, err)
	}
}
