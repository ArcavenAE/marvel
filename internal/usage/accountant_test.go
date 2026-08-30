package usage

import (
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

// recordSink captures the last reading written per session key.
type recordSink struct {
	mu     sync.Mutex
	last   map[string]api.SessionContext
	writes int
}

func newRecordSink() *recordSink {
	return &recordSink{last: make(map[string]api.SessionContext)}
}

func (r *recordSink) UpdateSessionContext(key string, c api.SessionContext) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last[key] = c
	r.writes++
}

func (r *recordSink) get(key string) (api.SessionContext, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.last[key]
	return c, ok
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Millisecond)
		return t
	}
}

var testCoords = Coords{AgentID: "ws/agent-0", Workspace: "ws", Team: "squad", Role: "worker"}

func newTestAccountant(t *testing.T, table Table) (*Accountant, *recordSink, *events.Ring) {
	t.Helper()
	sink := newRecordSink()
	ring := events.NewRing(50)
	a := New(sink, NewResolver(table), WithEvents(ring), WithClock(fixedClock()))
	return a, sink, ring
}

// turnEvent builds a per-request usage event for a harness.
func turnEvent(harness string, r rtevents.RequestUsage) rtevents.Event {
	return rtevents.Event{
		SchemaVersion: rtevents.SchemaVersion,
		Event:         rtevents.KindTurnCompleted,
		Harness:       harness,
		Data:          rtevents.TurnData{Request: &r},
	}
}

// endedEvent builds a session-end event carrying session TOTALS. Claude
// is hardcoded because it is the only harness that emits one: codex ends
// at EOF after turn.completed, and opencode run mode has no session
// lifecycle line at all.
func endedEvent(u rtevents.Usage, m *rtevents.Metering) rtevents.Event {
	return rtevents.Event{
		SchemaVersion: rtevents.SchemaVersion,
		Event:         rtevents.KindSessionEnded,
		Harness:       claudecode.Harness,
		Data:          rtevents.SessionEndedData{ExitCode: 0, Usage: u, Metering: m},
	}
}

// startedEvent builds a session-start event. Claude is hardcoded for the
// same reason, plus it is the only harness that names a model in-stream.
func startedEvent(model string) rtevents.Event {
	return rtevents.Event{
		SchemaVersion: rtevents.SchemaVersion,
		Event:         rtevents.KindSessionStarted,
		Harness:       claudecode.Harness,
		Data:          rtevents.SessionStartedData{Model: model},
	}
}

func additive(in, cacheRead, cacheCreation int) rtevents.RequestUsage {
	return rtevents.RequestUsage{
		Layout:          rtevents.LayoutAdditive,
		In:              in,
		CacheReadIn:     cacheRead,
		CacheCreationIn: cacheCreation,
	}
}

// TestOccupancyIsALevelNotASum is the accountant half of the same guard
// the claudecode parser test carries: three samples produce the LAST
// occupancy, never their sum.
func TestOccupancyIsALevelNotASum(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	for _, in := range []int{100, 200, 150} {
		a.Observe(testCoords, turnEvent(claudecode.Harness, additive(in, 0, 0)))
	}

	occ, ok := a.SessionOccupancy(testCoords.AgentID)
	if !ok {
		t.Fatal("no occupancy recorded")
	}
	if occ.Tokens != 150 {
		t.Errorf("tokens = %d, want 150 (the last level, not the 450 sum)", occ.Tokens)
	}
	if occ.Requests != 3 {
		t.Errorf("requests = %d, want 3", occ.Requests)
	}
}

// TestSpendAccumulatesWhileOccupancyDoesNot pins the asymmetry: the same
// three samples that leave occupancy at a level sum into spend.
func TestSpendAccumulatesWhileOccupancyDoesNot(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	for _, in := range []int{100, 200, 150} {
		a.Observe(testCoords, turnEvent(claudecode.Harness, additive(in, 10, 5)))
	}

	spend, ok := a.SessionSpend(testCoords.AgentID)
	if !ok {
		t.Fatal("no spend recorded")
	}
	if spend.In != 450 {
		t.Errorf("spend.in = %d, want 450", spend.In)
	}
	if spend.CacheReadIn != 30 || spend.CacheCreationIn != 15 {
		t.Errorf("spend cache = read %d creation %d, want 30/15", spend.CacheReadIn, spend.CacheCreationIn)
	}
	if spend.Requests != 3 {
		t.Errorf("spend.requests = %d, want 3", spend.Requests)
	}
}

