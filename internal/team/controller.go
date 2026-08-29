// Package team implements the team controller — a reconciliation loop
// that maintains desired replica count for each role within each team,
// and orchestrates shift operations (rolling session replacement).
package team

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/session"
)

// Controller reconciles desired team state with actual running sessions.
type Controller struct {
	store      *api.Store
	sessMgr    *session.Manager
	SocketPath string
	// Events receives structured state-transition events. Nil is safe.
	Events events.Emitter
	mu     sync.Mutex

	// roleHealth tracks per-role crash-loop state: restart count and
	// next-allowed-restart deadline. Keyed by workspace/team/role so
	// state survives session delete+recreate across restarts — the
	// pre-fix implementation reset the counter on every rebuild, which
	// made the backoff and max-restart caps impossible to enforce.
	// See ArcavenAE/marvel#11.
	//
	// This map is the live copy; the store's role_health bucket is its
	// durable mirror, written through on every change and read back by
	// RehydrateRoleHealth at daemon start. See aae-orc-qdew.
	roleHealth map[string]*RoleHealth

	// admissionHolds latches the last emitted admission Verdict.Key() per
	// role so a standing refusal emits one event per transition rather than
	// one per reconcile tick. Keyed like roleHealth ("workspace/team/role").
	//
	// In-memory only, deliberately. RoleHealth persists because a restart
	// count is history a restart must not erase; an admission hold is
	// derived from live state and recomputed within one tick of daemon
	// start, so a durable copy could only outlive its cause. Re-emitting
	// once after a restart is correct: the operator restarted the daemon and
	// the condition is still true. See aae-orc-qiay.
	admissionHolds map[string]string

	// ShiftTimeout bounds how long a single shift may run before the
	// reconciler declares it stuck and aborts it. Zero uses
	// defaultShiftTimeout. See aae-orc-qkfl.
	ShiftTimeout time.Duration

	// Snapshots supplies the measured state a full admission check needs.
	// Nil is safe and means count-shaped clauses only, which is all the
	// reconciler ever evaluates anyway (R2 in internal/admission: gating
	// repair on a monotonic meter would be an outage). It exists for
	// InitiateShift, whose cumulative clause needs the daemon's meter, and
	// keeps this package free of any usage import.
	Snapshots Snapshotter

	// now is an injection point for tests; nil means time.Now().UTC().
	now func() time.Time
}

// Snapshotter supplies the measured state one admission check evaluates.
// The daemon implements it over its usage accountant.
type Snapshotter interface {
	AdmissionSnapshot(t api.Team) admission.Snapshot
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
	// defaultShiftTimeout bounds how long a shift may run before the
	// reconciler treats it as stuck. A launch whose new generation never
	// reaches readiness (e.g. a heartbeat-checked role that never beats)
	// would otherwise hold the team in phase=launching forever, both
	// generations running and scale refused. Ten minutes is generous for
	// agent warm-up while still bounded. Override per Controller via
	// ShiftTimeout. See aae-orc-qkfl.
	defaultShiftTimeout = 10 * time.Minute
)

