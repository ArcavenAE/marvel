package usage

import "time"

// Occupancy is one session's context accounting.
//
// Limit == 0 means the denominator is unresolved and Percent is
// meaningless. The tri-state exists so a consumer cannot read "no
// denominator" as "0% used": orc finding-055 is the recorded instance of
// a wrong denominator badly misreporting, and an admission gate that
// reads unresolved as 0% admits everything.
type Occupancy struct {
	Tokens      int
	Limit       int
	Percent     float64
	LimitSource LimitSource
	Model       string
	Requests    int
	Compactions int
	// Peak is the high-water Percent, valid only when Limit > 0.
	Peak    float64
	FirstAt time.Time
	// ObservedAt is zero when the session was never observed.
	ObservedAt time.Time
}

// Spend accumulates. Unlike Occupancy it IS additive across requests.
type Spend struct {
	In              int
	Out             int
	CacheReadIn     int
	CacheCreationIn int
	ReasoningOut    int
	// PromptTokens is the layout-normalized prompt token count: the sum of
	// each request's own prompt size, In alone under a subsumptive layout
	// and In + the cache classes under an additive one.
	//
	// It is the only prompt figure a caller can add up without knowing each
	// harness's layout. The raw class fields are accumulated as the feed
	// reported them and Spend records no layout, so In + CacheReadIn +
	// CacheCreationIn double counts a subsumptive feed (codex) while In +
	// Out alone omits most of the input volume of an additive one (claude,
	// opencode). A budget summing the raw classes would therefore refuse a
	// codex team at roughly half its declared ceiling, silently.
	//
	// Includes subagent and non-primary-model samples, which are real spend
	// against other context windows even though they never enter occupancy.
	PromptTokens int
	CostUSD      float64
	// CostReported is false when the harness reports no cost at all
	// (codex publishes none in its exec stream), which is distinct from
	// reporting zero.
	CostReported bool
	Requests     int
	ObservedAt   time.Time
}

// TeamTotals is a team's accumulated spend, including sessions that have
// already exited.
type TeamTotals struct {
	Spend
	LiveSessions  int
	EndedSessions int
	Since         time.Time
	// Partial is true when any contributing session was never observed
	// (an interactive launch, a pane adopted from a prior daemon, an
	// unknown harness). A consumer refusing work against a budget must be
	// able to tell an incomplete total from a small one.
	Partial bool
}

// Reader is the read-only surface downstream consumers hold: admission
// control (refuse an over-budget spawn) and automatic shift triggers
// (rotate on context pressure). An interface so both can be tested
// against a fake with no Accountant.
//
// The accountant is a METER. It defines no threshold, no policy, and no
// refusal; a metered value becomes a gate only through a ratified written
// decision (ADR-007 clause 3, SOUL.md section 8).
type Reader interface {
	SessionOccupancy(agentID string) (Occupancy, bool)
	SessionSpend(agentID string) (Spend, bool)
	TeamSpend(workspace, team string) TeamTotals
}

// Baseline is the per-role history an admission check needs to price
// "what will K more of these cost".
//
// NOT IMPLEMENTED IN THIS SLICE. It is declared as the contract the
// admission-control work extends this package with, because the median
// machinery needs a retention and eviction policy this slice has no
// basis to choose. The Samples rule is the load-bearing part and must
// survive into that implementation: Samples == 0 means there is no
// history, and callers must not fabricate an estimate from nothing.
type Baseline struct {
	MedianRequestTokens  int
	MedianSessionTokens  int
	MedianSessionCostUSD float64
	MedianPeakPercent    float64
	Samples              int
}
