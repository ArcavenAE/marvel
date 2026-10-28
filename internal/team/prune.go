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
// team: "" when it may be pruned, else why it is kept or deferred. It runs at
// the mark, in the store's lock, and again in the store hold that would delete
// the team's record, with the drain rule after it. A team with no seat left
// is deleted there, its sessions after the record, exactly those the last
// check saw. A team whose seats are still leaving stays marked and is
// returned in draining: the reconcile retires its seats, and finishPrune
// deletes it once it is empty. A team that fails a check is not deleted, its
// Pruning is cleared, and it is returned in skipped with the reason. Each list
// is in key order.
func (c *Controller) Prune(keys []string, p api.TeamPruning, check func(*api.Team, []api.Session) string) (deleted []string, draining []api.PruneDraining, skipped []api.PruneSkipped) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deleted, draining = []string{}, []api.PruneDraining{}
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	deleteCheck := api.PruneDeleteCheck(check)
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
		reason, sessions, err := c.store.DeleteTeamIf(key, deleteCheck)
		if err != nil {
			reason = err.Error()
		}
		if reason == api.ReasonDraining {
			ws, name, _ := strings.Cut(key, "/")
			draining = append(draining, api.PruneDraining{Team: key, Seats: api.CountAlive(c.store.ListSessionsByTeam(ws, name))})
			continue
		}
		if reason != "" {
			c.unmarkPruning(key)
			skipped = append(skipped, api.PruneSkipped{Team: key, Reason: reason})
			continue
		}
		if c.pruneHook != nil {
			c.pruneHook(key, "sessions")
		}
		c.finishPrunedTeamLocked(key, sessions, p.Source)
		deleted = append(deleted, key)
	}
	return deleted, draining, skipped
}

// finishPrune deletes a marked team whose seats have all left, re-running the
// prune's rules 1 to 3 in the hold that deletes its record. A team that now
// fails one is kept and loses its mark. Caller holds c.mu.
func (c *Controller) finishPrune(t *api.Team) {
	source := t.Pruning.Source
	reason, sessions, err := c.store.DeleteTeamIf(t.Key(), api.PruneDeleteCheck(api.PruneRecheck(source)))
	switch {
	case err != nil:
		log.Printf("prune: %s: %v", t.Key(), err)
		return
	case reason == api.ReasonDraining:
		return
	case reason != "":
		log.Printf("prune: %s not deleted: %s", t.Key(), reason)
		c.unmarkPruning(t.Key())
		return
	}
	c.finishPrunedTeamLocked(t.Key(), sessions, source)
	c.prunedThisTick = append(c.prunedThisTick, t.Key())
}

// finishPrunedTeamLocked is what follows a pruned team's record delete: its
// finished sessions go, exactly those the last check saw, its roles' health is
// forgotten so a re-apply starts fresh, and team.pruned records it. Caller
// holds c.mu.
func (c *Controller) finishPrunedTeamLocked(key string, sessions []string, source string) {
	c.deleteSessionsLocked(sessions)
	ws, name, _ := strings.Cut(key, "/")
	c.clearRoleHealthForTeamLocked(ws, name)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindTeamPruned,
		Severity:  events.SeverityInfo,
		Workspace: ws,
		Team:      name,
		Message:   "team pruned by " + source,
	})
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
