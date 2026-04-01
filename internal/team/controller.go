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
}

// NewController creates a team controller.
func NewController(store *api.Store, sessMgr *session.Manager) *Controller {
	return &Controller{store: store, sessMgr: sessMgr}
}

// ReconcileOnce runs one reconciliation pass for all teams.
// Reaps dead sessions first so the reconciler sees accurate state.
func (c *Controller) ReconcileOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessMgr.ReapDead()
	c.evaluateHealth()

	teams := c.store.ListTeams()
	for _, t := range teams {
		c.reconcileTeam(t)
	}
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
		for i := actual; i < desired; i++ {
			name := fmt.Sprintf("%s-%s-g%d-%d", t.Name, role.Name, t.Generation, c.nextIndex(t, role, t.Generation))
			rt := role.Runtime
			rt.Args = c.injectIdentity(rt.Args, name, t, role, rt.Script)
			sess := &api.Session{
				Name:       name,
				Workspace:  t.Workspace,
				Team:       t.Name,
				Role:       role.Name,
				Generation: t.Generation,
				Runtime:    rt,
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
			c.applyRestartPolicy(sess, role)
		} else {
			sess.HealthState = api.HealthUnhealthy
		}
	}
}

func (c *Controller) applyRestartPolicy(sess *api.Session, role *api.Role) {
	switch role.RestartPolicy {
	case api.RestartNever:
		sess.State = api.SessionFailed
		log.Printf("health: session %s failed (restart_policy=never, failures=%d)",
			sess.Key(), sess.FailureCount)
	case api.RestartOnFailure:
		if sess.State == api.SessionFailed {
			c.restartSession(sess)
		} else {
			sess.State = api.SessionFailed
		}
	default: // RestartAlways
		c.restartSession(sess)
	}
}

func (c *Controller) restartSession(sess *api.Session) {
	log.Printf("health: restarting session %s (failures=%d, restarts=%d)",
		sess.Key(), sess.FailureCount, sess.RestartCount)
	sess.RestartCount++
	sess.State = api.SessionFailed
	if err := c.sessMgr.Delete(sess.Key()); err != nil {
		log.Printf("health: delete session %s for restart: %v", sess.Key(), err)
	}
	// Reconciler will recreate because actual < desired.
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
			rt := role.Runtime
			rt.Args = c.injectIdentity(rt.Args, name, t, role, rt.Script)
			sess := &api.Session{
				Name:       name,
				Workspace:  t.Workspace,
				Team:       t.Name,
				Role:       role.Name,
				Generation: t.Generation,
				Runtime:    rt,
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

// injectIdentity appends identity and script flags for runtimes that support them.
func (c *Controller) injectIdentity(args []string, name string, t *api.Team, role *api.Role, script string) []string {
	if role.Name == "" && script == "" {
		return args
	}
	injected := make([]string, len(args))
	copy(injected, args)
	if script != "" {
		injected = append(injected, "--script", script)
	}
	injected = append(injected,
		"--name", name,
		"--workspace", t.Workspace,
		"--team", t.Name,
		"--role", role.Name,
	)
	if c.SocketPath != "" {
		injected = append(injected, "--socket", c.SocketPath)
	}
	return injected
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
