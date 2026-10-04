package daemon

import (
	"time"

	"github.com/arcavenae/marvel/internal/config"

	"github.com/arcavenae/marvel/internal/api"
)

// teamDescription is describe team's result: the stored team, with its
// fields unchanged, plus a schedule block per scheduled role
// (scheduled-runs section 5). Schedules is omitted for a team with no
// scheduled role, so its output is what it was before.
type teamDescription struct {
	api.Team
	Schedules []scheduleDescription `json:"Schedules,omitempty"`
	// Conditions are standing conditions of the team's roles, such as a spawn
	// refused for its directory. Omitted when there are none.
	Conditions []roleCondition `json:"Conditions,omitempty"`
}

// roleCondition is one standing condition on a role, in the shape of a
// kubernetes condition: a type, a status, a message, and since when.
type roleCondition struct {
	Role    string    `json:"role"`
	Type    string    `json:"type"`
	Status  string    `json:"status"`
	Message string    `json:"message"`
	Since   time.Time `json:"since,omitzero"`
}

// scheduleDescription is one scheduled role's block. NextDue stays null
// until the schedule clock computes it; Held says why.
type scheduleDescription struct {
	Role            string           `json:"role"`
	Cron            string           `json:"cron"`
	Timezone        string           `json:"timezone"`
	StaleAfter      string           `json:"stale_after"`
	NextDue         *time.Time       `json:"next_due"`
	Held            string           `json:"held,omitempty"`
	Since           time.Time        `json:"since,omitzero"`
	LastSucceededAt time.Time        `json:"last_succeeded_at,omitzero"`
	Stale           bool             `json:"stale"`
	Runs            []runDescription `json:"runs"`
}

// runDescription is a run record as describe shows it: the record,
// result text included, plus its duration.
type runDescription struct {
	api.RunRecord
	Duration string `json:"duration"`
}

// scheduleHeld matches the plan's HoldSchedule: no firing is ever due yet.
const scheduleHeld = "held: the schedule does not fire yet; no firing is computed"

func (d *Daemon) describeTeam(key string) (teamDescription, error) {
	t, err := d.store.GetTeam(key)
	if err != nil {
		return teamDescription{}, err
	}
	out := teamDescription{Team: t}
	for _, r := range d.sessMgr.PlacementRefusals() {
		if r.Workspace == t.Workspace && r.Team == t.Name {
			out.Conditions = append(out.Conditions, roleCondition{
				Role: r.Role, Type: "PlacementRefused", Status: "True", Message: r.Message, Since: r.Since,
			})
		}
	}
	for _, role := range t.Roles {
		if role.Schedule == nil {
			continue
		}
		s := scheduleDescription{
			Role:       role.Name,
			Cron:       role.Schedule.Cron,
			Timezone:   role.Schedule.Timezone,
			StaleAfter: role.Schedule.StaleAfter.String(),
			Held:       scheduleHeld,
			Runs:       []runDescription{},
		}
		if st, ok := d.store.GetScheduleStatus(t.Key() + "/" + role.Name); ok {
			s.Since = st.Since
			s.LastSucceededAt = st.LastSucceededAt
			s.Stale = st.Stale
			// Newest first: the run an operator asks about is the last one.
			for i := len(st.History) - 1; i >= 0; i-- {
				r := st.History[i]
				s.Runs = append(s.Runs, runDescription{
					RunRecord: r,
					Duration:  r.Duration().String(),
				})
			}
		}
		out.Schedules = append(out.Schedules, s)
	}
	return out, nil
}

// scheduleHistoryMaxFrom reads the cluster's ceiling on schedule.history.
func scheduleHistoryMaxFrom(cl *config.Cluster) int { return 0 }
