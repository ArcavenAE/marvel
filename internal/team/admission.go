package team

import (
	"fmt"
	"log"
	"strings"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// admit evaluates the count clause for one role and returns how many of
// the requested spawns may proceed. Emitting and latching are side effects,
// so the caller only has to read the number.
//
// Never touches RoleHealth in either direction. A refusal is not a crash,
// and routing one through noteCrashAndBackoff would climb RestartCount
// every tick until MaxRestarts saturation froze BackoffUntil in the year
// 9999 — durably, in the bolt role_health bucket, surviving restarts. A
// role held by admission accrues no restart count, so a hold cannot age
// into a saturation freeze. Caller holds c.mu. See aae-orc-qiay.
func (c *Controller) admit(t *api.Team, role *api.Role, want int) int {
	live := api.CountAlive(c.store.ListSessionsByTeam(t.Workspace, t.Name))
	// Recomputed per role rather than once per team: spawning for an earlier
	// role changes the count a later role must be evaluated against, and
	// computing it once would over-admit within a single tick.
	v := admission.CheckSessions(t.Budget, live, c.declaredSessions(t), admission.Request{
		Role: role.Name,
		Want: want,
		// The reconciler only ever converges toward an already-declared
		// replica count. The parser guarantees declared <= ceiling, so this
		// path is admitted unless the declaration itself is over budget.
		Kind: admission.Repair,
		// Convergence is best-effort: 2 of 5 is strictly better for the
		// operator than 0 of 5. The synchronous verbs do the opposite.
		AllowPartial: true,
	})
	if !v.Refused() {
		c.clearAdmissionHold(t, role.Name)
		return v.Granted
	}

	roleKey := t.Workspace + "/" + t.Name + "/" + role.Name
	key := v.Key()
	if c.admissionHolds[roleKey] == key {
		return v.Granted
	}
	c.admissionHolds[roleKey] = key
	reason := v.Reason(admission.TriggerReconcile)
	// Tee to the log ring as well as the event ring: the event ring is
	// bounded and in-memory, and `marvel daemon logs` works over mrvl://.
	log.Printf("admission: %s role %s refused: %s", t.Key(), role.Name, reason)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindAdmissionRefused,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role.Name,
		Message:   reason,
	})
	c.setAdmissionState(t, api.AdmissionState{
		Held:   true,
		Role:   role.Name,
		Reason: reason,
		Since:  c.nowUTC(),
	})
	return v.Granted
}

// clearAdmissionHold drops a role's latch and says so once. A hold
// describes a refusal that is still happening, so it is dropped the moment
// the gate admits or stops being reached at all. Cheap when no hold exists:
// one map lookup, no store read, no event. Caller holds c.mu.
func (c *Controller) clearAdmissionHold(t *api.Team, role string) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	if _, held := c.admissionHolds[roleKey]; !held {
		return
	}
	delete(c.admissionHolds, roleKey)
	live := api.CountAlive(c.store.ListSessionsByTeam(t.Workspace, t.Name))
	msg := fmt.Sprintf("role %s may grow again: %d live", role, live)
	if t.Budget.MaxSessions > 0 {
		msg = fmt.Sprintf("role %s may grow again: %d live under max_sessions=%d", role, live, t.Budget.MaxSessions)
	}
	log.Printf("admission: %s %s", t.Key(), msg)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindAdmissionCleared,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role,
		Message:   msg,
	})
	if t.Admission.Role == role {
		c.setAdmissionState(t, api.AdmissionState{})
	}
}

// reconcileAdmissionState drops a recorded condition this process never
// refused.
//
// The latch is in-memory because the condition is derived from live state, so
// a durable copy could only outlive its cause: Team.Admission rides the team
// record into bolt, and a daemon restart would otherwise rehydrate a hold
// nothing is holding. Correcting it here, before any role is reconciled,
// means the record is right within one tick whatever else that tick decides
// (including a role sitting in a crash-loop backoff window, which returns
// before admission is reached at all). Silent on purpose: announcing a
// clearing with no matching refusal in the event ring would be noise.
// Caller holds c.mu.
func (c *Controller) reconcileAdmissionState(t *api.Team) {
	if !t.Admission.Held {
		return
	}
	if _, held := c.admissionHolds[t.Workspace+"/"+t.Name+"/"+t.Admission.Role]; held {
		return
	}
	c.setAdmissionState(t, api.AdmissionState{})
}

// setAdmissionState writes the standing condition through to the store, on
// transitions only. Following the Team.Shift precedent rather than
// inventing a second pattern for status on a spec record; writing every
// tick would be a bolt write storm. Caller holds c.mu.
func (c *Controller) setAdmissionState(t *api.Team, st api.AdmissionState) {
	if t.Admission == st {
		return
	}
	t.Admission = st
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		live.Admission = st
		return nil
	}); err != nil {
		log.Printf("admission: record state for %s: %v", t.Key(), err)
	}
}

// declaredSessions sums Replicas across a team's roles. Caller holds c.mu.
func (c *Controller) declaredSessions(t *api.Team) int {
	n := 0
	for i := range t.Roles {
		n += t.Roles[i].Replicas
	}
	return n
}

// dropAdmissionHolds forgets every latch under a key prefix, paired with
// the crash-loop cascade clears. A hold outliving the team or role it
// describes would suppress the first event of a genuinely new refusal.
// Caller holds c.mu.
func (c *Controller) dropAdmissionHolds(prefix string) {
	for k := range c.admissionHolds {
		if strings.HasPrefix(k, prefix) {
			delete(c.admissionHolds, k)
		}
	}
}

// AdmissionHold returns a role's latched verdict key, for tests and for
// operator-facing diagnostics. Returns ("", false) when the role is not
// held.
func (c *Controller) AdmissionHold(workspace, team, role string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key, ok := c.admissionHolds[workspace+"/"+team+"/"+role]
	return key, ok
}

// admitShift evaluates whether a rotation may start.
//
// The request carries Overlap, so the count clause is skipped: a shift is
// replacement, and its transient double count is a mechanism artifact
// rather than growth (R5). A budget exactly equal to declared replicas
// therefore does not forbid a rolling shift. A cumulative clause is NOT
// skipped, because a new generation is a new spender, so an exhausted token
// budget can refuse a rotation — synchronously, at the verb, where the
// operator can raise the ceiling in one command.
//
// Gating here rather than inside shiftLaunch is deliberate. Refusing at
// launch would return early and leave the team in phase=launching until
// abortStuckShift fires at the shift timeout, so the operator would see a
// shift-timeout warning instead of a budget one, ten minutes late. Caller
// holds c.mu.
func (c *Controller) admitShift(t *api.Team, roles []string) admission.Verdict {
	want := 0
	for _, name := range roles {
		for i := range t.Roles {
			if t.Roles[i].Name == name {
				want += t.Roles[i].Replicas
			}
		}
	}
	req := admission.Request{Want: want, Kind: admission.Growth, Overlap: true}
	if c.Snapshots == nil {
		live := api.CountAlive(c.store.ListSessionsByTeam(t.Workspace, t.Name))
		return admission.CheckSessions(t.Budget, live, c.declaredSessions(t), req)
	}
	return admission.Check(t.Budget, c.Snapshots.AdmissionSnapshot(*t), req)
}