// NewController creates a team controller.
func NewController(store *api.Store, sessMgr *session.Manager) *Controller {
	return &Controller{
		store:          store,
		sessMgr:        sessMgr,
		roleHealth:     make(map[string]*RoleHealth),
		admissionHolds: make(map[string]string),
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

func (c *Controller) shiftTimeout() time.Duration {
	if c.ShiftTimeout > 0 {
		return c.ShiftTimeout
	}
	return defaultShiftTimeout
}

func (c *Controller) getRoleHealth(key string) *RoleHealth {
	rh, ok := c.roleHealth[key]
	if !ok {
		rh = &RoleHealth{}
		c.roleHealth[key] = rh
	}
	return rh
}

// RehydrateRoleHealth loads persisted crash-loop state into the
// controller's map. Called at daemon start after Store.OpenBolt has
// rehydrated the rest of L2, so a role sitting in a backoff window (or
// frozen at MaxRestarts saturation) stays held back across a daemon
// restart instead of getting a free respawn on the first reconcile tick.
// No-op when persistence is disabled. See aae-orc-qdew.
func (c *Controller) RehydrateRoleHealth() error {
	recs, err := c.store.ListRoleHealth()
	if err != nil {
		return fmt.Errorf("load role health: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rec := range recs {
		c.roleHealth[rec.Key] = &RoleHealth{
			RestartCount:  rec.RestartCount,
			LastRestartAt: rec.LastRestartAt,
			BackoffUntil:  rec.BackoffUntil,
		}
	}
	if len(recs) > 0 {
		log.Printf("role health: rehydrated %d role(s) from durable state", len(recs))
	}
	return nil
}

// persistRoleHealth mirrors one role's state into the store. A failed
// write costs the backoff window across the next restart, not
// correctness now, so it logs rather than propagating. Caller holds
// c.mu.
func (c *Controller) persistRoleHealth(key string, rh *RoleHealth) {
	if err := c.store.PersistRoleHealth(api.RoleHealthRecord{
		Key:           key,
		RestartCount:  rh.RestartCount,
		LastRestartAt: rh.LastRestartAt,
		BackoffUntil:  rh.BackoffUntil,
	}); err != nil {
		log.Printf("persist role health %s: %v", key, err)
	}
}

// forgetRoleHealth drops one role's durable state, paired with the
// in-memory delete in the cascade-clear helpers. Caller holds c.mu.
func (c *Controller) forgetRoleHealth(key string) {
	if err := c.store.DeleteRoleHealth(key); err != nil {
		log.Printf("delete role health %s: %v", key, err)
	}
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

// ClearRoleHealthForTeam deletes crash-loop state for every role under
// the given (workspace, team). Called from the cascade delete path in
// daemon.handleDelete so that a subsequent re-apply of the same manifest
// starts with a fresh RestartCount and BackoffUntil — without this,
// accumulated state survives workspace/team delete (the map is keyed
// by name, which the operator is free to reuse) and the reconciler
// refuses spawns until the prior generation's backoff window elapses.
// If the prior generation hit MaxRestarts saturation, BackoffUntil
// would be frozen far in the future and the role would never recover.
// See ArcavenAE/marvel#29.
func (c *Controller) ClearRoleHealthForTeam(workspace, team string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := workspace + "/" + team + "/"
	for k := range c.roleHealth {
		if strings.HasPrefix(k, prefix) {
			delete(c.roleHealth, k)
			c.forgetRoleHealth(k)
		}
	}
	c.dropAdmissionHolds(prefix)
}

// ClearRoleHealthForWorkspace deletes crash-loop state for every role
// under every team in the given workspace. Called from the workspace-
// delete cascade. See ClearRoleHealthForTeam for the rationale.
func (c *Controller) ClearRoleHealthForWorkspace(workspace string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := workspace + "/"
	for k := range c.roleHealth {
		if strings.HasPrefix(k, prefix) {
			delete(c.roleHealth, k)
			c.forgetRoleHealth(k)
		}
	}
	c.dropAdmissionHolds(prefix)
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
	//
	// The charge is per role per tick, not per lost replica: see
	// noteReapedCrash.
	charged := make(map[string]*reapCharge)
	for _, r := range c.sessMgr.ReapDead() {
		c.noteReapedCrash(r, charged)
	}
	c.emitReapAccounting(charged)
	c.evaluateHealth()

	teams := c.store.ListTeams()
	for i := range teams {
		c.reconcileTeam(&teams[i])
	}
}

// noteReapedCrash attributes a reaped session to its role and records a
// crash in the role's health. If the team or role has vanished (e.g.,
// workspace delete cascade in progress), the crash is untracked — the
// reconciler won't try to recreate those sessions anyway.
//
// charged tracks which roles this tick has already charged, so a tick
// that finds k panes of one role gone records ONE crash rather than k.
// RoleHealth is per-role state, so charging it per lost replica scaled a
// single event by the replica count: three replicas taken down together
// by a tmux server dying, a foreign daemon reclaiming the marvel-* prefix,
// or a host reboot moved the role from restart count 0 to 3 and from a
// 30s backoff to a 4m one, and a role with max_restarts=3 spent its whole
// budget so the next loss froze it permanently. Marvel cannot tell an
// external kill from an application exit at this point — both present as
// a pane ID tmux no longer lists, and a harness exiting from the last
// pane collapses its tmux session exactly as a kill does — so the fix is
// cause-agnostic: bound the blast radius of one event instead of guessing
// at its cause. Flapping still escalates, because flapping repeats across
// ticks. See aae-orc-4bz2.
// reapCharge accumulates one role's crash accounting across a single tick's
// reap loop, so the tick can emit ONE accounting event per role rather than
// one per lost replica (or none, as when the accounting was log-only). See
// emitReapAccounting and aae-orc-m8n0.
type reapCharge struct {
	workspace, team, role string
	suppressed            int       // same-role reaps folded into the one charge
	saturated             bool      // the charge hit max_restarts (noteCrashAndBackoff == false)
	restartCount          int       // RestartCount after the charge
	backoffUntil          time.Time // BackoffUntil after the charge
	maxRestarts           int
}

func (c *Controller) noteReapedCrash(r session.ReapedSession, charged map[string]*reapCharge) {
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
	if role.RestartPolicy == api.RestartNever {
		// The health path's never contract applies to the reap path
		// too: a vacated pane under never goes terminal instead of
		// being replaced after a backoff window. ReapDead already
		// emitted session.crashed with the cause; session.failed here
		// records the verdict.
		c.freezeRole(r.Workspace, r.Team, r.Role)
		_ = c.store.UpdateSession(r.Key, func(live *api.Session) error {
			live.State = api.SessionFailed
			return nil
		})
		log.Printf("reap: session %s crashed (restart_policy=never), role %s frozen",
			r.Key, roleKey)
		events.Emit(c.Events, events.Event{
			Kind:      events.KindSessionFailed,
			Severity:  events.SeverityWarning,
			Workspace: r.Workspace,
			Team:      r.Team,
			Role:      r.Role,
			Session:   r.Key,
			Message:   "restart_policy=never, pane gone; role frozen",
		})
		return
	}
	if acc, ok := charged[roleKey]; ok {
		acc.suppressed++
		log.Printf("reap: session %s crashed with the rest of role %s in one tick; already charged",
			r.Key, roleKey)
		return
	}
	acc := &reapCharge{workspace: r.Workspace, team: r.Team, role: r.Role, maxRestarts: role.MaxRestarts}
	charged[roleKey] = acc
	if c.noteCrashAndBackoff(r.Workspace, r.Team, r.Role, role.MaxRestarts) {
		rh := c.roleHealth[roleKey]
		acc.restartCount = rh.RestartCount
		acc.backoffUntil = rh.BackoffUntil
		log.Printf("reap: session %s crashed (role %s restart #%d, next backoff=%s)",
			r.Key, roleKey, rh.RestartCount, time.Until(rh.BackoffUntil))
	} else {
		acc.saturated = true
		acc.restartCount = c.roleHealth[roleKey].RestartCount
		log.Printf("reap: session %s crashed but role %s already at max_restarts=%d",
			r.Key, roleKey, role.MaxRestarts)
	}
}

// emitReapAccounting brings the reap path to event parity with the health
// path: after a tick's reap loop has folded a role's lost replicas into one
// charge, it emits a single accounting event per charged role so the charge,
// the suppression count, and the resulting backoff are visible on
// `marvel events` instead of inferable only from the daemon log (and from
// the absence of any event at all). No new event kind — it reuses the health
// path's KindCrashLoopBackoff / KindRoleSaturated. See aae-orc-m8n0.
func (c *Controller) emitReapAccounting(charged map[string]*reapCharge) {
	for _, acc := range charged {
		if acc.saturated {
			events.Emit(c.Events, events.Event{
				Kind:      events.KindRoleSaturated,
				Severity:  events.SeverityWarning,
				Workspace: acc.workspace,
				Team:      acc.team,
				Role:      acc.role,
				Message:   fmt.Sprintf("max_restarts=%d reached (charged 1, suppressed %d)", acc.maxRestarts, acc.suppressed),
			})
			continue
		}
		events.Emit(c.Events, events.Event{
			Kind:      events.KindCrashLoopBackoff,
			Severity:  events.SeverityWarning,
			Workspace: acc.workspace,
			Team:      acc.team,
			Role:      acc.role,
			Message: fmt.Sprintf("charged 1, suppressed %d; restart #%d, backoff until %s",
				acc.suppressed, acc.restartCount, acc.backoffUntil.Format(time.RFC3339)),
		})
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
		c.persistRoleHealth(roleKey, rh)
		return false
	}
	now := c.nowUTC()
	rh.RestartCount++
	rh.LastRestartAt = now
	nextBackoff := computeBackoff(rh.RestartCount + 1)
	rh.BackoffUntil = now.Add(nextBackoff)
	c.persistRoleHealth(roleKey, rh)
	return true
}

// freezeRole permanently blocks replacement spawns for a role by setting
// its BackoffUntil to the saturation sentinel. This is how
// restart_policy=never goes terminal: the first failure stops the role,
// so the reconciler must not repair the replica count with a fresh
// session every tick. The failed row and its pane stay visible for
// post-mortem. Recovery is the same as MaxRestarts saturation: delete
// the team and re-apply (ClearRoleHealthForTeam resets the freeze).
// See ArcavenAE/marvel#107, aae-orc-pyre.
func (c *Controller) freezeRole(workspace, team, role string) {
	roleKey := workspace + "/" + team + "/" + role
	rh := c.getRoleHealth(roleKey)
	rh.BackoffUntil = saturationFreezeUntil
	c.persistRoleHealth(roleKey, rh)
}

func (c *Controller) reconcileTeam(t *api.Team) {
	// Drain sessions whose role no longer exists in the manifest before
	// anything else. Manifest.Apply replaces live.Roles wholesale, so a
	// role removed from a re-applied manifest leaves its sessions running:
	// the loops below iterate only current roles, so orphans are never
	// counted, scaled down, or deleted and survive restarts via bolt.
	// See aae-orc-69i2.
	c.reconcileOrphanedSessions(t)

	// Drop a recorded admission condition this process never refused, before
	// any role is reconciled. See reconcileAdmissionState.
	c.reconcileAdmissionState(t)

	if t.Shift.Phase != api.ShiftNone {
		c.reconcileShift(t)
		return
	}
	for i := range t.Roles {
		c.reconcileRole(t, &t.Roles[i])
	}
}

// reconcileOrphanedSessions deletes every session whose role is absent
// from the team's current role set, drained one per orphaned role with an
// operator-visible role.removed event (the per-session deletes also emit
// the manager's own session.deleted events). Crash-loop health state for a
// removed role is forgotten so a later re-add starts clean, mirroring the
// cascade-clear rationale in ClearRoleHealthForTeam. Caller holds c.mu.
// See aae-orc-69i2.
func (c *Controller) reconcileOrphanedSessions(t *api.Team) {
	known := make(map[string]struct{}, len(t.Roles))
	for i := range t.Roles {
		known[t.Roles[i].Name] = struct{}{}
	}
	drained := make(map[string]int)
	for _, sess := range c.store.ListSessionsByTeam(t.Workspace, t.Name) {
		if _, ok := known[sess.Role]; ok {
			continue
		}
		if err := c.sessMgr.Delete(sess.Key()); err != nil {
			log.Printf("reconcile: delete orphaned session %s (role %s removed): %v", sess.Key(), sess.Role, err)
			continue
		}
		drained[sess.Role]++
	}
	for role, n := range drained {
		roleKey := t.Workspace + "/" + t.Name + "/" + role
		delete(c.roleHealth, roleKey)
		c.forgetRoleHealth(roleKey)
		delete(c.admissionHolds, roleKey)
		log.Printf("reconcile: role %s removed from team %s, drained %d session(s)", role, t.Key(), n)
		events.Emit(c.Events, events.Event{
			Kind:      events.KindRoleRemoved,
			Severity:  events.SeverityWarning,
			Workspace: t.Workspace,
			Team:      t.Name,
			Role:      role,
			Message:   fmt.Sprintf("role removed from manifest, drained %d session(s)", n),
		})
	}
}

// reconcileRole repairs a role at the team's current generation. Outside a
// shift that is the only generation in play; reconcileShift calls
// reconcileRoleAt instead, because mid-shift the team generation is
// aspirational.
func (c *Controller) reconcileRole(t *api.Team, role *api.Role) {
	c.reconcileRoleAt(t, role, t.Generation)
}

// shiftRepairGeneration returns the generation a mid-shift repair of role
// should carry. Roles the shift has already carried over belong to the new
// generation; every other role still belongs to the old one, because the
// team generation is aspirational until the shift completes and an abort
// may hand it back. Tagging every repair with the new generation is what
// left a rolled-back team holding sessions of a generation it no longer
// claims, undrainable because the abort deletes only the shifting role's.
// See aae-orc-d0pt, finding-010 defect 2.
func shiftRepairGeneration(t *api.Team, role string) int64 {
	shifted := t.Shift.RoleIndex
	if shifted > len(t.Shift.Roles) {
		shifted = len(t.Shift.Roles)
	}
	for _, r := range t.Shift.Roles[:shifted] {
		if r == role {
			return t.Generation
		}
	}
	return t.Shift.OldGeneration
}

func (c *Controller) reconcileRoleAt(t *api.Team, role *api.Role, generation int64) {
	// Counting uses all generations — generation scoping is only
	// for shift logic (shiftLaunch/shiftDrain). This ensures non-shifting roles
	// aren't disrupted when only one role shifts and the team generation advances.
	current := c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name)
	desired := role.Replicas
	actual := 0
	for _, sess := range current {
		if sess.State.CountsAsAlive() {
			actual++
		}
	}

	// An admission hold describes a refusal that is still happening. Drop it
	// as soon as the gate below is not reached at all — the role is
	// satisfied, the budget was removed from the manifest, or a shift took
	// over — so `admission.cleared` fires and Team.Admission stops naming a
	// condition that has passed.
	if actual >= desired || !t.Budget.Declared() || t.Shift.Phase != api.ShiftNone {
		c.clearAdmissionHold(t, role.Name)
	}

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
		// Admission backstop against a team-declared budget (aae-orc-qiay,
		// resource-matrix enforcement locus 2). The primary refusal point is
		// the operator's verb, where nothing has been committed yet; this
		// catches the state a manifest declaration cannot see, chiefly a
		// declared count that is itself over the ceiling after an
		// out-of-band write or two racing scale calls.
		//
		// Session-count only: the token clause is monotonic within a daemon
		// lifetime, so gating repair on it would make an over-budget team
		// permanently unrepairable (R2). Placed AFTER the backoff gate so a
		// cooling role emits no admission event (backoff is the older,
		// stronger condition), and BEFORE ClearCrashedForRole because that
		// call mutates store state: refusing after it would delete this
		// role's Crashed markers every tick while never spawning.
		//
		// Skipped entirely while a shift is in progress, so a launching
		// generation's transient double count cannot refuse a non-shifting
		// role's legitimate repair (R5).
		if t.Budget.Declared() && t.Shift.Phase == api.ShiftNone {
			granted := c.admit(t, role, desired-actual)
			if granted <= 0 {
				return
			}
			desired = actual + granted
		}
		// Crash markers from the reap path have done their observability
		// job by now (operators saw them during the backoff window). The
		// fresh session is the new truth — clear stale Crashed markers
		// for this role so nextIndex computes against live sessions only
		// and `marvel get sessions` doesn't carry the ghost forward.
		c.sessMgr.ClearCrashedForRole(t.Workspace, t.Name, role.Name)
		for i := actual; i < desired; i++ {
			name := fmt.Sprintf("%s-%s-g%d-%d", t.Name, role.Name, generation, c.nextIndex(t, role, generation))
			sess := &api.Session{
				Name:       name,
				Workspace:  t.Workspace,
				Team:       t.Name,
				Role:       role.Name,
				Generation: generation,
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
//
// Per orc finding-032, all mutation to session health state is routed
// through the Store via UpdateSession. The closure body holds the write
// lock, so the mutation is atomic relative to daemon reads and other
// writers. The outer loop works off value snapshots — safe to iterate
// while the closure mutates the live copy.
func (c *Controller) evaluateHealth() {
	now := time.Now().UTC()
	sessions := c.store.ListSessions()

	// Build a lookup cache: workspace/team → team (value snapshot).
	teamCache := make(map[string]api.Team)
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

		var (
			transitionedToUnhealthy bool
			shouldApplyRestart      bool
		)
		var updated api.Session

		err := c.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			if role == nil || role.HealthCheck == nil {
				live.HealthState = api.HealthUnknown
				updated = *live
				return nil
			}
			if role.HealthCheck.Type != api.HealthCheckHeartbeat {
				// process-alive is handled by ReapDead. Pane exists → healthy.
				live.HealthState = api.HealthHealthy
				live.FailureCount = 0
				updated = *live
				return nil
			}

			// Heartbeat staleness check.
			live.LastHealthCheck = now

			if live.LastHeartbeat.IsZero() {
				// Grace period: allow timeout from creation for first heartbeat.
				if now.Sub(live.CreatedAt) < role.HealthCheck.Timeout {
					live.HealthState = api.HealthUnknown
					updated = *live
					return nil
				}
				live.FailureCount++
			} else if now.Sub(live.LastHeartbeat) > role.HealthCheck.Timeout {
				live.FailureCount++
			} else {
				live.FailureCount = 0
				live.HealthState = api.HealthHealthy
				updated = *live
				return nil
			}

			if live.FailureCount >= role.HealthCheck.FailureThreshold {
				if live.HealthState != api.HealthUnhealthy {
					transitionedToUnhealthy = true
				}
				live.HealthState = api.HealthUnhealthy
				shouldApplyRestart = true
			} else {
				live.HealthState = api.HealthUnhealthy
			}
			updated = *live
			return nil
		})
		if err != nil {
			// Session disappeared between snapshot and update — fine,
			// another tick will handle whatever's next.
			continue
		}

		if transitionedToUnhealthy {
			events.Emit(c.Events, events.Event{
				Kind:      events.KindHealthCheckFailed,
				Severity:  events.SeverityWarning,
				Workspace: updated.Workspace,
				Team:      updated.Team,
				Role:      updated.Role,
				Session:   updated.Key(),
				Message:   fmt.Sprintf("heartbeat stale %d/%d failures", updated.FailureCount, role.HealthCheck.FailureThreshold),
			})
		}
		if shouldApplyRestart {
			c.applyRestartPolicy(&updated, &t, role)
		}
	}
}

func (c *Controller) applyRestartPolicy(sess *api.Session, t *api.Team, role *api.Role) {
	switch role.RestartPolicy {
	case api.RestartNever:
		_ = c.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.State = api.SessionFailed
			return nil
		})
		sess.State = api.SessionFailed
		// never means the role stops. Without the freeze, SessionFailed
		// drops out of CountsAsAlive and the reconciler replaces the
		// session every tick, uncapped and with no backoff — one live
		// pane leaked per cycle (marvel#107, aae-orc-pyre).
		c.freezeRole(t.Workspace, t.Name, role.Name)
		log.Printf("health: session %s failed (restart_policy=never, failures=%d), role frozen",
			sess.Key(), sess.FailureCount)
		events.Emit(c.Events, events.Event{
			Kind:      events.KindSessionFailed,
			Severity:  events.SeverityWarning,
			Workspace: t.Workspace,
			Team:      t.Name,
			Role:      role.Name,
			Session:   sess.Key(),
			Message:   fmt.Sprintf("restart_policy=never, failures=%d", sess.FailureCount),
		})
	case api.RestartOnFailure:
		if sess.State == api.SessionFailed {
			c.restartSession(sess, t, role)
		} else {
			_ = c.store.UpdateSession(sess.Key(), func(live *api.Session) error {
				live.State = api.SessionFailed
				return nil
			})
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
		if sess.State != api.SessionCrashLoopBackOff {
			events.Emit(c.Events, events.Event{
				Kind:      events.KindCrashLoopBackoff,
				Severity:  events.SeverityWarning,
				Workspace: t.Workspace,
				Team:      t.Name,
				Role:      role.Name,
				Session:   sess.Key(),
				Message:   fmt.Sprintf("cooling down, backoff until %s", rh.BackoffUntil.Format(time.RFC3339)),
			})
		}
		_ = c.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.State = api.SessionCrashLoopBackOff
			return nil
		})
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
			events.Emit(c.Events, events.Event{
				Kind:      events.KindRoleSaturated,
				Severity:  events.SeverityWarning,
				Workspace: t.Workspace,
				Team:      t.Name,
				Role:      role.Name,
				Session:   sess.Key(),
				Message:   fmt.Sprintf("max_restarts=%d reached", role.MaxRestarts),
			})
			events.Emit(c.Events, events.Event{
				Kind:      events.KindSessionFailed,
				Severity:  events.SeverityWarning,
				Workspace: t.Workspace,
				Team:      t.Name,
				Role:      role.Name,
				Session:   sess.Key(),
				Message:   fmt.Sprintf("max_restarts=%d reached, not restarting", role.MaxRestarts),
			})
		}
		_ = c.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.State = api.SessionFailed
			return nil
		})
		sess.State = api.SessionFailed
		return
	}

	log.Printf("health: restarting session %s (role %s restart #%d, next backoff=%s)",
		sess.Key(), roleKey, rh.RestartCount, time.Until(rh.BackoffUntil))
	events.Emit(c.Events, events.Event{
		Kind:      events.KindSessionRestarted,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role.Name,
		Session:   sess.Key(),
		Message:   fmt.Sprintf("restart #%d, next backoff %s", rh.RestartCount, time.Until(rh.BackoffUntil)),
	})
	_ = c.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.RestartCount++
		live.State = api.SessionFailed
		return nil
	})
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

	// Admission, before any shift state is written. See admitShift for why
	// the gate is here and not in shiftLaunch.
	if t.Budget.Declared() {
		if v := c.admitShift(&t, roles); v.Refused() {
			reason := v.Reason(admission.TriggerShift)
			log.Printf("admission: %s shift refused: %s", teamKey, reason)
			events.Emit(c.Events, events.Event{
				Kind:      events.KindAdmissionRefused,
				Severity:  events.SeverityWarning,
				Workspace: t.Workspace,
				Team:      t.Name,
				Message:   reason,
			})
			return fmt.Errorf("team %s: %s", teamKey, reason)
		}
	}

	oldGen := t.Generation
	newGen := oldGen + 1
	if err := c.store.UpdateTeam(teamKey, func(live *api.Team) error {
		// Re-check inside the lock — another caller could have started
		// a shift between our snapshot and this mutation.
		if live.Shift.Phase != api.ShiftNone {
			return fmt.Errorf("team %s: shift already in progress (phase: %s)", teamKey, live.Shift.Phase)
		}
		live.Generation = newGen
		live.Shift = api.ShiftState{
			Phase:         api.ShiftLaunching,
			OldGeneration: oldGen,
			RoleIndex:     0,
			Roles:         roles,
			StartedAt:     c.nowUTC(),
		}
		return nil
	}); err != nil {
		return err
	}

	log.Printf("shift: initiated for %s gen %d→%d roles=%v", teamKey, oldGen, newGen, roles)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindShiftStarted,
		Workspace: t.Workspace,
		Team:      t.Name,
		Message:   fmt.Sprintf("gen %d→%d roles=%v", oldGen, newGen, roles),
	})
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
	// Shift timeout: bound how long a shift may run. A launch whose new
	// generation never reaches readiness would otherwise keep the team in
	// phase=launching forever, both generations running and scale refused
	// (ShiftState.StartedAt was written and read by nothing). On expiry we
	// abort the shift; see abortStuckShift for the rollback rationale.
	// See aae-orc-qkfl.
	if !t.Shift.StartedAt.IsZero() && c.nowUTC().Sub(t.Shift.StartedAt) > c.shiftTimeout() {
		c.abortStuckShift(t)
		return
	}

	if t.Shift.RoleIndex >= len(t.Shift.Roles) {
		// All roles shifted — complete.
		log.Printf("shift: complete for %s/%s", t.Workspace, t.Name)
		events.Emit(c.Events, events.Event{
			Kind:      events.KindShiftCompleted,
			Workspace: t.Workspace,
			Team:      t.Name,
			Message:   fmt.Sprintf("gen %d active", t.Generation),
		})
		_ = c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
			live.Shift = api.ShiftState{}
			return nil
		})
		t.Shift = api.ShiftState{}
		return
	}

	shiftingRoleName := t.Shift.Roles[t.Shift.RoleIndex]

	// Reconcile non-shifting roles normally, each at the generation it
	// actually belongs to. See shiftRepairGeneration.
	for i := range t.Roles {
		if t.Roles[i].Name != shiftingRoleName {
			c.reconcileRoleAt(t, &t.Roles[i], shiftRepairGeneration(t, t.Roles[i].Name))
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
		_ = c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
			live.Shift.RoleIndex++
			return nil
		})
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

