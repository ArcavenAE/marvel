package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// A codex seat's feed is a running total with no occupancy, and its tokens are
// real spend. The accountant publishes the spend to the session without a
// context reading: CTX% stays absent, and the TOUT cell reads the totals.
func TestCodexCumulativeSampleSetsSpend(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{"gpt-5.6-sol": 258_400})
	a.Bind(testCoords, Bind{Harness: codex.Harness, Args: []string{"-m", "gpt-5.6-sol"}})

	a.Observe(testCoords, turnEvent(codex.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 14005, CacheReadIn: 11008, Out: 71,
	}))
	a.Observe(testCoords, turnEvent(codex.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 28110, CacheReadIn: 24064, Out: 76,
	}))

	sp, ok := sink.getSpend(testCoords.AgentID)
	if !ok {
		t.Fatal("no spend reached the session from a cumulative feed")
	}
	if sp.Out == nil || *sp.Out != 76 {
		t.Errorf("Out = %v, want 76 (the latest total)", deref(sp.Out))
	}
	if sp.PromptTokens == nil || *sp.PromptTokens != 28110 {
		t.Errorf("PromptTokens = %v, want 28110 (the latest total)", deref(sp.PromptTokens))
	}
	if sp.OutRate.ObservedAt.IsZero() {
		t.Error("OutRate was not sampled: the second total is 5 tokens above the first")
	}
	if _, wrote := sink.get(testCoords.AgentID); wrote {
		t.Error("a context reading was written from a cumulative feed; CTX% must stay absent")
	}
}

// A feed that carries occupancy still writes one whole reading and no
// separate spend write: the spend rides the reading, as before.
func TestOccupancyFeedWritesNoSeparateSpend(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{Layout: rtevents.LayoutAdditive, In: 100, Out: 10}))

	if _, ok := sink.getSpend(testCoords.AgentID); ok {
		t.Error("an occupancy feed wrote a separate spend update")
	}
	if got, ok := sink.get(testCoords.AgentID); !ok || got.SpendOut == nil {
		t.Errorf("the reading carries no spend: %+v (written %v)", got, ok)
	}
}
