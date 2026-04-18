// Package team implements the team controller — a reconciliation loop
// that maintains desired replica count for each role within each team,
// and orchestrates shift operations (rolling session replacement).
package team

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/session"
)

// Controller reconciles desired team state with actual running sessions.
type Controller struct {
	store      *api.Store
	sessMgr    *session.Manager
	SocketPath string
	mu         sync.Mutex

	// roleHealth tracks per-role crash-loop state: restart count and
	// next-allowed-restart deadline. Keyed by workspace/team/role so
	// state survives session delete+recreate across restarts — the
	// pre-fix implementation reset the counter on every rebuild, which
	// made the backoff and max-restart caps impossible to enforce.
	// See ArcavenAE/marvel#11.
	roleHealth map[string]*RoleHealth

	// now is an injection point for tests; nil means time.Now().UTC().
	now func() time.Time
}

// RoleHealth is the per-role crash-loop tracking state.
type RoleHealth struct {
	RestartCount  int
	LastRestartAt time.Time
	// BackoffUntil is the wall-clock after which the next restart is
	// allowed. Sessions whose role is inside the backoff window are
	// marked SessionCrashLoopBackOff and left alive (not deleted) so
	// operators see the condition and the reconciler doesn't respawn.
	BackoffUntil time.Time
}

// Restart backoff configuration. Exponential with a 5-minute cap — the
// defaults Skippy suggested in ArcavenAE/marvel#11.
const (
	restartBackoffInitial = 30 * time.Second
	restartBackoffMax     = 5 * time.Minute
)

// NewController creates a team controller.
func NewController(store *api.Store, sessMgr *session.Manager) *Controller {
	return &Controller{
		store:      store,
		sessMgr:    sessMgr,
		roleHealth: make(map[string]*RoleHealth),
	}
}

// computeBackoff returns the exponential backoff duration for the nth
// restart (1-indexed): 30s, 60s, 2m, 4m, 5m, 5m, ...
func computeBackoff(n int) time.Duration {
	if n <= 1 {
		return restartBackoffInitial
	}
	d := restartBackoffInitial << (n - 1)
	if d <= 0 || d > restartBackoffMax { // guards against overflow too
		return restartBackoffMax
	}
	return d
}

func (c *Controller) nowUTC() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now().UTC()
}

func (c *Controller) getRoleHealth(key string) *RoleHealth {
	rh, ok := c.roleHealth[key]
	if !ok {
		rh = &RoleHealth{}
		c.roleHealth[key] = rh
	}
	return rh
}

// RoleHealthSnapshot returns the current restart state for a role,
// useful for tests and for `marvel describe team` observability.
// Returns (nil, false) if the role has no recorded crash-loop history.
func (c *Controller) RoleHealthSnapshot(workspace, team, role string) (*RoleHealth, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := workspace + "/" + team + "/" + role
	rh, ok := c.roleHealth[key]
	if !ok {
		return nil, false
	}
	return &RoleHealth{
		RestartCount:  rh.RestartCount,
		LastRestartAt: rh.LastRestartAt,
		BackoffUntil:  rh.BackoffUntil,
	}, true
}

// ReconcileOnce runs one reconciliation pass for all teams.
// Reaps dead sessions first so the reconciler sees accurate state.
func (c *Controller) ReconcileOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// The reap path converges with the health path on the crash-loop
	// bookkeeping: a clean exit that vacates the pane is just as much a
	// crash as a stale heartbeat, and must bump RoleHealth counters +
	// extend the backoff window. Without this, `marvel inject ... "exit"`
	// respawned instantly forever (ArcavenAE/marvel#11). We defer the
	// spawn decision to reconcileRole, which already honors BackoffUntil
	// and (now) MaxRestarts saturation.
	for _, r := range c.sessMgr.ReapDead() {
		c.noteReapedCrash(r)
	}
	c.evaluateHealth()

	teams := c.store.ListTeams()
	for _, t := range teams {
		c.reconcileTeam(t)
	}
}

