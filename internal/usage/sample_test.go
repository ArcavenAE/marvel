package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

func TestSampleOccupancyAppliesLayout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		sample Sample
		want   int
	}{
		{
			// Measured: In alone would read 11368 where the real prompt is
			// 33377, because the cached bulk is not in In.
			name:   "additive sums the cache classes",
			sample: Sample{Layout: rtevents.LayoutAdditive, In: 11368, CacheReadIn: 16643, CacheCreationIn: 5366},
			want:   33377,
		},
		{
			// Measured: input_tokens 13992 already contains
			// cached_input_tokens 11008, so summing gives 25000, which is
			// wrong.
			name:   "subsumptive uses In alone",
			sample: Sample{Layout: rtevents.LayoutSubsumptive, In: 13992, CacheReadIn: 11008},
			want:   13992,
		},
		{
			name:   "an undeclared layout falls back to additive",
			sample: Sample{In: 100, CacheReadIn: 10, CacheCreationIn: 1},
			want:   111,
		},
	}
	for _, c := range cases {
		if got := c.sample.Occupancy(); got != c.want {
			t.Errorf("%s: occupancy = %d, want %d", c.name, got, c.want)
		}
	}
}

// ClassSum is the reconciliation quantity and must ignore Layout: the two
// sides of the comparison are the same token classes either way.
func TestSampleClassSumIgnoresLayout(t *testing.T) {
	t.Parallel()
	s := Sample{Layout: rtevents.LayoutSubsumptive, In: 100, CacheReadIn: 20, CacheCreationIn: 3}
	if got := s.ClassSum(); got != 123 {
		t.Errorf("class sum = %d, want 123", got)
	}
	if s.Occupancy() != 100 {
		t.Errorf("occupancy = %d, want 100 under the subsumptive layout", s.Occupancy())
	}
}

func TestSampleFromTurnEvent(t *testing.T) {
	t.Parallel()
	prof, _ := profileFor(claudecode.Harness)
	ev := rtevents.Event{
		Event:            rtevents.KindTurnCompleted,
		Harness:          claudecode.Harness,
		HarnessSessionID: "sess-1",
		Data: rtevents.TurnData{Request: &rtevents.RequestUsage{
			RequestID: "msg_1", Model: "claude-fable-5", Layout: rtevents.LayoutAdditive,
			In: 331, CacheReadIn: 33479, CacheCreationIn: 326,
		}},
	}

	s, ok := sampleFromEvent(testCoords, ev, prof, "claude-fable-5[1m]")
	if !ok {
		t.Fatal("turn event with a Request produced no sample")
	}
	if s.Terminal {
		t.Error("a per-request sample must not be marked terminal")
	}
	if s.Cumulation != CumulationRequest {
		t.Errorf("cumulation = %q, want %q", s.Cumulation, CumulationRequest)
	}
	if s.Occupancy() != 34136 {
		t.Errorf("occupancy = %d, want 34136", s.Occupancy())
	}
	if s.HarnessSessionID != "sess-1" || s.AgentID != testCoords.AgentID {
		t.Errorf("identity not stamped: %+v", s)
	}
}

// A terminal sample is marked Terminal and its cumulation is SESSION, so
// the fold can exclude it structurally rather than by inspection.
func TestSampleFromSessionEndedIsTerminal(t *testing.T) {
	t.Parallel()
	prof, _ := profileFor(claudecode.Harness)
	ev := rtevents.Event{
		Event:   rtevents.KindSessionEnded,
		Harness: claudecode.Harness,
		Data: rtevents.SessionEndedData{
			Usage: rtevents.Usage{In: 11701, Out: 455},
			Metering: &rtevents.Metering{
				CacheReadIn: 72131, CacheCreationIn: 17162,
				ModelUsage: map[string]rtevents.ModelUsage{
					"claude-fable-5[1m]":        {ContextWindow: 1_000_000},
					"claude-haiku-4-5-20251001": {ContextWindow: 200_000},
				},
			},
		},
	}

	s, ok := sampleFromEvent(testCoords, ev, prof, "claude-fable-5[1m]")
	if !ok {
		t.Fatal("session-ended event produced no sample")
	}
	if !s.Terminal {
		t.Fatal("session-ended sample not marked terminal")
	}
	if s.Cumulation != CumulationSession {
		t.Errorf("cumulation = %q, want %q", s.Cumulation, CumulationSession)
	}
	if s.ClassSum() != 100994 {
		t.Errorf("class sum = %d, want 100994 (the session totals)", s.ClassSum())
	}
	// The window is selected by the primary model, never by ranging the
	// map: the wrong entry here is off by 5x and Go map order is random.
	if s.DeclaredLimit != 1_000_000 {
		t.Errorf("declared limit = %d, want 1000000 (the primary model's window)", s.DeclaredLimit)
	}
}