// abortStuckShift ends a shift that exceeded the timeout. Rolling the
// stuck generation back is the safer default than tearing down the
// working sessions: in the launching phase the new generation is the one
// that never came ready, so it is deleted and the known-good old
// generation is kept; in draining the sessions are left in place and
// normal reconciliation converges the replica counts. Either way the
// shift state is cleared, which resumes normal reconciliation and
// unblocks scale (the daemon refuses scale while a shift is in progress).
// An operator-visible warning names the phase and the state the abort
// left: the generation rolled back to, or the generation it stopped at
// and how far it got. Caller holds c.mu. See aae-orc-qkfl, aae-orc-d0pt.
func (c *Controller) abortStuckShift(t *api.Team) {
	var role string
	if t.Shift.RoleIndex < len(t.Shift.Roles) {
		role = t.Shift.Roles[t.Shift.RoleIndex]
	}
	// Rolling the counter back is only honest while nothing has drained.
	// At the first role's launch the pre-shift state is intact, so the
	// team goes back to the generation it came from. Once a drain has
	// happened the old generation's sessions are gone, and handing the
	// team back to it would name a generation that no longer exists; the
	// shift stops where it stands instead, and reconciliation converges
	// the roles that never got their turn. Generation is written in
	// exactly two places, InitiateShift and here; without this restore a
	// stuck shift leaked one permanently, and since the session key is
	// <team>-<role>-g<gen>-<index> the leak renamed every session the
	// team created afterwards. See aae-orc-d0pt, finding-010 defect 1.
	rollback := t.Shift.Phase == api.ShiftLaunching && t.Shift.RoleIndex == 0
	oldGen := t.Shift.OldGeneration
	elapsed := c.nowUTC().Sub(t.Shift.StartedAt)
	outcome := fmt.Sprintf("rolled back to gen %d", oldGen)
	if !rollback {
		outcome = fmt.Sprintf("stopped at gen %d with %d of %d roles shifted",
			t.Generation, t.Shift.RoleIndex, len(t.Shift.Roles))
	}
	log.Printf("shift: %s stuck in %s for %s (role %s), aborting, %s",
		t.Key(), t.Shift.Phase, elapsed, role, outcome)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindShiftTimedOut,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role,
		Message:   fmt.Sprintf("shift stuck in %s past %s, %s", t.Shift.Phase, c.shiftTimeout(), outcome),
	})
	if t.Shift.Phase == api.ShiftLaunching && role != "" {
		for _, sess := range c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role, t.Generation) {
			if err := c.sessMgr.Delete(sess.Key()); err != nil {
				log.Printf("shift: abort delete new-gen session %s: %v", sess.Key(), err)
			}
		}
	}
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		if rollback {
			live.Generation = oldGen
		}
		live.Shift = api.ShiftState{}
		return nil
	}); err != nil {
		log.Printf("shift: abort clear shift state for %s: %v", t.Key(), err)
	}
	if rollback {
		t.Generation = oldGen
	}
	t.Shift = api.ShiftState{}
}

