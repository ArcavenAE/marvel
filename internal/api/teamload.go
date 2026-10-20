package api

import (
	"sort"
	"time"
)

// TeamLoadValidity is how long a team's load row stays fresh after the tick
// that computed it. The row moves every reconcile tick, so one much older than
// a few ticks means the daemon stopped computing it.
const TeamLoadValidity = 30 * time.Second

// TeamLoad is one team's load on the cluster that owns it: seats declared and
// running, the mean ACTIVE% over the seats that have a reading, seats limited,
// and the live agent names (docs/design/team-load-rollup.md section 2). It is
// diagnostic only; no value gates a spawn, a merge, a scale or a shift.
type TeamLoad struct {
	Workspace string `json:"workspace"`
	Team      string `json:"team"`
	// Declared is the sum of the team's role replicas; Running is the seats
	// whose session is running.
	Declared int `json:"declared"`
	Running  int `json:"running"`
	// ActiveMeanPct is the mean ACTIVE% over the ActiveMeasured seats that
	// have a reading. With ActiveMeasured zero it holds nothing, and a reader
	// prints a dash, never 0%.
	ActiveMeanPct  float64 `json:"active_mean_pct"`
	ActiveMeasured int     `json:"active_measured"`
	// Limited counts the running seats whose account is at a rate limit.
	Limited int `json:"limited"`
	// Agents are the running seats' session names, sorted.
	Agents []string `json:"agents,omitempty"`
	// ObservedAt is the tick the row was computed on; ValidUntil is when it
	// stops being fresh.
	ObservedAt time.Time `json:"observed_at"`
	ValidUntil time.Time `json:"valid_until"`
}

// RollTeamLoad computes one team's row from its sessions, which must carry the
// ActiveTicks stamp. Sessions of other teams are ignored.
func RollTeamLoad(t Team, sessions []Session, now time.Time) TeamLoad {
	row := TeamLoad{
		Workspace:  t.Workspace,
		Team:       t.Name,
		ObservedAt: now,
		ValidUntil: now.Add(TeamLoadValidity),
	}
	for _, r := range t.Roles {
		row.Declared += r.Replicas
	}
	var sum float64
	for _, s := range sessions {
		if s.Workspace != t.Workspace || s.Team != t.Name || s.State != SessionRunning {
			continue
		}
		row.Running++
		row.Agents = append(row.Agents, s.Name)
		if s.Condition == ConditionLimited {
			row.Limited++
		}
		// The same gate the ACTIVE% cell uses. A seat with a statusline feed
		// counts at its API-time reading, and has none while that is pending
		// or stale (provisional, cost_samples.go). Any other seat: no activity
		// channel, one that has never reported, or a ring that has not watched
		// the whole window, has no reading.
		if pct, kind := APIActivePct(s, now); kind != APIActivityNone {
			if kind == APIActivityReading {
				sum += pct
				row.ActiveMeasured++
			}
		} else if tk := s.ActiveTicks; tk.Observable && tk.Full && tk.Total > 0 && !s.ContextAt.IsZero() {
			sum += 100 * float64(tk.Active) / float64(tk.Total)
			row.ActiveMeasured++
		}
	}
	if row.ActiveMeasured > 0 {
		row.ActiveMeanPct = sum / float64(row.ActiveMeasured)
	}
	sort.Strings(row.Agents)
	return row
}