// TestTerminalSampleNeverEntersOccupancy is the regression guard for the
// defect that would otherwise reappear the moment anyone routes the
// result line through the same fold: session TOTALS are not a level.
//
// The numbers are the real tool_call fixture. Level after three requests
// is 34136; the terminal totals sum to 100994.
func TestTerminalSampleNeverEntersOccupancy(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{"claude-fable-5[1m]": 1_000_000})
	a.Bind(testCoords, Bind{Harness: claudecode.Harness, Redirection: api.BackendDefault})
	a.Observe(testCoords, startedEvent("claude-fable-5[1m]"))

	for _, r := range []rtevents.RequestUsage{
		{Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", RequestID: "a", In: 11368, CacheReadIn: 16643, CacheCreationIn: 5366},
		{Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", RequestID: "b", In: 2, CacheReadIn: 22009, CacheCreationIn: 11470},
		{Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", RequestID: "c", In: 331, CacheReadIn: 33479, CacheCreationIn: 326},
	} {
		a.Observe(testCoords, turnEvent(claudecode.Harness, r))
	}

	before, _ := a.SessionOccupancy(testCoords.AgentID)
	if before.Tokens != 34136 {
		t.Fatalf("tokens = %d before session end, want 34136", before.Tokens)
	}

	a.Observe(testCoords, endedEvent(
		rtevents.Usage{In: 11701, Out: 455},
		&rtevents.Metering{
			CacheReadIn:     72131,
			CacheCreationIn: 17162,
			ModelUsage: map[string]rtevents.ModelUsage{
				"claude-fable-5[1m]": {ContextWindow: 1_000_000},
			},
		},
	))

	after, _ := a.SessionOccupancy(testCoords.AgentID)
	if after.Tokens != 34136 {
		t.Errorf("tokens = %d after session end, want 34136 (the terminal total 100994 must not fold in)", after.Tokens)
	}
	if after.Peak != before.Peak {
		t.Errorf("peak moved from %.4f to %.4f on a terminal sample", before.Peak, after.Peak)
	}
	if s := a.Stats(); s.CumulationViolations != 0 || s.ReconcileMismatches != 0 {
		t.Errorf("reconciliation flagged a correct session: %+v", s)
	}
}

// TestReconciliationSilentWhenClassesAgree uses the fixture's exact
// numbers, which match class-wise in both directions.
func TestReconciliationSilentWhenClassesAgree(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(11368, 16643, 5366)))
	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(2, 22009, 11470)))
	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(331, 33479, 326)))
	a.Observe(testCoords, endedEvent(
		rtevents.Usage{In: 11701},
		&rtevents.Metering{CacheReadIn: 72131, CacheCreationIn: 17162},
	))

	s := a.Stats()
	if s.ReconcileMismatches != 0 || s.CumulationViolations != 0 {
		t.Errorf("clean reconciliation flagged: %+v", s)
	}
}

// TestCumulationViolationDetected is the runtime detector for a
// cumulative series read as levels. Strictly-greater is an arithmetic
// contradiction, so no threshold is involved.
func TestCumulationViolationDetected(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	// A cumulative series: each sample is the running total.
	for _, in := range []int{100, 200, 300} {
		a.Observe(testCoords, turnEvent(claudecode.Harness, additive(in, 0, 0)))
	}
	// The harness says the whole session used 300.
	a.Observe(testCoords, endedEvent(rtevents.Usage{In: 300}, &rtevents.Metering{NumTurns: 3}))

	s := a.Stats()
	if s.CumulationViolations != 1 {
		t.Errorf("cumulation violations = %d, want 1 (accumulated 600 against a 300 total)", s.CumulationViolations)
	}
	if s.ReconcileMismatches != 0 {
		t.Errorf("a cumulation violation must not double-count as a reconcile mismatch: %+v", s)
	}
}

// TestReconcileMismatchOnMissedSample is the other side: accumulating
// LESS than the harness reports means a sample was missed, not that the
// cumulation is wrong.
func TestReconcileMismatchOnMissedSample(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(100, 0, 0)))
	a.Observe(testCoords, endedEvent(rtevents.Usage{In: 350}, &rtevents.Metering{NumTurns: 2}))

	s := a.Stats()
	if s.ReconcileMismatches != 1 {
		t.Errorf("reconcile mismatches = %d, want 1", s.ReconcileMismatches)
	}
	if s.CumulationViolations != 0 {
		t.Errorf("cumulation violations = %d, want 0", s.CumulationViolations)
	}
}

func TestCompactionDetectedPastHysteresis(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(167_000, 0, 0)))
	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(96_000, 0, 0)))

	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Compactions != 1 {
		t.Errorf("compactions = %d, want 1 for a 71k drop on a 167k level", occ.Compactions)
	}
	if occ.Tokens != 96_000 {
		t.Errorf("tokens = %d, want 96000", occ.Tokens)
	}
	if s := a.Stats(); s.CompactionsDetected != 1 {
		t.Errorf("stats compactions = %d, want 1", s.CompactionsDetected)
	}
}

func TestCompactionNotDetectedWithinHysteresis(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	// A 1000-token dip on a 100k level: under both the absolute floor
	// (2048) and the fractional band (10k).
	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(100_000, 0, 0)))
	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(99_000, 0, 0)))

	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Compactions != 0 {
		t.Errorf("compactions = %d, want 0 for a 1k dip", occ.Compactions)
	}
	if occ.Tokens != 99_000 {
		t.Errorf("tokens = %d, want 99000 (the level still follows the sample)", occ.Tokens)
	}
}

