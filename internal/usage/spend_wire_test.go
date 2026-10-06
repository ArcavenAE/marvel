package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// TestSessionContextCarriesSpend pins the wire half of the spend: the
// reading the accountant writes carries the session's cumulative output
// tokens and layout-normalized prompt tokens, the same figures
// SessionSpend reports.
func TestSessionContextCarriesSpend(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})

	first := additive(1000, 400, 100)
	first.Out = 50
	second := additive(2000, 900, 100)
	second.Out = 70
	a.Observe(testCoords, turnEvent(claudecode.Harness, first))
	a.Observe(testCoords, turnEvent(claudecode.Harness, second))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	spend, seen := a.SessionSpend(testCoords.AgentID)
	if !seen || spend.PromptTokens == 0 {
		t.Fatalf("SessionSpend = %+v, seen %v; the fixture should have spent", spend, seen)
	}
	if got.SpendOut == nil || *got.SpendOut != 120 {
		t.Errorf("SpendOut = %v, want 120 (50 + 70, a sum)", deref(got.SpendOut))
	}
	if got.SpendPromptTokens == nil || *got.SpendPromptTokens != spend.PromptTokens {
		t.Errorf("SpendPromptTokens = %v, want %d (SessionSpend's own figure)", deref(got.SpendPromptTokens), spend.PromptTokens)
	}
}

// TestUnmeteredReadingCarriesNoSpend: a stream event with no usage writes
// no reading at all, so no zero-valued spend can be inferred from one.
func TestUnmeteredReadingCarriesNoSpend(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, rtevents.Event{
		SchemaVersion: rtevents.SchemaVersion,
		Event:         rtevents.KindSessionStarted,
		Harness:       claudecode.Harness,
		Data:          rtevents.SessionStartedData{Model: "claude-opus-4-8"},
	})

	if got, ok := sink.get(testCoords.AgentID); ok && (got.SpendOut != nil || got.SpendPromptTokens != nil) {
		t.Errorf("a session that spent nothing carries spend out=%v prompt=%v, want both absent",
			deref(got.SpendOut), deref(got.SpendPromptTokens))
	}
}

func deref(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}
