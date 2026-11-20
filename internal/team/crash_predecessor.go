package team

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// crashedPredecessors returns the keys of the seats of role, in one
// generation, that crash repair is about to replace, in index order. A
// replacement takes one as its predecessor, so its MARVEL_PREDECESSOR names the
// seat whose handoff, mail and lock it inherits (marvel#541, marvel#1402). A
// key a live seat already names is spent, as a wake spends it. A Crashed row of
// another generation is not named here; ClearCrashedForRole still deletes it.
func (c *Controller) crashedPredecessors(t *api.Team, role string, generation int64) []string {
	rows := c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role)
	named := liveNamed(rows)
	type crashed struct {
		key   string
		index int
	}
	var found []crashed
	for _, s := range rows {
		if s.State != api.SessionCrashed || s.Generation != generation || named[s.Key()] {
			continue
		}
		if idx, ok := seatIndex(s.Key()); ok {
			found = append(found, crashed{key: s.Key(), index: idx})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].index < found[j].index })
	keys := make([]string, 0, len(found))
	for _, f := range found {
		keys = append(keys, f.key)
	}
	return keys
}

// liveNamed is the set of keys the live seats among rows name as predecessor.
func liveNamed(rows []api.Session) map[string]bool {
	named := map[string]bool{}
	for _, s := range rows {
		if s.State.CountsAsAlive() && s.Predecessor != "" {
			named[s.Predecessor] = true
		}
	}
	return named
}

// seatIndex reads the index a session key ends in: <team>-<role>-g<generation>-<index>.
func seatIndex(key string) (int, bool) {
	idx, err := strconv.Atoi(key[strings.LastIndex(key, "-")+1:])
	return idx, err == nil
}

// crashPendingKey names one role's pending list in one generation of one team.
type crashPendingKey struct {
	team       string
	role       string
	generation int64
}

func pendingKey(t *api.Team, role string, generation int64) crashPendingKey {
	return crashPendingKey{team: t.Key(), role: role, generation: generation}
}

// pendingCrashPredecessors is crashedPredecessors plus the keys an earlier tick
// read and no created seat has taken yet. The spawn loop clears the Crashed
// rows in the tick that reads them, but a replacement may not be created in
// that tick: a view build defers it, Create refuses it, or plan.Spawn is
// smaller than the crashed count. The keys stay, per role and generation, until
// crashPredecessorTaken drops one or a live seat names it. The result is in
// index order.
func (c *Controller) pendingCrashPredecessors(t *api.Team, role string, generation int64) []string {
	fresh := c.crashedPredecessors(t, role, generation)
	spent := liveNamed(c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role))
	key := pendingKey(t, role, generation)

	c.crashMu.Lock()
	defer c.crashMu.Unlock()
	var merged []string
	for _, k := range append(slices.Clone(c.crashPending[key]), fresh...) {
		if !spent[k] && !slices.Contains(merged, k) {
			merged = append(merged, k)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		a, _ := seatIndex(merged[i])
		b, _ := seatIndex(merged[j])
		return a < b
	})
	if len(merged) == 0 {
		delete(c.crashPending, key)
		return nil
	}
	if c.crashPending == nil {
		c.crashPending = map[crashPendingKey][]string{}
	}
	c.crashPending[key] = merged
	return slices.Clone(merged)
}

// crashPredecessorTaken drops key from the pending list once a created seat
// names it.
func (c *Controller) crashPredecessorTaken(t *api.Team, role string, generation int64, key string) {
	pk := pendingKey(t, role, generation)
	c.crashMu.Lock()
	defer c.crashMu.Unlock()
	rest := slices.DeleteFunc(slices.Clone(c.crashPending[pk]), func(k string) bool { return k == key })
	if len(rest) == 0 {
		delete(c.crashPending, pk)
		return
	}
	c.crashPending[pk] = rest
}

// keepRestartPredecessor records the key of a seat a health restart just
// deleted, so the replacement the reconciler spawns after the backoff names it.
// A crash leaves its row until the spawn loop reads it; a health restart
// deletes the row at once, so the key goes to the pending list here, where the
// spawn loop reads it with the crashed keys (marvel#1402).
func (c *Controller) keepRestartPredecessor(t *api.Team, role string, generation int64, key string) {
	pk := pendingKey(t, role, generation)
	c.crashMu.Lock()
	defer c.crashMu.Unlock()
	if slices.Contains(c.crashPending[pk], key) {
		return
	}
	if c.crashPending == nil {
		c.crashPending = map[crashPendingKey][]string{}
	}
	pending := append(slices.Clone(c.crashPending[pk]), key)
	sort.SliceStable(pending, func(i, j int) bool {
		a, _ := seatIndex(pending[i])
		b, _ := seatIndex(pending[j])
		return a < b
	})
	c.crashPending[pk] = pending
}

// dropCrashPending forgets a role's pending keys: the role wants no spawn, so
// no replacement is coming for them.
func (c *Controller) dropCrashPending(t *api.Team, role string, generation int64) {
	c.crashMu.Lock()
	defer c.crashMu.Unlock()
	delete(c.crashPending, pendingKey(t, role, generation))
}

// pruneCrashPending forgets the pending keys of a team that is gone, of a
// generation the team has left, and of a role the team no longer declares.
func (c *Controller) pruneCrashPending() {
	c.crashMu.Lock()
	defer c.crashMu.Unlock()
	for k := range c.crashPending {
		team, err := c.store.GetTeam(k.team)
		if err != nil || team.Generation != k.generation || !slices.ContainsFunc(team.Roles, func(r api.Role) bool { return r.Name == k.role }) {
			delete(c.crashPending, k)
		}
	}
}