func TestSampleFromSessionEndedPicksNoWindowForAnUnknownPrimary(t *testing.T) {
	t.Parallel()
	prof, _ := profileFor(claudecode.Harness)
	ev := rtevents.Event{
		Event:   rtevents.KindSessionEnded,
		Harness: claudecode.Harness,
		Data: rtevents.SessionEndedData{
			Usage: rtevents.Usage{In: 100},
			Metering: &rtevents.Metering{ModelUsage: map[string]rtevents.ModelUsage{
				"claude-fable-5[1m]": {ContextWindow: 1_000_000},
			}},
		},
	}
	// No primary model known, so no entry may be picked. Guessing here is
	// how a 5x-wrong denominator gets in.
	s, ok := sampleFromEvent(testCoords, ev, prof, "")
	if !ok {
		t.Fatal("no sample")
	}
	if s.DeclaredLimit != 0 {
		t.Errorf("declared limit = %d, want 0 when the primary model is unknown", s.DeclaredLimit)
	}
}

func TestSampleFromEventRejectsEventsWithoutUsage(t *testing.T) {
	t.Parallel()
	prof, _ := profileFor(claudecode.Harness)
	for _, ev := range []rtevents.Event{
		{Event: rtevents.KindTurnCompleted, Harness: claudecode.Harness, Data: rtevents.TurnData{}},
		{Event: rtevents.KindToolCall, Harness: claudecode.Harness, Data: rtevents.ToolCallData{Tool: "Bash"}},
		{Event: rtevents.KindMessageCompleted, Harness: claudecode.Harness, Data: rtevents.MessageData{Role: "assistant"}},
		{Event: rtevents.KindError, Harness: claudecode.Harness, Data: rtevents.ErrorData{Kind: "parse"}},
		{Event: rtevents.KindSessionEnded, Harness: claudecode.Harness, Data: rtevents.SessionEndedData{ExitCode: 1}},
	} {
		if _, ok := sampleFromEvent(testCoords, ev, prof, ""); ok {
			t.Errorf("%s produced a sample despite carrying no usage", ev.Event)
		}
	}
}

func TestProfilesCoverEveryStreamCapableHarness(t *testing.T) {
	t.Parallel()
	// Cumulation is asserted per harness, not uniformly: codex is the one
	// harness whose stream reports running totals, and an assertion that
	// every harness reports levels is how that went unnoticed.
	want := map[string]Cumulation{
		claudecode.Harness: CumulationRequest,
		codex.Harness:      CumulationSession,
		opencode.Harness:   CumulationRequest,
	}
	for h, w := range want {
		p, ok := profileFor(h)
		if !ok {
			t.Errorf("harness %q has no profile; its samples would be ignored", h)
			continue
		}
		if p.cumulation != w {
			t.Errorf("harness %q cumulation = %q, want %q", h, p.cumulation, w)
		}
	}
	if _, ok := profileFor("crush"); ok {
		t.Error("crush has a profile, but it publishes no structured stream")
	}
	if _, ok := profileFor(""); ok {
		t.Error("the empty harness resolved to a profile")
	}
}

// Claude declares its window only on the terminal line, so the table (or
// the learned cache) is the live path even for the one harness that
// declares one at all. Pinned because the opposite reading is the natural
// assumption and it would leave Claude with no live denominator.
func TestClaudeWindowArrivesTooLateToServeALiveReading(t *testing.T) {
	t.Parallel()
	p, _ := profileFor(claudecode.Harness)
	if !p.limitInStream {
		t.Error("claude does declare its window in-stream")
	}
	if !p.limitArrivesAtEnd {
		t.Error("claude's window rides the terminal result line, so it cannot serve a live reading")
	}
	if c, _ := profileFor(codex.Harness); c.limitInStream {
		t.Error("codex declares no window in its exec stream")
	}
}
