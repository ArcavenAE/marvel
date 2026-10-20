package api

import (
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// Readiness routing (docs/design/seat-readiness.md section 6, ruled
// 2026-10-10 in section 9): which seats a job may be placed on, read from the
// duty.<name> and withdrawn.<duty> cells and nothing else. The rule is a pure
// function so that director and marvel route identically. The golden vectors
// in contracts/readiness/routing-vectors.json are the shared test.
//
// RouteSeat reads cells and a clock. It reads no count, rate or time
// threshold, so none of them can exclude a seat (ADR-007): the clock only
// decides whether a cell is fresh, and an expired cell asks first.

// RouteVerdict is the router's answer for a seat, or for one duty of it.
type RouteVerdict string

const (
	// RouteExcluded: off the free list, with the reason shown (rule 1).
	RouteExcluded RouteVerdict = "excluded"
	// RouteEligible: a fresh proven value for every duty the job needs (rule 2).
	RouteEligible RouteVerdict = "eligible"
	// RouteAskFirst: neither excluded nor proven; never assumed (rule 3).
	RouteAskFirst RouteVerdict = "ask-first"
)

// RouteReason is the code for a duty's verdict. It is a fixed word, never
// pane text or an error string.
type RouteReason string

const (
	ReasonWithdrawn      RouteReason = "withdrawn"       // withdrawn.<duty> is set
	ReasonStructuralFail RouteReason = "structural-fail" // a fresh fail with a structural code
	ReasonProven         RouteReason = "proven"          // a fresh proven value
	ReasonHostVantage    RouteReason = "host-vantage"    // proven from a host vantage proves the host
	ReasonTransientFail  RouteReason = "transient-fail"  // a fresh fail with a non-structural code
	ReasonUnproven       RouteReason = "unproven"        // a fresh unproven value
	ReasonNeverObserved  RouteReason = "never-observed"  // the cell reads -
	ReasonExpired        RouteReason = "expired"         // the cell reads ?
	ReasonOverridden     RouteReason = "overridden"      // excluded, and a supervisor named the seat
	ReasonNoDuties       RouteReason = "no-duties"       // the job named no duty to judge
)

// RouteOverride is a supervisor naming a seat for a job (rule 1, ruling 3).
// It applies only to the seat it names, and only when By names who.
type RouteOverride struct {
	Seat string `json:"seat"`
	By   string `json:"by"`
}

// RouteInput is everything the rule reads for one seat and one job.
type RouteInput struct {
	Seat string `json:"seat"`
	// Duties are the duties the job needs, as the role resolver gives them.
	// For a supervised seat the caller includes first-turn and ack (design
	// section 3).
	Duties []string `json:"duties"`
	// Cells are the seat's readiness cells, keyed by cell name, as
	// Store.SeatReadiness returns them.
	Cells    map[string]asof.Cell[string] `json:"cells"`
	Override *RouteOverride               `json:"override,omitempty"`
}

// DutyRoute is the verdict for one duty, with the cell and Source behind it.
type DutyRoute struct {
	Duty    string       `json:"duty"`
	Verdict RouteVerdict `json:"verdict"`
	Reason  RouteReason  `json:"reason"`
	Source  string       `json:"source,omitempty"`
}

// RouteDecision is the router's answer for a seat.
type RouteDecision struct {
	Verdict RouteVerdict `json:"verdict"`
	// Reason is set when the seat-level verdict has a reason of its own
	// (no-duties); otherwise each duty carries its reason.
	Reason       RouteReason `json:"reason,omitempty"`
	Duties       []DutyRoute `json:"duties,omitempty"`
	OverriddenBy string      `json:"overridden_by,omitempty"`
}

// RouteSeat applies the routing rule to one seat at now.
func RouteSeat(in RouteInput, now time.Time) RouteDecision {
	return RouteDecision{Verdict: RouteAskFirst}
}
