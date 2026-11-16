package team

import (
	"fmt"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// manualNotice is what a seat is sent when an operator shifts its role by hand
// and the role declares a handoff. Everything from "write your handoff now" on
// is built by prepareHandoffNotice, as for the automatic asks.
const manualNotice = "shift: an operator asked for a shift; write your handoff now"

// ShiftOpts are the operator's choices for one manual shift.
type ShiftOpts struct {
	// NoHandoff shifts without asking the departing seat for the handoff its
	// role declares. It is the operator's explicit decision to shift without a
	// watcher (docs/design/shift-trigger-list.md D5 point 3).
	NoHandoff bool
}

// InitiateShiftOpts starts a manual shift. A role that declares a handoff is
// asked for it first: the request is recorded on the team and the shift
// follows only on the marker, as for the automatic triggers, so marvel never
// shifts a seat unwatched. If the window ends without the marker the seat keeps
// running and team.shift-handoff-missing is emitted; running the shift again
// with opts.NoHandoff proceeds without a handoff. A shift of a role with no
// handoff keys is InitiateShift unchanged.
//
// An unscoped team shift that covers a role declaring a handoff is refused,
// naming each such role, unless opts.NoHandoff is set (marvel#1447).
func (c *Controller) InitiateShiftOpts(teamKey, role string, opts ShiftOpts) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if opts.NoHandoff {
		return c.initiateShiftLocked(teamKey, role)
	}
	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return fmt.Errorf("get team %s: %w", teamKey, err)
	}
	// initiateShiftLocked owns the refusals for a shift in progress, a pruning
	// team and an unknown role, so the paths below leave them to it.
	if t.Shift.Phase != api.ShiftNone || t.Pruning != nil {
		return c.initiateShiftLocked(teamKey, role)
	}

	if role == "" {
		var names []string
		for i := range t.Roles {
			if declaresHandoff(&t.Roles[i]) {
				names = append(names, t.Roles[i].Name)
			}
		}
		if len(names) > 0 {
			return fmt.Errorf("team %s: role %s declares a handoff, so a shift of the whole team would not ask for it; shift the role with --role <role> to ask for its handoff, or pass --no-handoff to shift without one",
				teamKey, strings.Join(names, ", "))
		}
		return c.initiateShiftLocked(teamKey, role)
	}

	var target *api.Role
	for i := range t.Roles {
		if t.Roles[i].Name == role {
			target = &t.Roles[i]
		}
	}
	if target == nil || !declaresHandoff(target) {
		return c.initiateShiftLocked(teamKey, role)
	}
	if req, ok := t.ShiftRequests[role]; ok {
		state := "waiting for its marker"
		if req.Escalated {
			state = "escalated: no marker arrived in the window"
		}
		return fmt.Errorf("team %s: role %s already has a handoff request for %s (%s); wait for it, or pass --no-handoff to shift without one",
			teamKey, role, req.Session, state)
	}
	live := withoutRetiring(&t, aliveSessions(c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role)))
	if len(live) == 0 {
		return c.initiateShiftLocked(teamKey, role) // no seat to ask
	}
	sortNewestFirst(live)
	seat := live[len(live)-1] // the oldest, the one the shift retires first
	c.requestHandoff(&t, target, api.ShiftCondition{On: api.HandoffCauseManual}, seat, handoffAsk{
		notice: manualNotice,
		origin: NoticeManual,
		detail: fmt.Sprintf("manual shift of role %s asked session %s", role, seat.Key()),
	}, c.nowUTC())
	if _, ok := t.ShiftRequests[role]; !ok {
		return fmt.Errorf("team %s: role %s: the handoff request was not recorded; see the daemon log", teamKey, role)
	}
	return nil
}

// declaresHandoff reports whether role names the file its seat hands off to.
func declaresHandoff(role *api.Role) bool {
	return role.Shift != nil && role.Shift.Handoff != ""
}