// TestNonPrimaryModelRoutedToSpendOnly is the guard for the routing
// side-request: the tool_call fixture proves one Claude session can touch
// two models whose windows differ 5x, and a 527-token side call would read
// as a 33k collapse to any downward-step detector.
func TestNonPrimaryModelRoutedToSpendOnly(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{"claude-fable-5[1m]": 1_000_000})
	a.Bind(testCoords, Bind{Harness: claudecode.Harness, Redirection: api.BackendDefault})
	a.Observe(testCoords, startedEvent("claude-fable-5[1m]"))

	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", In: 11368, CacheReadIn: 16643, CacheCreationIn: 5366,
	}))
	// The routing model answers with a tiny prompt.
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-haiku-4-5-20251001", In: 527,
	}))

	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Tokens != 33377 {
		t.Errorf("tokens = %d, want 33377 (the side request must not move the series)", occ.Tokens)
	}
	if occ.Compactions != 0 {
		t.Errorf("compactions = %d, want 0", occ.Compactions)
	}
	if occ.Requests != 1 {
		t.Errorf("requests = %d, want 1 (the side request is not part of the series)", occ.Requests)
	}
	// It still costs tokens, so it still counts as spend.
	spend, _ := a.SessionSpend(testCoords.AgentID)
	if spend.In != 11368+527 {
		t.Errorf("spend.in = %d, want %d", spend.In, 11368+527)
	}
	if s := a.Stats(); s.NonPrimarySamples != 1 {
		t.Errorf("non-primary samples = %d, want 1", s.NonPrimarySamples)
	}
	// The denominator stays the primary model's window for the session's
	// life; no switching, no averaging.
	if occ.Limit != 1_000_000 {
		t.Errorf("limit = %d, want 1000000 (the primary model's window)", occ.Limit)
	}
}

// A harness that names no model anywhere (codex, opencode) must not have
// every one of its own samples read as a model switch.
//
// opencode carries this. codex used to, and no longer can: its samples
// are running totals and leave the fold before the routing check ever
// runs. The two measured opencode levels are used here because a level
// that FALLS between turns is also the shape a session total cannot have.
func TestUnnamedModelDoesNotRouteAsNonPrimary(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	for _, in := range []int{6018, 27} {
		a.Observe(testCoords, turnEvent(opencode.Harness, additive(in, 0, 0)))
	}

	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Requests != 2 {
		t.Errorf("requests = %d, want 2", occ.Requests)
	}
	if occ.Tokens != 27 {
		t.Errorf("tokens = %d, want 27 (the last level, not the 6045 sum)", occ.Tokens)
	}
	if s := a.Stats(); s.NonPrimarySamples != 0 {
		t.Errorf("non-primary samples = %d, want 0", s.NonPrimarySamples)
	}
}

// TestCumulativeSamplesProduceNoOccupancy is the codex guard, and the
// numbers are the specimen that overturned its profile.
//
// The repo's own tool_call.jsonl fixture reports turn.completed
// input_tokens 28110 for thread 019fba87-d036-7ae1-a20e-7187ef8e3329.
// Codex's per-request record for that same thread holds two requests:
// prompts of 14005 then 14105, accumulating to 28110. The prompt at turn
// end was 14105. A feed that cannot tell those apart must report no
// level at all rather than the sum, so CTX% renders "-".
func TestCumulativeSamplesProduceNoOccupancy(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{"gpt-5.6-sol": 258_400})

	a.Bind(testCoords, Bind{Harness: codex.Harness, Args: []string{"-m", "gpt-5.6-sol"}})
	a.Observe(testCoords, turnEvent(codex.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 14005, CacheReadIn: 11008, Out: 71,
	}))
	a.Observe(testCoords, turnEvent(codex.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 28110, CacheReadIn: 24064, Out: 76,
	}))

	if _, ok := sink.get(testCoords.AgentID); ok {
		t.Error("a reading was written from a cumulative feed; CTX% must render absent")
	}
	// SessionOccupancy's bool reports that state EXISTS (Bind made it),
	// not that a level was measured, so the level itself is the assertion.
	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Tokens != 0 {
		t.Errorf("tokens = %d, want 0; no level is derivable from a running total", occ.Tokens)
	}
	if occ.Requests != 0 {
		t.Errorf("requests = %d, want 0", occ.Requests)
	}
	if occ.Percent != 0 {
		t.Errorf("percent = %v, want 0 even though the window resolved", occ.Percent)
	}

	st := a.Stats()
	if st.CumulativeSamples != 2 {
		t.Errorf("cumulative samples = %d, want 2", st.CumulativeSamples)
	}
	if st.SamplesObserved != 0 {
		t.Errorf("samples observed = %d, want 0 (none entered occupancy)", st.SamplesObserved)
	}

	// The tokens are still real money. A running total REPLACES the
	// previous one: accumulating them would report 42115 prompt tokens
	// for a session that used 28110.
	spend, ok := a.SessionSpend(testCoords.AgentID)
	if !ok {
		t.Fatal("no spend recorded")
	}
	if spend.PromptTokens != 28110 {
		t.Errorf("PromptTokens = %d, want 28110 (the latest total, not the 42115 sum)", spend.PromptTokens)
	}
	if spend.Out != 76 {
		t.Errorf("Out = %d, want 76 (the latest total, not the 147 sum)", spend.Out)
	}
	if spend.CacheReadIn != 24064 {
		t.Errorf("CacheReadIn = %d, want 24064 (the latest total)", spend.CacheReadIn)
	}
}

