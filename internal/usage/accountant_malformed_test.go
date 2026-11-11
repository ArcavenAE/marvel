package usage

import (
	"math"
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// A seat's own stream supplies every number the accountant folds. A negative
// class would lower the spend other sessions' admission reads, and an
// enormous one could overflow a later sum, so such a sample is rejected
// before any fold and counted.
func TestMalformedSampleIsRejectedAndCounted(t *testing.T) {
	t.Parallel()
	neg := -0.5
	huge := math.Inf(1)
	nan := math.NaN()
	justOver := math.Nextafter(maxCostUSD, 2*maxCostUSD)
	cases := map[string]rtevents.RequestUsage{
		"negative input":         {Layout: rtevents.LayoutSubsumptive, In: -1, Out: 10},
		"negative output":        {Layout: rtevents.LayoutSubsumptive, In: 10, Out: -5},
		"negative cache":         {Layout: rtevents.LayoutSubsumptive, In: 10, CacheReadIn: -3},
		"oversized class":        {Layout: rtevents.LayoutSubsumptive, In: 10, Out: maxClassTokens + 1},
		"negative cost":          {Layout: rtevents.LayoutSubsumptive, In: 10, Out: 5, Cost: &neg},
		"NaN cost":               {Layout: rtevents.LayoutSubsumptive, In: 10, Out: 5, Cost: &nan},
		"cost just over the cap": {Layout: rtevents.LayoutSubsumptive, In: 10, Out: 5, Cost: &justOver},
		"non-finite cost":        {Layout: rtevents.LayoutSubsumptive, In: 10, Out: 5, Cost: &huge},
		"negative total":         {Layout: rtevents.LayoutSubsumptive, In: 10, Out: 5, Total: -15},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a, _, _ := newTestAccountant(t, Table{})
			a.Observe(testCoords, turnEvent(claudecode.Harness, r))

			if s := a.Stats(); s.MalformedSamples != 1 || s.SamplesObserved != 0 {
				t.Fatalf("stats = malformed %d observed %d, want 1 and 0", s.MalformedSamples, s.SamplesObserved)
			}
			tt := a.TeamSpend(testCoords.Workspace, testCoords.Team)
			if tt.PromptTokens != 0 || tt.Out != 0 {
				t.Fatalf("spend = prompt %d out %d, want nothing folded from a rejected sample", tt.PromptTokens, tt.Out)
			}
		})
	}
}

// A sound sample is untouched, and the rejection never blocks the next one.
func TestMalformedSampleDoesNotBlockLaterSamples(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{Layout: rtevents.LayoutSubsumptive, In: -1}))
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{Layout: rtevents.LayoutSubsumptive, In: 100, Out: 20}))
	a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{Layout: rtevents.LayoutSubsumptive, Out: -2}))

	s := a.Stats()
	if s.MalformedSamples != 2 || s.SamplesObserved != 1 {
		t.Fatalf("stats = malformed %d observed %d, want 2 and 1", s.MalformedSamples, s.SamplesObserved)
	}
	if tt := a.TeamSpend(testCoords.Workspace, testCoords.Team); tt.PromptTokens != 100 || tt.Out != 20 {
		t.Fatalf("spend = prompt %d out %d, want 100 and 20", tt.PromptTokens, tt.Out)
	}
}

// The warning is once per session; the counter is every sample.
func TestMalformedSampleWarnsOncePerSession(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})
	for range 3 {
		a.Observe(testCoords, turnEvent(claudecode.Harness, rtevents.RequestUsage{Layout: rtevents.LayoutSubsumptive, In: -1}))
	}
	if s := a.Stats(); s.MalformedSamples != 3 {
		t.Fatalf("malformed = %d, want 3", s.MalformedSamples)
	}
	a.mu.Lock()
	_, warned := a.warned["malformed:"+testCoords.AgentID]
	a.mu.Unlock()
	if !warned {
		t.Fatal("no malformed warning was recorded for the session")
	}
}

func TestSampleMalformedBounds(t *testing.T) {
	t.Parallel()
	ok := 12.5
	atCap := maxCostUSD
	for name, s := range map[string]Sample{
		"zero":             {},
		"at the class cap": {In: maxClassTokens, Out: maxClassTokens},
		"plain cost":       {In: 5, CostUSD: &ok},
		"cost at the cap":  {In: 5, CostUSD: &atCap},
	} {
		if why := s.Malformed(); why != "" {
			t.Errorf("%s: Malformed() = %q, want a sound sample", name, why)
		}
	}
}

// A session-end sample goes through the same check: its totals feed spend
// and reconciliation, so a negative one must not reach them either.
func TestMalformedTerminalSampleIsRejectedAndCounted(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAccountant(t, Table{})

	a.Observe(testCoords, endedEvent(rtevents.Usage{In: -5, Out: 10}, nil))

	if s := a.Stats(); s.MalformedSamples != 1 {
		t.Fatalf("malformed = %d, want 1", s.MalformedSamples)
	}
	if tt := a.TeamSpend(testCoords.Workspace, testCoords.Team); tt.PromptTokens != 0 || tt.Out != 0 {
		t.Fatalf("spend = prompt %d out %d, want nothing folded from a rejected terminal sample", tt.PromptTokens, tt.Out)
	}
}
