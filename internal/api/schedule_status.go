package api

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// RunOutcome is how one run of a scheduled role ended.
type RunOutcome string

const (
	RunSucceeded RunOutcome = "succeeded"
	RunFailed    RunOutcome = "failed"
	RunCancelled RunOutcome = "cancelled"
)

// maxRunResultBytes caps the result text a run record keeps (design
// section 5).
const maxRunResultBytes = 4 << 10

// RunRecord is one finished run of a scheduled role (scheduled-runs
// section 5). Firing, DueAt and CatchUp belong to the schedule clock and
// stay empty until it sets them.
type RunRecord struct {
	Session   string    `json:"session"`
	Firing    string    `json:"firing,omitempty"`
	DueAt     time.Time `json:"due_at,omitzero"`
	CatchUp   bool      `json:"catch_up,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
	// EndedAt is when the reap path found the run's pane dead, not when
	// the process exited, so Duration includes up to one reconcile tick
	// of lag.
	EndedAt    time.Time  `json:"ended_at"`
	Outcome    RunOutcome `json:"outcome"`
	ExitStatus string     `json:"exit_status,omitempty"`
	Tokens     RunTokens  `json:"tokens"`
	// PermissionDenials counts the tool calls the harness refused, from
	// the stream's final result line. A denied call does not fail a run,
	// so this is the only place a denial shows in the record.
	PermissionDenials int `json:"permission_denials,omitempty"`
	// Result is the run's final message, cut to 4 KiB. describe team
	// shows it; events and reports carry status only (scheduled-runs
	// section 5).
	Result          string `json:"result,omitempty"`
	ResultTruncated bool   `json:"result_truncated"`
}

// RunTokens is a run's spend as the usage accountant metered it. Metered
// is false when the accountant never saw a request from the run, so a
// zero total that was never measured cannot read as a free run.
type RunTokens struct {
	Prompt       int     `json:"prompt"`
	Out          int     `json:"out"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	CostReported bool    `json:"cost_reported,omitempty"`
	Metered      bool    `json:"metered"`
}

// SetResult stores text as the run's result, cut to the cap on a rune
// boundary so the record never holds half a character.
func (r *RunRecord) SetResult(text string) {
	r.ResultTruncated = len(text) > maxRunResultBytes
	if !r.ResultTruncated {
		r.Result = text
		return
	}
	cut := maxRunResultBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	r.Result = text[:cut]
}

// Duration is how long the run took, zero when its start is unknown.
func (r RunRecord) Duration() time.Duration {
	if r.StartedAt.IsZero() || r.EndedAt.Before(r.StartedAt) {
		return 0
	}
	return r.EndedAt.Sub(r.StartedAt)
}

// ScheduleStatus is the status a scheduled role keeps beside its spec, the
// way RoleHealth sits beside a role: run history, the last success, and
// the freshness alarm's state. Key is workspace/team/role.
type ScheduleStatus struct {
	Key string `json:"key"`
	// Since is when marvel first recorded this schedule. Until a run
	// succeeds it is the freshness alarm's anchor.
	Since           time.Time `json:"since"`
	LastSucceededAt time.Time `json:"last_succeeded_at,omitzero"`
	Stale           bool      `json:"stale,omitempty"`
	StaleChangedAt  time.Time `json:"stale_changed_at,omitzero"`
	// History is oldest first, bounded per outcome by the role's
	// schedule.history.
	History []RunRecord `json:"history,omitempty"`

	// The clock (S-3). SpecKey is the cron and zone NextDueAt was computed
	// from, so a changed schedule is recomputed rather than fired late.
	SpecKey    string    `json:"spec_key,omitempty"`
	NextDueAt  time.Time `json:"next_due_at,omitzero"`
	NextFireAt time.Time `json:"next_fire_at,omitzero"`
	// The current firing: its id and nominal due time, whether it was a
	// recovery run and how many earlier due times it coalesced.
	Firing        string    `json:"firing,omitempty"`
	FiringDueAt   time.Time `json:"firing_due_at,omitzero"`
	FiringCatchUp bool      `json:"firing_catch_up,omitempty"`
	FiringMissed  int       `json:"firing_missed,omitempty"`
	// Attempts counts failed runs of the current firing; RetryAfter holds
	// a retry back; Settled means the firing spawns nothing more.
	Attempts   int       `json:"attempts,omitempty"`
	RetryAfter time.Time `json:"retry_after,omitzero"`
	Settled    bool      `json:"settled,omitempty"`
	// Frozen is on_failure = freeze having fired; reset-health clears it.
	Frozen    bool `json:"frozen,omitempty"`
	Suspended bool `json:"suspended,omitempty"`
}

