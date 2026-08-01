package usage

import (
	"time"

	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// Cumulation says whether a sample carries one request's own level or a
// running total over the session. Declared per feed and asserted at
// runtime, never inferred: getting it wrong is the measured failure this
// package exists to prevent (see doc.go).
type Cumulation string

const (
	CumulationRequest Cumulation = "request"
	CumulationSession Cumulation = "session"
)

// Sample is one normalized observation. Feed-agnostic: the stream feed
// and a future OTEL feed produce this identical shape, so the fold is
// written once.
type Sample struct {
	AgentID          string
	HarnessSessionID string
	Harness          string
	// Model is the model as the feed reported it, empty when the feed
	// names none (codex and opencode name none in-stream).
	Model     string
	RequestID string
	TS        time.Time

	Layout     rtevents.Layout
	Cumulation Cumulation

	In              int
	Out             int
	CacheReadIn     int
	CacheCreationIn int
	ReasoningOut    int
	Total           int
	// TotalExcludesCache says Total is defined over In + Out +
	// ReasoningOut alone. See events.RequestUsage.
	TotalExcludesCache bool
	CostUSD            *float64

	// Subagent marks a request made inside a tool call (Claude's Task
	// tool). Its tokens are real spend against a DIFFERENT context
	// window, so it must not enter the occupancy fold.
	Subagent bool

	// DeclaredLimit is the context window the feed declared alongside
	// this sample, 0 when it declared none.
	DeclaredLimit int
	// Terminal marks a session-end sample. Such a sample carries session
	// TOTALS, not a level, and must never enter the occupancy fold. It
	// feeds reconciliation and limit learning only.
	Terminal bool
}

// Occupancy is the context-window numerator this sample implies.
// Meaningless on a Terminal sample, which carries totals.
func (s Sample) Occupancy() int {
	if s.Layout == rtevents.LayoutSubsumptive {
		return s.In
	}
	return s.In + s.CacheReadIn + s.CacheCreationIn
}

// ClassSum adds the three prompt-token classes without applying Layout.
// It is the reconciliation quantity: comparing accumulated per-request
// classes against a terminal total has to be layout-independent, because
// the two sides are the same classes either way.
func (s Sample) ClassSum() int {
	return s.In + s.CacheReadIn + s.CacheCreationIn
}

// TotalMismatch reports the harness's own total minus the sum of the
// classes that total is defined over, or 0 when the harness published
// none. Nonzero means the harness's arithmetic disagrees with marvel's
// reading of its fields, which is a usage-schema change. It does not
// arbitrate Layout where TotalExcludesCache is set; see
// AdditiveConfirmed and events.RequestUsage.TotalMismatch.
func (s Sample) TotalMismatch() int {
	if s.Total == 0 {
		return 0
	}
	sum := s.In + s.Out + s.ReasoningOut
	if !s.TotalExcludesCache && s.Layout != rtevents.LayoutSubsumptive {
		sum += s.CacheReadIn + s.CacheCreationIn
	}
	return s.Total - sum
}

// AdditiveConfirmed reports a sample that PROVES In excludes CacheReadIn.
// One-sided; see events.RequestUsage.AdditiveConfirmed.
func (s Sample) AdditiveConfirmed() bool {
	return s.Layout == rtevents.LayoutAdditive && s.CacheReadIn > 0 && s.In < s.CacheReadIn
}

// sampleFromEvent lifts one adapter event into a Sample. ok is false for
// events carrying no usage, which is most of them.
//
// primaryRaw is the session's model as the harness names it, needed to
// index a terminal sample's per-model window declaration. Selecting that
// entry by anything else (first key, max, a range) is wrong: a session
// routing across models carries several entries with windows differing
// by 5x, and Go map iteration is randomized.
func sampleFromEvent(c Coords, ev rtevents.Event, prof profile, primaryRaw string) (Sample, bool) {
	base := Sample{
		AgentID:          c.AgentID,
		HarnessSessionID: ev.HarnessSessionID,
		Harness:          ev.Harness,
		TS:               ev.TS,
		Cumulation:       prof.cumulation,
	}

	switch d := ev.Data.(type) {
	case rtevents.TurnData:
		r := d.Request
		if r == nil {
			return Sample{}, false
		}
		base.Model = r.Model
		base.RequestID = r.RequestID
		base.Layout = r.Layout
		base.In = r.In
		base.Out = r.Out
		base.CacheReadIn = r.CacheReadIn
		base.CacheCreationIn = r.CacheCreationIn
		base.ReasoningOut = r.ReasoningOut
		base.Total = r.Total
		base.TotalExcludesCache = r.TotalExcludesCache
		base.CostUSD = r.Cost
		base.DeclaredLimit = r.ContextWindow
		base.Subagent = r.Subagent()
		return base, true

	case rtevents.SessionEndedData:
		if d.Metering == nil && d.Usage.In == 0 && d.Usage.Out == 0 {
			return Sample{}, false
		}
		base.Terminal = true
		base.Cumulation = CumulationSession
		base.Model = primaryRaw
		base.In = d.Usage.In
		base.Out = d.Usage.Out
		base.CostUSD = d.Usage.Cost
		if m := d.Metering; m != nil {
			base.CacheReadIn = m.CacheReadIn
			base.CacheCreationIn = m.CacheCreationIn
			if mu, ok := m.ModelUsage[primaryRaw]; ok {
				base.DeclaredLimit = mu.ContextWindow
			}
		}
		return base, true
	}
	return Sample{}, false
}
