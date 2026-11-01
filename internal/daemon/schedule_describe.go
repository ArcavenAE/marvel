package daemon

import (
	"maps"
	"slices"
	"time"

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
	// PendingShiftRequests are the roles' pending handoff requests with their
	// age and delivery count, so a request that never reached its seat or its
	// supervisor is seen here (docs/design/host-health-guard.md section 4.4).
	// Omitted when no role has one.
	PendingShiftRequests []shiftRequestDescription `json:"PendingShiftRequests,omitempty"`
}

// shiftRequestDescription is one pending handoff request as describe shows it.
// DeliveredCount is the escalation envelopes marvel has published to the
// team's supervisor for it; 0 on an escalated request is the stuck state.
type shiftRequestDescription struct {
	Role              string    `json:"role"`
	Session           string    `json:"session"`
	Cause             string    `json:"cause"`
	RequestedAt       time.Time `json:"requested_at"`
	Age               string    `json:"age"`
	Escalated         bool      `json:"escalated"`
	DeliveredCount    int       `json:"delivered_count"`
	LastDeliveredAt   time.Time `json:"last_delivered_at,omitzero"`
	NoticeUndelivered string    `json:"notice_undelivered,omitempty"`
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

// scheduleDescription is one scheduled role's block. NextDue is null until
// the clock first sees the schedule (one reconcile tick after apply).
type scheduleDescription struct {
	Role       string     `json:"role"`
	Cron       string     `json:"cron"`
	Timezone   string     `json:"timezone"`
	StaleAfter string     `json:"stale_after"`
	NextDue    *time.Time `json:"next_due"`
	// Firing is the current firing's id, which names its due time.
	Firing    string `json:"firing,omitempty"`
	Frozen    bool   `json:"frozen,omitempty"`
	Suspended bool   `json:"suspended,omitempty"`
	// DSTWarning is set while the zone observes daylight saving and the
	// schedule has no dst_ack (design 2a, a tzdata change after apply).
	DSTWarning      string           `json:"dst_warning,omitempty"`
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
	out.Conditions = append(out.Conditions, d.exposureConditions(t)...)
	now := time.Now()
	for _, role := range slices.Sorted(maps.Keys(t.ShiftRequests)) {
		req := t.ShiftRequests[role]
		out.PendingShiftRequests = append(out.PendingShiftRequests, shiftRequestDescription{
			Role: role, Session: req.Session, Cause: req.Cause, RequestedAt: req.RequestedAt,
			Age: now.Sub(req.RequestedAt).Truncate(time.Second).String(), Escalated: req.Escalated,
			DeliveredCount: req.DeliveredCount, LastDeliveredAt: req.LastDeliveredAt,
			NoticeUndelivered: req.NoticeUndelivered,
		})
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
			Runs:       []runDescription{},
		}
		if _, observes := api.ScheduleZoneFacts(role.Schedule.Timezone, time.Now().UTC(), time.Now().UTC()); observes && !role.Schedule.DSTAck {
			s.DSTWarning = "timezone " + role.Schedule.Timezone + " observes daylight saving and the schedule has no dst_ack"
		}
		if st, ok := d.store.GetScheduleStatus(t.Key() + "/" + role.Name); ok {
			s.Since = st.Since
			s.LastSucceededAt = st.LastSucceededAt
			s.Stale = st.Stale
			s.Firing = st.Firing
			s.Frozen = st.Frozen
			s.Suspended = st.Suspended
			if !st.NextDueAt.IsZero() {
				next := st.NextDueAt
				s.NextDue = &next
			}
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