// TestTotalMismatchCounted: the total invariant catches a harness whose
// classes stop adding up to its own published total, which is a usage
// schema change, and still produces the reading.
func TestTotalMismatchCounted(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	// opencode's total is defined over in + out + reasoning; 29257 is not.
	a.Observe(testCoords, turnEvent(opencode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive,
		In:     40, Out: 12, ReasoningOut: 5,
		CacheReadIn: 29000, CacheCreationIn: 200,
		Total: 29257, TotalExcludesCache: true,
	}))

	if s := a.Stats(); s.TotalMismatches != 1 {
		t.Errorf("total mismatches = %d, want 1", s.TotalMismatches)
	}
	// The reading is still produced: a suspect number beats no number, and
	// the counter plus the log line say it is suspect.
	if _, ok := a.SessionOccupancy(testCoords.AgentID); !ok {
		t.Error("no reading produced for a mismatched sample")
	}
}

// TestOpenCodeMeasuredTotalIsSilent is the false-positive guard. OpenCode's
// total omits the cache classes by the vendor's own definition
// (finding-007), so a caching turn must not report a mismatch. It would
// otherwise fire on every caching session and say nothing about the one
// premise it was reached for.
func TestOpenCodeMeasuredTotalIsSilent(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(opencode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive,
		In:     40, Out: 12, ReasoningOut: 5,
		CacheReadIn: 29000, CacheCreationIn: 200,
		Total: 57, TotalExcludesCache: true,
	}))

	s := a.Stats()
	if s.TotalMismatches != 0 {
		t.Errorf("total mismatches = %d, want 0 for opencode's measured total definition", s.TotalMismatches)
	}
	// The one signal that does bear on the cache layout: In cannot contain
	// a cache read larger than itself.
	if s.AdditiveConfirmations != 1 {
		t.Errorf("additive confirmations = %d, want 1", s.AdditiveConfirmations)
	}
	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Tokens != 29240 {
		t.Errorf("tokens = %d, want 29240 (in + cache read + cache write)", occ.Tokens)
	}
}

// The confirmation is one-sided: a subsumptive In is always the larger
// number, so nothing in the stream can flag the case marvel would get
// wrong. Pins the claim so nobody reads a zero counter as a clean bill.
func TestSubsumptiveShapeIsNotConfirmed(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(opencode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive,
		In:     29240, Out: 12, ReasoningOut: 5,
		CacheReadIn: 29000, CacheCreationIn: 200,
		Total: 29257, TotalExcludesCache: true,
	}))

	s := a.Stats()
	if s.AdditiveConfirmations != 0 {
		t.Errorf("additive confirmations = %d, want 0 for a subsumptive shape", s.AdditiveConfirmations)
	}
	if s.TotalMismatches != 0 {
		t.Errorf("total mismatches = %d, want 0: the total is well-formed either way", s.TotalMismatches)
	}
}

// A Claude subagent turn (Task tool) carries a much smaller prompt against
// its own window. It must not touch the parent's occupancy level, must not
// book a compaction, and must still be paid for.
func TestSubagentTurnStaysOutOfOccupancy(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{"claude-fable-5": 1_000_000})

	main := func(in, cacheRead int) rtevents.RequestUsage {
		return rtevents.RequestUsage{
			Model: "claude-fable-5", Layout: rtevents.LayoutAdditive,
			In: in, Out: 3, CacheReadIn: cacheRead,
		}
	}
	a.Observe(testCoords, turnEvent(claudecode.Harness, main(331, 33479)))
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Model: "claude-fable-5", Layout: rtevents.LayoutAdditive,
		In: 900, Out: 40, CacheReadIn: 1200,
		ParentToolUseID: "toolu_sub",
	}))
	a.Observe(testCoords, turnEvent(claudecode.Harness, main(400, 34200)))

	occ, ok := a.SessionOccupancy(testCoords.AgentID)
	if !ok {
		t.Fatal("no occupancy for the session")
	}
	if occ.Tokens != 34600 {
		t.Errorf("tokens = %d, want 34600 (the last MAIN-agent level)", occ.Tokens)
	}
	if occ.Requests != 2 {
		t.Errorf("requests = %d, want 2 (the subagent turn is not a session request)", occ.Requests)
	}
	if occ.Compactions != 0 {
		t.Errorf("compactions = %d, want 0; the subagent's smaller prompt is not a compaction", occ.Compactions)
	}

	s := a.Stats()
	if s.SubagentSamples != 1 {
		t.Errorf("subagent samples = %d, want 1", s.SubagentSamples)
	}
	if s.CompactionsDetected != 0 {
		t.Errorf("compactions detected = %d, want 0", s.CompactionsDetected)
	}
	// Real tokens, real money: spend keeps them.
	spend, _ := a.SessionSpend(testCoords.AgentID)
	if spend.In != 331+900+400 {
		t.Errorf("spend in = %d, want %d (the subagent's prompt is still billed)", spend.In, 331+900+400)
	}
	if spend.Requests != 3 {
		t.Errorf("spend requests = %d, want 3", spend.Requests)
	}
}