// AddRun records a finished run, then drops the oldest runs of the run's
// class beyond that class's bound. Successes are one class, bounded by
// history.succeeded. Failed and cancelled runs are the other, sharing
// history.failed. So a streak of failures or kills cannot push the last
// success out.
func (st *ScheduleStatus) AddRun(r RunRecord, h ScheduleHistory) {
	if st.Since.IsZero() {
		st.Since = r.EndedAt
	}
	if r.Outcome == RunSucceeded && r.EndedAt.After(st.LastSucceededAt) {
		st.LastSucceededAt = r.EndedAt
	}
	st.History = append(st.History, r)

	succeeded := r.Outcome == RunSucceeded
	limit := h.Failed
	if succeeded {
		limit = h.Succeeded
	}
	excess := -limit
	for _, x := range st.History {
		if (x.Outcome == RunSucceeded) == succeeded {
			excess++
		}
	}
	if excess <= 0 {
		return
	}
	kept := st.History[:0]
	for _, x := range st.History {
		if (x.Outcome == RunSucceeded) == succeeded && excess > 0 {
			excess--
			continue
		}
		kept = append(kept, x)
	}
	st.History = kept
}

// EvaluateFreshness sets the stale state at now and reports whether it
// changed. A schedule is stale when no run has succeeded within
// staleAfter: of the last success, or of Since when none has. The first
// evaluation stamps Since.
func (st *ScheduleStatus) EvaluateFreshness(now time.Time, staleAfter time.Duration) bool {
	if st.Since.IsZero() {
		st.Since = now
	}
	anchor := st.LastSucceededAt
	if anchor.IsZero() {
		anchor = st.Since
	}
	stale := now.Sub(anchor) > staleAfter
	if stale == st.Stale {
		return false
	}
	st.Stale = stale
	st.StaleChangedAt = now
	return true
}

// HistoryBound is how many runs of each outcome the role keeps,
// defaulted for a record written before the field existed.
func (p SchedulePolicy) HistoryBound() ScheduleHistory {
	if p.History == nil {
		return ScheduleHistory{Succeeded: defaultScheduleHistorySucceeded, Failed: defaultScheduleHistoryFailed}
	}
	return *p.History
}

func cloneScheduleStatus(st *ScheduleStatus) ScheduleStatus {
	out := *st
	out.History = append([]RunRecord(nil), st.History...)
	return out
}

// GetScheduleStatus returns a copy of one role's schedule status.
func (s *Store) GetScheduleStatus(key string) (ScheduleStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.scheduleStatus[key]
	if !ok {
		return ScheduleStatus{}, false
	}
	return cloneScheduleStatus(st), true
}

// ListScheduleStatus returns a copy of every schedule status.
func (s *Store) ListScheduleStatus() []ScheduleStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ScheduleStatus, 0, len(s.scheduleStatus))
	for _, st := range s.scheduleStatus {
		out = append(out, cloneScheduleStatus(st))
	}
	return out
}

// UpdateScheduleStatus applies fn to one role's schedule status under the
// store lock, creating the record when it does not exist, and returns a
// copy of the result. fn reports whether it changed anything; only a
// change is written to disk, so the reconciler can evaluate every tick
// without a write per tick.
func (s *Store) UpdateScheduleStatus(key string, fn func(*ScheduleStatus) bool) (ScheduleStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.scheduleStatus[key]
	if !ok {
		st = &ScheduleStatus{Key: key}
	}
	work := cloneScheduleStatus(st)
	if !fn(&work) && ok {
		return cloneScheduleStatus(st), nil
	}
	work.Key = key
	if err := s.persistPut(bucketScheduleStatus, key, &work); err != nil {
		return cloneScheduleStatus(st), fmt.Errorf("persist schedule status %s: %w", key, err)
	}
	s.scheduleStatus[key] = &work
	return cloneScheduleStatus(&work), nil
}

// DeleteScheduleStatus removes one role's schedule status. Deleting a key
// that does not exist is not an error.
func (s *Store) DeleteScheduleStatus(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.scheduleStatus[key]; !ok {
		return nil
	}
	if err := s.persistDelete(bucketScheduleStatus, key); err != nil {
		return fmt.Errorf("delete schedule status %s: %w", key, err)
	}
	delete(s.scheduleStatus, key)
	return nil
}

// DefaultScheduleHistoryMax is the ceiling on schedule.history when the
// cluster sets no schedule_history_max. A role's whole status is one stored
// value rewritten on every run, and each run can carry a 4 KiB result.
const DefaultScheduleHistoryMax = 50

// ScheduleHistoryCap checks every scheduled role's history against the
// cluster's ceiling, limit, or DefaultScheduleHistoryMax when limit is not
// positive. The ceiling is the cluster's because it protects the daemon's
// store, not a team's choice, so it is checked at apply, not at parse.
func ScheduleHistoryCap(m *Manifest, limit int) error {
	if limit <= 0 {
		limit = DefaultScheduleHistoryMax
	}
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			if r.Schedule == nil || r.Schedule.History == nil {
				continue
			}
			if h := r.Schedule.History; h.Succeeded > limit || h.Failed > limit {
				return fmt.Errorf("team %s role %s: schedule.history keeps at most %d runs of each outcome (schedule_history_max on the cluster)", t.Name, r.Name, limit)
			}
		}
	}
	return nil
}