func (c *Controller) shiftLaunch(t *api.Team, role *api.Role) {
	// Count only LIVE new-gen rows against the replica count, the same
	// predicate reconcileRoleAt uses. The store query stays all-states
	// (three of its five callers depend on that; see nextIndex, shiftDrain,
	// abortStuckShift), so the live filter belongs at this call site: a
	// Failed or Crashed successor must not satisfy the launch and suppress
	// the live replacement that should take its place. See aae-orc-6kgq.
	live := aliveSessions(c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role.Name, t.Generation))
	desired := role.Replicas

	if len(live) < desired {
		// Create remaining new-gen sessions. nextIndex counts all states,
		// so a dead row keeps its index and the replacement gets a fresh,
		// non-colliding one.
		for i := len(live); i < desired; i++ {
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

	// All new-gen sessions created — check readiness, then transition to
	// draining. Filter to live rows again so a leftover dead row neither
	// counts toward "launched" nor fails allReady and holds the shift in
	// launching forever.
	live = aliveSessions(c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role.Name, t.Generation))
	newGen := live
	if len(newGen) >= desired {
		if c.allReady(newGen, role) {
			log.Printf("shift: %s/%s role %s — %d new sessions ready, draining old gen %d",
				t.Workspace, t.Name, role.Name, len(newGen), t.Shift.OldGeneration)
			// Record the phase flip before announcing it. A failed update
			// leaves the team in launching, so allReady is consulted again
			// next tick, and an event emitted ahead of the write would then
			// be a readiness stamp for a transition that did not happen.
			// Emitting after the write keeps the ring's count of
			// KindShiftRoleReady equal to the number of transitions.
			if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
				live.Shift.Phase = api.ShiftDraining
				return nil
			}); err != nil {
				log.Printf("shift: %s advance role %s to draining: %v", t.Key(), role.Name, err)
				return
			}
			t.Shift.Phase = api.ShiftDraining
			events.Emit(c.Events, events.Event{
				Kind:       events.KindShiftRoleReady,
				Workspace:  t.Workspace,
				Team:       t.Name,
				Role:       role.Name,
				Generation: t.Generation,
				Message: fmt.Sprintf("gen %d ready, gate=%s, draining gen %d: %s",
					t.Generation, readinessGate(role), t.Shift.OldGeneration,
					strings.Join(sessionKeys(newGen), " ")),
			})
		} else {
			log.Printf("shift: %s/%s role %s — %d sessions launched, waiting for readiness",
				t.Workspace, t.Name, role.Name, len(newGen))
		}
	}
}