// TestUnresolvedLimitReportsAbsence: tokens are recorded, the percentage
// is not invented, and the operator gets one event saying why.
func TestUnresolvedLimitReportsAbsence(t *testing.T) {
	t.Parallel()
	a, sink, ring := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(opencode.Harness, additive(13992, 0, 0)))
	a.Observe(testCoords, turnEvent(opencode.Harness, additive(28110, 0, 0)))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	if got.ContextTokens != 28110 {
		t.Errorf("tokens = %d, want 28110", got.ContextTokens)
	}
	if got.ContextLimit != 0 {
		t.Errorf("limit = %d, want 0", got.ContextLimit)
	}
	if got.ContextPercent != 0 {
		t.Errorf("percent = %v, want 0 (never invented from a guessed window)", got.ContextPercent)
	}
	if got.ContextLimitSource != string(LimitUnresolved) {
		t.Errorf("limit source = %q, want %q", got.ContextLimitSource, LimitUnresolved)
	}

	snap := ring.Snapshot(events.Filter{Kind: events.KindContextLimitUnresolved}, 0)
	if len(snap) != 1 {
		t.Fatalf("got %d context.limit-unresolved events, want exactly 1 per session", len(snap))
	}
	if snap[0].Session != testCoords.AgentID {
		t.Errorf("event session = %q, want %q", snap[0].Session, testCoords.AgentID)
	}
	if s := a.Stats(); s.SessionsUnresolved != 1 {
		t.Errorf("sessions unresolved = %d, want 1", s.SessionsUnresolved)
	}
}

func TestUnknownHarnessIgnored(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent("crush", additive(1000, 0, 0)))

	if _, ok := sink.get(testCoords.AgentID); ok {
		t.Error("an unknown harness produced a reading; a blank column is the intended behavior")
	}
	if s := a.Stats(); s.SamplesIgnored != 1 {
		t.Errorf("samples ignored = %d, want 1", s.SamplesIgnored)
	}
}

func TestEventsWithoutUsageIgnored(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})

	// A turn event from a harness that does not attribute usage.
	a.Observe(testCoords, rtevents.Event{
		Event: rtevents.KindTurnCompleted, Harness: claudecode.Harness,
		Data: rtevents.TurnData{},
	})
	a.Observe(testCoords, rtevents.Event{
		Event: rtevents.KindToolCall, Harness: claudecode.Harness,
		Data: rtevents.ToolCallData{Tool: "Bash"},
	})

	if _, ok := sink.get(testCoords.AgentID); ok {
		t.Error("an event with no usage produced a reading")
	}
	if s := a.Stats(); s.SamplesIgnored != 2 {
		t.Errorf("samples ignored = %d, want 2", s.SamplesIgnored)
	}
}

func TestBindResolvesDenominatorBeforeFirstSample(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})

	a.Bind(testCoords, Bind{Harness: opencode.Harness, Args: []string{"-m", "gpt-x"}, Window: 258_400})
	a.Observe(testCoords, turnEvent(opencode.Harness, additive(25_840, 0, 0)))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	if got.ContextLimit != 258_400 {
		t.Errorf("limit = %d, want 258400 on the FIRST fold", got.ContextLimit)
	}
	if got.ContextLimitSource != string(LimitFromManifest) {
		t.Errorf("limit source = %q, want %q", got.ContextLimitSource, LimitFromManifest)
	}
	if got.ContextPercent < 9.9 || got.ContextPercent > 10.1 {
		t.Errorf("percent = %v, want about 10", got.ContextPercent)
	}
	if got.ContextModel != "gpt-x" {
		t.Errorf("model = %q, want gpt-x (read from the launch args)", got.ContextModel)
	}
}

// TestSessionStartUpgradesDenominator: Claude names its model in-stream,
// which outranks whatever the launch args said.
func TestSessionStartUpgradesDenominator(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{
		"claude-haiku-4-5":   200_000,
		"claude-fable-5[1m]": 1_000_000,
	})

	a.Bind(testCoords, Bind{Harness: claudecode.Harness, Args: []string{"--model", "haiku"}, Redirection: api.BackendDefault})
	a.Observe(testCoords, startedEvent("claude-fable-5[1m]"))
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", In: 100_000,
	}))

	got, _ := sink.get(testCoords.AgentID)
	if got.ContextLimit != 1_000_000 {
		t.Errorf("limit = %d, want 1000000 from the in-stream model", got.ContextLimit)
	}
	if got.ContextLimitSource != string(LimitFromTable) {
		t.Errorf("limit source = %q, want %q", got.ContextLimitSource, LimitFromTable)
	}
}