// noteReapedCrash attributes a reaped session to its role and records a
// crash in the role's health. If the team or role has vanished (e.g.,
// workspace delete cascade in progress), the crash is untracked — the
// reconciler won't try to recreate those sessions anyway.
func (c *Controller) noteReapedCrash(r session.ReapedSession) {
	t, err := c.store.GetTeam(r.Workspace + "/" + r.Team)
	if err != nil {
		return
	}
	var role *api.Role
	for i := range t.Roles {
		if t.Roles[i].Name == r.Role {
			role = &t.Roles[i]
			break
		}
	}
	if role == nil {
		return
	}
	roleKey := r.Workspace + "/" + r.Team + "/" + r.Role
	if c.noteCrashAndBackoff(r.Workspace, r.Team, r.Role, role.MaxRestarts) {
		rh := c.roleHealth[roleKey]
		log.Printf("reap: session %s crashed (role %s restart #%d, next backoff=%s)",
			r.Key, roleKey, rh.RestartCount, time.Until(rh.BackoffUntil))
	} else {
		log.Printf("reap: session %s crashed but role %s already at max_restarts=%d",
			r.Key, roleKey, role.MaxRestarts)
	}
}

// saturationFreezeUntil is the sentinel BackoffUntil used to freeze a
// saturated role. Chosen far enough in the future that arithmetic on it
// cannot overflow or wrap within the lifetime of a daemon process.
var saturationFreezeUntil = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// noteCrashAndBackoff records a crash on the role: increments the restart
// counter and extends the backoff window. Returns false (and does not
// bump RestartCount) once the role has saturated MaxRestarts — in that
// case it freezes BackoffUntil to the far future so reconcileRole's
// backoff gate permanently refuses to spawn replacements. Shared by the
// health path (via restartSession) and the reap path (via
// noteReapedCrash) so both converge on the same RoleHealth state. See
// ArcavenAE/marvel#11.
func (c *Controller) noteCrashAndBackoff(workspace, team, role string, maxRestarts int) bool {
	roleKey := workspace + "/" + team + "/" + role
	rh := c.getRoleHealth(roleKey)
	if maxRestarts > 0 && rh.RestartCount >= maxRestarts {
		rh.BackoffUntil = saturationFreezeUntil
		return false
	}
	now := c.nowUTC()
	rh.RestartCount++
	rh.LastRestartAt = now
	nextBackoff := computeBackoff(rh.RestartCount + 1)
	rh.BackoffUntil = now.Add(nextBackoff)
	return true
}

func (c *Controller) reconcileTeam(t *api.Team) {
	if t.Shift.Phase != api.ShiftNone {
		c.reconcileShift(t)
		return
	}
	for i := range t.Roles {
		c.reconcileRole(t, &t.Roles[i])
	}
}

func (c *Controller) reconcileRole(t *api.Team, role *api.Role) {
	// Normal reconciliation uses all generations — generation scoping is only
	// for shift logic (shiftLaunch/shiftDrain). This ensures non-shifting roles
	// aren't disrupted when only one role shifts and the team generation advances.
	current := c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name)
	desired := role.Replicas
	actual := len(current)

	if actual < desired {
		// Respect crash-loop backoff. If the role is cooling down from
		// a recent restart, hold off on spawning replacements until the
		// backoff window elapses. Without this the reconciler would
		// immediately recreate a session we just deleted and defeat
		// the whole backoff. Reap-path saturation (MaxRestarts) is
		// also honored here: noteCrashAndBackoff freezes BackoffUntil
		// to the far future on saturation, so this same gate refuses
		// respawns once a role has exhausted its budget. See
		// ArcavenAE/marvel#11.
		roleKey := t.Workspace + "/" + t.Name + "/" + role.Name
		if rh, ok := c.roleHealth[roleKey]; ok && c.nowUTC().Before(rh.BackoffUntil) {
			return
		}
		for i := actual; i < desired; i++ {
			name := fmt.Sprintf("%s-%s-g%d-%d", t.Name, role.Name, t.Generation, c.nextIndex(t, role, t.Generation))
			sess := &api.Session{
				Name:       name,
				Workspace:  t.Workspace,
				Team:       t.Name,
				Role:       role.Name,
				Generation: t.Generation,
				Runtime:    role.Runtime,
			}
			if err := c.sessMgr.Create(sess); err != nil {
				log.Printf("reconcile: create session %s: %v", name, err)
			}
		}
	} else if actual > desired {
		excess := actual - desired
		for i := 0; i < excess; i++ {
			sess := current[len(current)-1-i]
			if err := c.sessMgr.Delete(sess.Key()); err != nil {
				log.Printf("reconcile: delete session %s: %v", sess.Key(), err)
			}
		}
	}
}

