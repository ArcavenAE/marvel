package team

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// reconcileCadenceClock is the slot clock for interactive roles' declared
// cadences (docs/design/declared-cadence.md V2). For each declared cadence it
// keeps the next due time and, when one arrives, writes a slot record: open,
// or skipped. It opens slot records and nothing else: it never spawns, types or
// claims. It runs right after the schedule clock on the same team list.
//
// The status of a cadence that is no longer declared, of a role that is gone
// and of a deleted team is dropped, so no stale open slot is left for a claim
// to serve.
func (c *Controller) reconcileCadenceClock(teams []api.Team) {
	now := c.nowUTC()
	declared := make(map[string]bool)
	for i := range teams {
		t := &teams[i]
		withheld := c.postureWithholds(t)
		for j := range t.Roles {
			role := &t.Roles[j]
			for _, cad := range role.Cadence {
				key := t.Workspace + "/" + t.Name + "/" + role.Name + "/" + cad.Name
				declared[key] = true
				c.stepCadenceClock(t, role, cad, key, now, withheld)
			}
		}
	}
	for _, st := range c.store.ListCadenceStatus() {
		if declared[st.Key] {
			continue
		}
		if err := c.store.DeleteCadenceStatus(st.Key); err != nil {
			log.Printf("warning: drop cadence status %s: %v", st.Key, err)
		}
	}
}

func (c *Controller) stepCadenceClock(t *api.Team, role *api.Role, cad api.Cadence, key string, now time.Time, withheld bool) {
	var step api.CadenceStep
	var stepErr error
	if _, err := c.store.UpdateCadenceStatus(key, func(st *api.CadenceStatus) bool {
		step, stepErr = st.Step(cad, now, withheld)
		return step.Changed
	}); err != nil {
		log.Printf("warning: cadence clock %s: %v", key, err)
		return
	}
	if stepErr != nil {
		log.Printf("warning: cadence clock %s: %v", key, stepErr)
		return
	}
	emit := func(kind events.Kind, sev events.Severity, msg string) {
		events.Emit(c.Events, events.Event{
			Kind: kind, Severity: sev,
			Workspace: t.Workspace, Team: t.Name, Role: role.Name,
			Message: msg,
		})
	}
	switch {
	case step.Opened != nil:
		s := step.Opened
		msg := fmt.Sprintf("cadence %s: slot %s, due %s", cad.Name, s.ID, s.DueAt.Format(time.RFC3339))
		var detail []string
		if s.CatchUp {
			detail = append(detail, "catch-up")
		}
		if s.Missed > 0 {
			detail = append(detail, fmt.Sprintf("missed %d", s.Missed))
		}
		if len(detail) > 0 {
			msg += " (" + strings.Join(detail, ", ") + ")"
		}
		emit(events.KindCadenceSlotOpened, events.SeverityInfo, msg)
	case step.Skipped != nil:
		s := step.Skipped
		why := "the newest due time was past starting_deadline " + cad.StartingDeadline.String()
		if s.Reason == api.CadenceSkipPosture {
			why = "the team's convergence posture is hold and nothing in it is live; marvel converge starts it"
		}
		emit(events.KindCadenceSlotSkipped, events.SeverityWarning,
			fmt.Sprintf("cadence %s: skipped slot %s, due %s, covering %d due time(s): %s", cad.Name, s.ID, s.DueAt.Format(time.RFC3339), s.Missed, why))
	}
}
