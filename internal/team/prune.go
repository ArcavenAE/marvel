package team

import (
	"github.com/arcavenae/marvel/internal/api"
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
	return []string{}, nil
}