// The [1m] suffix has to survive the whole path, not just the resolver: a
// session started on the 200k sibling must not read against the 1M window.
// The per-request model drops the suffix in both cases, so the identity
// check and the denominator lookup cannot share a key.
func TestSiblingWindowsDoNotCollideAtSessionStart(t *testing.T) {
	t.Parallel()
	table := Table{"claude-sonnet-4-6": 200_000, "claude-sonnet-4-6[1m]": 1_000_000}

	for _, c := range []struct {
		start string
		want  int
	}{
		{"claude-sonnet-4-6", 200_000},
		{"claude-sonnet-4-6[1m]", 1_000_000},
	} {
		a, sink, _ := newTestAccountant(t, table)
		a.Bind(testCoords, Bind{Harness: claudecode.Harness, Redirection: api.BackendDefault})
		a.Observe(testCoords, startedEvent(c.start))
		a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
			Layout: rtevents.LayoutAdditive, Model: "claude-sonnet-4-6", In: 100_000,
		}))

		got, ok := sink.get(testCoords.AgentID)
		if !ok {
			t.Fatalf("%s: no reading written", c.start)
		}
		if got.ContextLimit != c.want {
			t.Errorf("%s: limit = %d, want %d", c.start, got.ContextLimit, c.want)
		}
		if got.ContextRequests != 1 {
			t.Errorf("%s: requests = %d, want 1; the unsuffixed per-request model is the same series", c.start, got.ContextRequests)
		}
	}
}

// TestTerminalDeclarationLearnsWindow: Claude declares its window only on
// the terminal line, so the value it teaches is what serves the NEXT
// session live. This is how the empty codex and opencode table sections
// fill themselves from real sessions.
func TestTerminalDeclarationLearnsWindow(t *testing.T) {
	t.Parallel()
	sink := newRecordSink()
	res := NewResolver(Table{})
	a := New(sink, res, WithClock(fixedClock()))

	a.Bind(testCoords, Bind{Harness: claudecode.Harness, Redirection: api.BackendDefault})
	a.Observe(testCoords, startedEvent("claude-fable-5[1m]"))
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", In: 34_136,
	}))
	// Before the declaration the window is unknown.
	if got, _ := sink.get(testCoords.AgentID); got.ContextLimit != 0 {
		t.Fatalf("limit = %d before any declaration, want 0", got.ContextLimit)
	}

	a.Observe(testCoords, endedEvent(
		rtevents.Usage{In: 34_136},
		&rtevents.Metering{ModelUsage: map[string]rtevents.ModelUsage{
			"claude-fable-5[1m]": {ContextWindow: 1_000_000},
		}},
	))

	// Learned under the session's backend verdict (default here).
	if w, ok := res.Learned("claude-fable-5[1m]", api.BackendDefault); !ok || w != 1_000_000 {
		t.Errorf("learned window = %d (%v), want 1000000", w, ok)
	}
	// The [1m] suffix must survive into the cache key: it changes the
	// window 5x, so a key that strips it collapses two models into one.
	if _, ok := res.Learned("claude-fable-5", api.BackendDefault); ok {
		t.Error("the learned key dropped the [1m] suffix, which changes the window 5x")
	}

	// A second session on the same daemon resolves live.
	next := Coords{AgentID: "ws/agent-1", Workspace: "ws", Team: "squad", Role: "worker"}
	a.Bind(next, Bind{Harness: claudecode.Harness, Redirection: api.BackendDefault})
	a.Observe(next, startedEvent("claude-fable-5[1m]"))
	a.Observe(next, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", In: 50_000,
	}))
	got, ok := sink.get(next.AgentID)
	if !ok {
		t.Fatal("no reading for the second session")
	}
	if got.ContextLimit != 1_000_000 {
		t.Errorf("second session limit = %d, want 1000000", got.ContextLimit)
	}
	if got.ContextLimitSource != string(LimitLearned) {
		t.Errorf("second session limit source = %q, want %q", got.ContextLimitSource, LimitLearned)
	}
}

func TestPeakIsAHighWaterMark(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})
	a.Bind(testCoords, Bind{Harness: claudecode.Harness, Window: 200_000})

	for _, in := range []int{100_000, 160_000, 90_000} {
		a.Observe(testCoords, turnEvent(claudecode.Harness, additive(in, 0, 0)))
	}

	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Peak < 79.9 || occ.Peak > 80.1 {
		t.Errorf("peak = %v, want about 80", occ.Peak)
	}
	if occ.Percent < 44.9 || occ.Percent > 45.1 {
		t.Errorf("percent = %v, want about 45 (the current level, not the peak)", occ.Percent)
	}
}

