package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// The same real prompt of 10,000 tokens reaches the wire as the same
// PromptTokens whether the harness adds its cache classes to In (Claude's
// layout) or counts them inside In (codex's). That is why PROMPT can stand in
// a column beside seats on other harnesses and a raw input column cannot.
func TestPromptTokensLayoutNormalized(t *testing.T) {
	t.Parallel()
	additiveSeat, additiveSink, _ := newTestAccountant(t, Table{})
	additiveSeat.Observe(testCoords, turnEvent(claudecode.Harness, additive(2000, 7000, 1000)))

	subsumptiveSeat, subsumptiveSink, _ := newTestAccountant(t, Table{})
	subsumptiveSeat.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 10_000, CacheReadIn: 7000,
	}))

	a, okA := additiveSink.get(testCoords.AgentID)
	s, okS := subsumptiveSink.get(testCoords.AgentID)
	if !okA || !okS {
		t.Fatalf("readings written: additive %v, subsumptive %v; want both", okA, okS)
	}
	if a.SpendPromptTokens == nil || s.SpendPromptTokens == nil {
		t.Fatalf("prompt tokens absent: additive %v, subsumptive %v", deref(a.SpendPromptTokens), deref(s.SpendPromptTokens))
	}
	if *a.SpendPromptTokens != 10_000 || *s.SpendPromptTokens != 10_000 {
		t.Errorf("prompt tokens additive %d, subsumptive %d; want 10000 for both", *a.SpendPromptTokens, *s.SpendPromptTokens)
	}
}
