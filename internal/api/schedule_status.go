package api

import (
	"time"
)

// RunOutcome is how one run of a scheduled role ended.
type RunOutcome string

const (
	RunSucceeded RunOutcome = "succeeded"
	RunFailed    RunOutcome = "failed"
)

// maxRunResultBytes caps the result text a run record keeps (design
// section 5).
const maxRunResultBytes = 4 << 10

// RunRecord is one finished run of a scheduled role.
type RunRecord struct {
	Session    string     `json:"session"`
	Firing     string     `json:"firing,omitempty"`
	DueAt      time.Time  `json:"due_at,omitzero"`
	CatchUp    bool       `json:"catch_up,omitempty"`
	StartedAt  time.Time  `json:"started_at,omitzero"`
	EndedAt    time.Time  `json:"ended_at"`
	Outcome    RunOutcome `json:"outcome"`
	ExitStatus string     `json:"exit_status,omitempty"`
	Tokens     RunTokens  `json:"tokens"`
	// PermissionDenials counts the tool calls the harness refused, from
	// the stream's final result line.
	PermissionDenials int    `json:"permission_denials,omitempty"`
	Result            string `json:"result,omitempty"`
	ResultTruncated   bool   `json:"result_truncated,omitempty"`
}

// RunTokens is a run's spend as the usage accountant metered it.
type RunTokens struct {
	Prompt       int     `json:"prompt"`
	Out          int     `json:"out"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	CostReported bool    `json:"cost_reported,omitempty"`
	Metered      bool    `json:"metered"`
}

// SetResult stores text as the run's result.
func (r *RunRecord) SetResult(text string) {}

// Duration is how long the run took.
func (r RunRecord) Duration() time.Duration { return 0 }

// ScheduleStatus is the per-role record beside a scheduled role's spec.
type ScheduleStatus struct {
	Key             string      `json:"key"`
	Since           time.Time   `json:"since"`
	LastSucceededAt time.Time   `json:"last_succeeded_at,omitzero"`
	Stale           bool        `json:"stale,omitempty"`
	StaleChangedAt  time.Time   `json:"stale_changed_at,omitzero"`
	History         []RunRecord `json:"history,omitempty"`
}

// AddRun records a finished run.
func (st *ScheduleStatus) AddRun(r RunRecord, h ScheduleHistory) {}

// EvaluateFreshness reports whether the stale state changed at now.
func (st *ScheduleStatus) EvaluateFreshness(now time.Time, staleAfter time.Duration) bool {
	return false
}

// GetScheduleStatus returns a copy of one role's schedule status.
func (s *Store) GetScheduleStatus(key string) (ScheduleStatus, bool) {
	return ScheduleStatus{}, false
}

// ListScheduleStatus returns a copy of every schedule status.
func (s *Store) ListScheduleStatus() []ScheduleStatus { return nil }

// UpdateScheduleStatus applies fn to one role's schedule status.
func (s *Store) UpdateScheduleStatus(key string, fn func(*ScheduleStatus) bool) (ScheduleStatus, error) {
	return ScheduleStatus{}, nil
}

// DeleteScheduleStatus removes one role's schedule status.
func (s *Store) DeleteScheduleStatus(key string) error { return nil }