// readinessGate names the rule allReady applied, so a reader of the
// readiness event can tell a heartbeat-gated successor from one admitted on
// pane existence alone. The distinction matters for anything reasoning about
// shift latency: pane-Running fires within about 100 ms of spawn and says
// nothing about whether the harness inside can work yet.
func readinessGate(role *api.Role) string {
	if role.HealthCheck == nil {
		return "running"
	}
	return string(role.HealthCheck.Type)
}

// aliveSessions returns the subset of sessions the reconciler counts as
// alive (pending, running, crashloop-backoff), preserving order. It is the
// shift path's slice-shaped companion to api.CountAlive: shiftLaunch needs
// both the count and the filtered slice (to feed allReady), so it filters
// once here rather than counting and filtering separately.
func aliveSessions(sessions []api.Session) []api.Session {
	out := make([]api.Session, 0, len(sessions))
	for i := range sessions {
		if sessions[i].State.CountsAsAlive() {
			out = append(out, sessions[i])
		}
	}
	return out
}

// sessionKeys returns the session keys in order, for event messages.
func sessionKeys(sessions []api.Session) []string {
	keys := make([]string, 0, len(sessions))
	for i := range sessions {
		keys = append(keys, sessions[i].Key())
	}
	return keys
}

// allReady returns true if all sessions are ready to take over.
// For roles without a healthcheck, pane existence (Running state) is sufficient.
// For heartbeat-based checks, at least one heartbeat must have been received.
func (c *Controller) allReady(sessions []api.Session, role *api.Role) bool {
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
		_ = c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
			live.Shift.RoleIndex++
			if live.Shift.RoleIndex < len(live.Shift.Roles) {
				live.Shift.Phase = api.ShiftLaunching
			}
			return nil
		})
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