// evaluateHealth checks heartbeat staleness for all sessions and applies
// restart policies when failure thresholds are exceeded.
func (c *Controller) evaluateHealth() {
	now := time.Now().UTC()
	sessions := c.store.ListSessions()

	// Build a lookup cache: workspace/team → team (avoid repeated store access).
	teamCache := make(map[string]*api.Team)
	for _, t := range c.store.ListTeams() {
		teamCache[t.Key()] = t
	}

	for _, sess := range sessions {
		if sess.State != api.SessionRunning {
			continue
		}

		teamKey := fmt.Sprintf("%s/%s", sess.Workspace, sess.Team)
		t, ok := teamCache[teamKey]
		if !ok {
			continue
		}

		var role *api.Role
		for i := range t.Roles {
			if t.Roles[i].Name == sess.Role {
				role = &t.Roles[i]
				break
			}
		}
		if role == nil || role.HealthCheck == nil {
			sess.HealthState = api.HealthUnknown
			continue
		}

		if role.HealthCheck.Type != api.HealthCheckHeartbeat {
			// process-alive is handled by ReapDead. If we're here, the pane exists.
			sess.HealthState = api.HealthHealthy
			sess.FailureCount = 0
			continue
		}

		// Heartbeat staleness check.
		sess.LastHealthCheck = now

		if sess.LastHeartbeat.IsZero() {
			// Grace period: allow timeout from creation for first heartbeat.
			if now.Sub(sess.CreatedAt) < role.HealthCheck.Timeout {
				sess.HealthState = api.HealthUnknown
				continue
			}
			sess.FailureCount++
		} else if now.Sub(sess.LastHeartbeat) > role.HealthCheck.Timeout {
			sess.FailureCount++
		} else {
			sess.FailureCount = 0
			sess.HealthState = api.HealthHealthy
			continue
		}

		if sess.FailureCount >= role.HealthCheck.FailureThreshold {
			sess.HealthState = api.HealthUnhealthy
			c.applyRestartPolicy(sess, t, role)
		} else {
			sess.HealthState = api.HealthUnhealthy
		}
	}
}

func (c *Controller) applyRestartPolicy(sess *api.Session, t *api.Team, role *api.Role) {
	switch role.RestartPolicy {
	case api.RestartNever:
		sess.State = api.SessionFailed
		log.Printf("health: session %s failed (restart_policy=never, failures=%d)",
			sess.Key(), sess.FailureCount)
	case api.RestartOnFailure:
		if sess.State == api.SessionFailed {
			c.restartSession(sess, t, role)
		} else {
			sess.State = api.SessionFailed
		}
	default: // RestartAlways
		c.restartSession(sess, t, role)
	}
}