func TestForgetRollsSpendIntoTeamTotals(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})
	cost := 0.42

	a.Bind(testCoords, Bind{Harness: opencode.Harness})
	a.Observe(testCoords, turnEvent(opencode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, In: 1000, Out: 20, Total: 1020, Cost: &cost,
	}))

	live := a.TeamSpend("ws", "squad")
	if live.LiveSessions != 1 || live.In != 1000 {
		t.Fatalf("live team totals = %+v, want 1 session with 1000 in", live)
	}

	a.Forget(testCoords.AgentID)

	after := a.TeamSpend("ws", "squad")
	if after.LiveSessions != 0 || after.EndedSessions != 1 {
		t.Errorf("after forget: live %d ended %d, want 0/1", after.LiveSessions, after.EndedSessions)
	}
	if after.In != 1000 || after.Out != 20 {
		t.Errorf("retired spend = in %d out %d, want 1000/20", after.In, after.Out)
	}
	if after.CostUSD != cost {
		t.Errorf("retired cost = %v, want %v", after.CostUSD, cost)
	}
	if after.Partial {
		t.Error("a fully observed session must not mark the team total partial")
	}
	if s := a.Stats(); s.Tracked != 0 {
		t.Errorf("tracked = %d after forget, want 0", s.Tracked)
	}
}

// A session that was never observed makes the team total incomplete, and
// a consumer must be able to tell that from a small total.
func TestNeverObservedSessionMarksTeamTotalPartial(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Bind(testCoords, Bind{Harness: claudecode.Harness})
	a.Forget(testCoords.AgentID)

	if got := a.TeamSpend("ws", "squad"); !got.Partial {
		t.Error("a never-observed session did not mark the team total partial")
	}
}

func TestSweepDropsVanishedSessions(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	for _, id := range []string{"ws/a", "ws/b", "ws/c"} {
		a.Bind(Coords{AgentID: id, Workspace: "ws", Team: "squad"}, Bind{Harness: claudecode.Harness})
		a.Observe(Coords{AgentID: id, Workspace: "ws", Team: "squad"},
			turnEvent(claudecode.Harness, additive(100, 0, 0)))
	}
	if s := a.Stats(); s.Tracked != 3 {
		t.Fatalf("tracked = %d, want 3", s.Tracked)
	}

	a.Sweep(map[string]bool{"ws/b": true})

	s := a.Stats()
	if s.Tracked != 1 {
		t.Errorf("tracked = %d after sweep, want 1 (the leak canary)", s.Tracked)
	}
	if _, ok := a.SessionOccupancy("ws/a"); ok {
		t.Error("ws/a survived the sweep")
	}
	if _, ok := a.SessionOccupancy("ws/b"); !ok {
		t.Error("ws/b was swept despite being live")
	}
	// Swept sessions still contribute their spend to the team: the total
	// is the two retired plus the one still live.
	got := a.TeamSpend("ws", "squad")
	if got.EndedSessions != 2 || got.LiveSessions != 1 {
		t.Errorf("team totals = %d ended %d live, want 2/1", got.EndedSessions, got.LiveSessions)
	}
	if got.In != 300 {
		t.Errorf("team spend.in = %d, want 300 (two retired plus one live)", got.In)
	}
}

func TestNilAccountantIsSafe(t *testing.T) {
	t.Parallel()
	var a *Accountant

	a.Bind(testCoords, Bind{Harness: claudecode.Harness})
	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(1, 2, 3)))
	a.Forget(testCoords.AgentID)
	a.Sweep(map[string]bool{})
	if s := a.Stats(); s.Tracked != 0 {
		t.Errorf("nil accountant stats = %+v, want zero", s)
	}
	if _, ok := a.SessionOccupancy("x"); ok {
		t.Error("nil accountant reported an occupancy")
	}
	if _, ok := a.SessionSpend("x"); ok {
		t.Error("nil accountant reported a spend")
	}
	if got := a.TeamSpend("ws", "squad"); got.LiveSessions != 0 {
		t.Errorf("nil accountant team totals = %+v, want zero", got)
	}
}

// The accountant is shared by N per-session drain goroutines, and will be
// shared with an OTEL receiver goroutine later. Run under -race.
func TestConcurrentObserveIsSafe(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{"claude-fable-5[1m]": 1_000_000})

	var wg sync.WaitGroup
	for i := range 8 {
		c := Coords{AgentID: "ws/agent-" + string(rune('a'+i)), Workspace: "ws", Team: "squad", Role: "worker"}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Bind(c, Bind{Harness: claudecode.Harness, Window: 200_000})
			for n := range 50 {
				a.Observe(c, turnEvent(claudecode.Harness, additive(1000+n*10, 0, 0)))
			}
			_, _ = a.SessionOccupancy(c.AgentID)
			_ = a.TeamSpend("ws", "squad")
			a.Forget(c.AgentID)
		}()
	}
	wg.Wait()

	if got := a.TeamSpend("ws", "squad"); got.EndedSessions != 8 {
		t.Errorf("ended sessions = %d, want 8", got.EndedSessions)
	}
}