// restartSession decides whether to restart an unhealthy session now,
// hold it in crash-loop backoff, or mark it permanently failed. State
// is tracked on the Controller's roleHealth map (keyed by workspace/
// team/role) so the restart count and backoff window survive the
// Delete+Recreate round-trip that a restart implies.
//
// Policy summary:
//   - First restart is immediate (k8s-style).
//   - Subsequent restarts wait the exponential backoff window.
//   - If role.MaxRestarts > 0 and we've hit it, the session moves to
//     SessionFailed and stays there.
//
// Ref: ArcavenAE/marvel#11, aae-orc-xhk.
func (c *Controller) restartSession(sess *api.Session, t *api.Team, role *api.Role) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role.Name
	rh := c.getRoleHealth(roleKey)
	now := c.nowUTC()

	// Inside the backoff window: mark visible, keep the pane alive,
	// let the reconciler hold steady — do not create replacements
	// during backoff, and do not re-kill the session we're waiting on.
	// Checked before saturation so we don't clobber a CrashLoopBackOff
	// marker with Failed on the tick that hits MaxRestarts.
	if now.Before(rh.BackoffUntil) {
		sess.State = api.SessionCrashLoopBackOff
		return
	}

	// Saturation check: noteCrashAndBackoff refuses to record a crash
	// once MaxRestarts is hit, so we treat a false return as permanent
	// failure and keep the session in the store. reconcileRole's
	// MaxRestarts gate then refuses to spawn a replacement.
	if !c.noteCrashAndBackoff(t.Workspace, t.Name, role.Name, role.MaxRestarts) {
		if sess.State != api.SessionFailed {
			log.Printf("health: session %s: role %s hit max_restarts=%d, not restarting",
				sess.Key(), roleKey, role.MaxRestarts)
		}
		sess.State = api.SessionFailed
		return
	}

	log.Printf("health: restarting session %s (role %s restart #%d, next backoff=%s)",
		sess.Key(), roleKey, rh.RestartCount, time.Until(rh.BackoffUntil))
	sess.RestartCount++
	sess.State = api.SessionFailed
	if err := c.sessMgr.Delete(sess.Key()); err != nil {
		log.Printf("health: delete session %s for restart: %v", sess.Key(), err)
	}
	// Reconciler sees actual<desired on its next pass but holds off on
	// recreating until BackoffUntil elapses (set by noteCrashAndBackoff
	// above).
}

// InitiateShift starts a shift operation for a team.
// If role is empty, all roles shift (supervisor last). If role is specified, only that role shifts.
func (c *Controller) InitiateShift(teamKey, role string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return fmt.Errorf("get team %s: %w", teamKey, err)
	}

	if t.Shift.Phase != api.ShiftNone {
		return fmt.Errorf("team %s: shift already in progress (phase: %s)", teamKey, t.Shift.Phase)
	}

	// Build role list in shift order (supervisor last).
	var roles []string
	if role != "" {
		found := false
		for _, r := range t.Roles {
			if r.Name == role {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("team %s: role %s not found", teamKey, role)
		}
		roles = []string{role}
	} else {
		roles = shiftOrder(t.Roles)
	}

	oldGen := t.Generation
	t.Generation++
	t.Shift = api.ShiftState{
		Phase:         api.ShiftLaunching,
		OldGeneration: oldGen,
		RoleIndex:     0,
		Roles:         roles,
		StartedAt:     time.Now().UTC(),
	}

	log.Printf("shift: initiated for %s gen %d→%d roles=%v", teamKey, oldGen, t.Generation, roles)
	return nil
}

// shiftOrder returns role names sorted with "supervisor" last.
func shiftOrder(roles []api.Role) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		if names[i] == "supervisor" {
			return false
		}
		if names[j] == "supervisor" {
			return true
		}
		return false // preserve original order for non-supervisors
	})
	return names
}

func (c *Controller) reconcileShift(t *api.Team) {
	if t.Shift.RoleIndex >= len(t.Shift.Roles) {
		// All roles shifted — complete.
		log.Printf("shift: complete for %s/%s", t.Workspace, t.Name)
		t.Shift = api.ShiftState{}
		return
	}

	shiftingRoleName := t.Shift.Roles[t.Shift.RoleIndex]

	// Reconcile non-shifting roles normally with current generation.
	for i := range t.Roles {
		if t.Roles[i].Name != shiftingRoleName {
			c.reconcileRole(t, &t.Roles[i])
		}
	}

	// Find the role being shifted.
	var role *api.Role
	for i := range t.Roles {
		if t.Roles[i].Name == shiftingRoleName {
			role = &t.Roles[i]
			break
		}
	}
	if role == nil {
		log.Printf("shift: role %s not found in team %s, skipping", shiftingRoleName, t.Key())
		t.Shift.RoleIndex++
		return
	}

	switch t.Shift.Phase {
	case api.ShiftLaunching:
		c.shiftLaunch(t, role)
	case api.ShiftDraining:
		c.shiftDrain(t, role)
	}
}