// TestSpendPromptTokensAppliesLayout is the measurement aae-orc-qiay's token
// budget rests on.
//
// The raw class fields accumulate exactly as the feed reported them and
// Spend records no layout, so no sum of them is both complete and free of
// double counting: In + CacheReadIn + CacheCreationIn double counts a
// subsumptive feed, while In + Out alone omits most of an additive feed's
// input volume. PromptTokens applies Sample.Occupancy per request, so it is
// the one prompt figure a caller can add up without knowing the harness.
//
// Falsification: with the raw classes summed instead, the codex row here
// reads 63153 against a real 42102, so a declared max_tokens would refuse a
// codex team at roughly two thirds of its ceiling, silently.
func TestSpendPromptTokensAppliesLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		harness string
		samples []rtevents.RequestUsage
		want    int
	}{
		{
			// codex is subsumptive AND cumulative, so its running totals
			// replace rather than accumulate. Layout still governs which
			// classes count: summing the raw classes would read 42165
			// against the harness's own 28110.
			name:    "subsumptive counts In alone",
			harness: codex.Harness,
			samples: []rtevents.RequestUsage{
				{Layout: rtevents.LayoutSubsumptive, In: 13992, CacheReadIn: 6996},
				{Layout: rtevents.LayoutSubsumptive, In: 28110, CacheReadIn: 14055},
			},
			want: 28110,
		},
		{
			name:    "additive counts In plus the cache classes",
			harness: claudecode.Harness,
			samples: []rtevents.RequestUsage{
				additive(11368, 16643, 5366),
				additive(2, 22009, 11470),
			},
			want: 11368 + 16643 + 5366 + 2 + 22009 + 11470,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, _, _ := newTestAccountant(t, Table{})
			for _, s := range tt.samples {
				a.Observe(testCoords, turnEvent(tt.harness, s))
			}

			spend, ok := a.SessionSpend(testCoords.AgentID)
			if !ok {
				t.Fatal("no spend recorded")
			}
			if spend.PromptTokens != tt.want {
				t.Errorf("PromptTokens = %d, want %d", spend.PromptTokens, tt.want)
			}

			// The figure must promote through retirement into the team total,
			// because a fan-out's spend is mostly in sessions that exited.
			team := a.TeamSpend(testCoords.Workspace, testCoords.Team)
			if team.PromptTokens != tt.want {
				t.Errorf("live TeamSpend PromptTokens = %d, want %d", team.PromptTokens, tt.want)
			}
			a.Forget(testCoords.AgentID)
			retired := a.TeamSpend(testCoords.Workspace, testCoords.Team)
			if retired.PromptTokens != tt.want {
				t.Errorf("retired TeamSpend PromptTokens = %d, want %d", retired.PromptTokens, tt.want)
			}
		})
	}
}

// TestTerminalSampleAddsNoPromptTokens proves the placement is safe.
// Sample.Occupancy is meaningless on a terminal sample, which carries
// session totals rather than a level — folding one in is the defect this
// package exists to prevent. Observe returns to foldTerminalLocked before
// any spend call, so a terminal sample must move the figure by nothing.
func TestTerminalSampleAddsNoPromptTokens(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(11368, 16643, 5366)))
	before, _ := a.SessionSpend(testCoords.AgentID)

	cost := 0.42
	a.Observe(testCoords, endedEvent(
		rtevents.Usage{In: 11368, Out: 900, Cost: &cost},
		&rtevents.Metering{CacheReadIn: 16643, CacheCreationIn: 5366},
	))

	after, _ := a.SessionSpend(testCoords.AgentID)
	if after.PromptTokens != before.PromptTokens {
		t.Errorf("a terminal sample moved PromptTokens from %d to %d", before.PromptTokens, after.PromptTokens)
	}
	if !after.CostReported {
		t.Error("test is vacuous: the terminal sample was not folded at all")
	}
}

// TestSubagentAndNonPrimarySpendCountsAsPromptTokens: both classes are real
// money against a DIFFERENT context window, so they stay out of occupancy
// and stay in spend. A budget that dropped them would undercount a team
// running Task tools or routing across models.
func TestSubagentAndNonPrimarySpendCountsAsPromptTokens(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, startedEvent("claude-fable-5"))
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", In: 1000, CacheReadIn: 200,
	}))
	primary, _ := a.SessionSpend(testCoords.AgentID)

	// A subagent turn: parentToolUseID marks it, so it never enters the
	// occupancy fold.
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-fable-5", ParentToolUseID: "toolu_1",
		In: 300, CacheReadIn: 50,
	}))
	// A non-primary model answering inside the same session.
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutAdditive, Model: "claude-haiku-4-5-20251001", In: 40,
	}))

	spend, _ := a.SessionSpend(testCoords.AgentID)
	want := primary.PromptTokens + 300 + 50 + 40
	if spend.PromptTokens != want {
		t.Errorf("PromptTokens = %d, want %d (subagent and non-primary spend included)", spend.PromptTokens, want)
	}
	occ, _ := a.SessionOccupancy(testCoords.AgentID)
	if occ.Tokens != 1200 {
		t.Errorf("occupancy tokens = %d, want 1200 (neither sample entered the level)", occ.Tokens)
	}
}