func (c *Controller) shiftLaunch(t *api.Team, role *api.Role) {
	newGen := c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role.Name, t.Generation)
	desired := role.Replicas

	if len(newGen) < desired {
		// Create remaining new-gen sessions.
		for i := len(newGen); i < desired; i++ {
			name := fmt.Sprintf("%s-%s-g%d-%d", t.Name, role.Name, t.Generation, c.nextIndex(t, role, t.Generation))
			sess := &api.Session{
				Name:       name,
				Workspace:  t.Workspace,
				Team:       t.Name,
				Role:       role.Name,
				Generation: t.Generation,
				Runtime:    role.Runtime,
			}
			if err := c.sessMgr.Create(sess); err != nil {
				log.Printf("shift: create session %s: %v", name, err)
				return
			}
		}
	}

	// All new-gen sessions created — check readiness, then transition to draining.
	newGen = c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role.Name, t.Generation)
	if len(newGen) >= desired {
		if c.allReady(newGen, role) {
			log.Printf("shift: %s/%s role %s — %d new sessions ready, draining old gen %d",
				t.Workspace, t.Name, role.Name, len(newGen), t.Shift.OldGeneration)
			t.Shift.Phase = api.ShiftDraining
		} else {
			log.Printf("shift: %s/%s role %s — %d sessions launched, waiting for readiness",
				t.Workspace, t.Name, role.Name, len(newGen))
		}
	}
}

// allReady returns true if all sessions are ready to take over.
// For roles without a healthcheck, pane existence (Running state) is sufficient.
// For heartbeat-based checks, at least one heartbeat must have been received.
func (c *Controller) allReady(sessions []*api.Session, role *api.Role) bool {
	if role.HealthCheck == nil {
		// No healthcheck — running state is sufficient.
		for _, s := range sessions {
			if s.State != api.SessionRunning {
				return false
			}
		}
		return true
	}
	for _, s := range sessions {
		if s.State != api.SessionRunning {
			return false
		}
		if role.HealthCheck.Type == api.HealthCheckHeartbeat && s.LastHeartbeat.IsZero() {
			return false
		}
	}
	return true
}

func (c *Controller) shiftDrain(t *api.Team, role *api.Role) {
	oldGen := c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role.Name, t.Shift.OldGeneration)

	if len(oldGen) == 0 {
		// All old-gen drained for this role — advance to next role.
		log.Printf("shift: %s/%s role %s — old gen drained", t.Workspace, t.Name, role.Name)
		t.Shift.RoleIndex++
		if t.Shift.RoleIndex < len(t.Shift.Roles) {
			t.Shift.Phase = api.ShiftLaunching
		}
		return
	}

	// Rolling drain: delete one old-gen session per reconcile tick.
	sess := oldGen[0]
	if err := c.sessMgr.Delete(sess.Key()); err != nil {
		log.Printf("shift: drain session %s: %v", sess.Key(), err)
	}
}

// nextIndex finds the next available index for a role's sessions within a generation.
func (c *Controller) nextIndex(t *api.Team, role *api.Role, generation int64) int {
	current := c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role.Name, generation)
	prefix := fmt.Sprintf("%s-%s-g%d-", t.Name, role.Name, generation)
	max := -1
	for _, s := range current {
		var idx int
		if _, err := fmt.Sscanf(s.Name, prefix+"%d", &idx); err == nil {
			if idx > max {
				max = idx
			}
		}
	}
	return max + 1
}

// Run starts the reconciliation loop, reconciling every interval.
// Blocks until ctx is cancelled.
func (c *Controller) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Reconcile immediately on start.
	c.ReconcileOnce()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.ReconcileOnce()
		}
	}
}

// IsShifting returns true if the team is currently shifting.
func (c *Controller) IsShifting(teamKey string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return false
	}
	return t.Shift.Phase != api.ShiftNone
}
