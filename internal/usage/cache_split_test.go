package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

// I-477 slice 2, the accountant half (card i477c1113-1): the spend slice splits
// the prompt into fresh input, cache reads and cache writes, so that
// TIN + CR + CW = PROMPT on every harness, and a class the harness never
// reports stays nil rather than 0.

// splitOf reads the four prompt figures a seat's spend carries, from the reading
// the accountant wrote or, for a feed with no occupancy, from its spend-only
// write.
func splitOf(t *testing.T, sink *recordSink) (fresh, cacheRead, cacheWrite, prompt *int) {
	t.Helper()
	if c, ok := sink.get(testCoords.AgentID); ok {
		return c.SpendFresh, c.SpendCacheRead, c.SpendCacheWrite, c.SpendPromptTokens
	}
	sp, ok := sink.getSpend(testCoords.AgentID)
	if !ok {
		t.Fatal("the accountant wrote neither a reading nor a spend")
	}
	return sp.Fresh, sp.CacheRead, sp.CacheWrite, sp.PromptTokens
}

func want(t *testing.T, name string, got *int, w int) {
	t.Helper()
	if got == nil || *got != w {
		t.Errorf("%s = %v, want %d", name, deref(got), w)
	}
}

// Every harness's feed ends in the same identity: fresh input plus cache reads
// plus cache writes is the prompt. Claude and opencode report the classes
// beside each other (additive); codex counts cached input inside input
// (subsumptive), so its fresh input is the input less the cached part.
func TestTinPlusCacheEqualsPromptAllHarnesses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		harness string
		feed    []rtevents.RequestUsage
		fresh   int
		read    int
		write   int
		prompt  int
	}{
		{
			name: "claude", harness: claudecode.Harness,
			feed:  []rtevents.RequestUsage{additive(1000, 400, 100), additive(2000, 900, 0)},
			fresh: 3000, read: 1300, write: 100, prompt: 4400,
		},
		{
			name: "opencode", harness: opencode.Harness,
			feed:  []rtevents.RequestUsage{additive(500, 200, 50), additive(700, 300, 25)},
			fresh: 1200, read: 500, write: 75, prompt: 1775,
		},
		{
			// codex totals replace each other: the second is the session so far.
			name: "codex", harness: codex.Harness,
			feed: []rtevents.RequestUsage{
				{Layout: rtevents.LayoutSubsumptive, In: 14005, CacheReadIn: 11008, Out: 71},
				{Layout: rtevents.LayoutSubsumptive, In: 28110, CacheReadIn: 24064, Out: 76},
			},
			fresh: 4046, read: 24064, write: 0, prompt: 28110,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, sink, _ := newTestAccountant(t, Table{"gpt-5.6-sol": 258_400})
			a.Bind(testCoords, Bind{Harness: tc.harness, Args: []string{"-m", "gpt-5.6-sol"}})
			for _, r := range tc.feed {
				a.Observe(testCoords, turnEvent(tc.harness, r))
			}
			fresh, read, write, prompt := splitOf(t, sink)
			want(t, "TIN", fresh, tc.fresh)
			want(t, "CR", read, tc.read)
			want(t, "PROMPT", prompt, tc.prompt)
			want(t, "CW", write, tc.write)
			if fresh != nil && read != nil && write != nil && prompt != nil {
				if sum := *fresh + *read + *write; sum != *prompt {
					t.Errorf("TIN + CR + CW = %d, PROMPT = %d, want them equal", sum, *prompt)
				}
			}
		})
	}
}

// Codex counts cached input inside input_tokens, so the figure the harness
// calls input is not fresh input. TIN is the input less the cached part, and the
// cached part is CR.
func TestCodexTinIsInputMinusCached(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{"gpt-5.6-sol": 258_400})
	a.Bind(testCoords, Bind{Harness: codex.Harness, Args: []string{"-m", "gpt-5.6-sol"}})
	a.Observe(testCoords, turnEvent(codex.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 14005, CacheReadIn: 11008, Out: 71,
	}))
	fresh, read, write, prompt := splitOf(t, sink)
	want(t, "TIN", fresh, 14005-11008)
	want(t, "CR", read, 11008)
	want(t, "PROMPT", prompt, 14005)
	// Codex sends cache_write_input_tokens (0 on every capture), so CW is a
	// metered zero and prints 0, not the dash of a class nobody reported.
	want(t, "CW", write, 0)
	// The spend the accountant reports in process carries the same split.
	sp, _ := a.SessionSpend(testCoords.AgentID)
	if sp.FreshIn != 14005-11008 {
		t.Errorf("SessionSpend.FreshIn = %d, want %d", sp.FreshIn, 14005-11008)
	}
}

// The codex parser reads cache_write_input_tokens into CacheCreationIn, so a
// codex seat's CW is that figure, not an unreported class (review of #1445, low
// 1). Nothing here asserts TIN + CR + CW = PROMPT for a nonzero write: whether
// codex counts a cache write inside input_tokens is not measured, every capture
// carries 0.
func TestCodexCwReportsTheParsersCacheWrite(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{"gpt-5.6-sol": 258_400})
	a.Bind(testCoords, Bind{Harness: codex.Harness, Args: []string{"-m", "gpt-5.6-sol"}})
	a.Observe(testCoords, turnEvent(codex.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 14005, CacheReadIn: 11008, CacheCreationIn: 500, Out: 71,
	}))
	_, _, write, _ := splitOf(t, sink)
	want(t, "CW", write, 500)
}

// A codex sample whose cached input exceeds its input cannot come from a
// well-formed feed (cached input is part of input). It must not break
// TIN + CR + CW = PROMPT without a sign: CR is capped at the input, so the
// three classes still add up to the prompt (review of #1445, low 2). The sample
// is still counted, so its output tokens are not lost.
func TestCodexCachedAboveInputKeepsTheIdentity(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{"gpt-5.6-sol": 258_400})
	a.Bind(testCoords, Bind{Harness: codex.Harness, Args: []string{"-m", "gpt-5.6-sol"}})
	a.Observe(testCoords, turnEvent(codex.Harness, rtevents.RequestUsage{
		Layout: rtevents.LayoutSubsumptive, In: 100, CacheReadIn: 150, Out: 7,
	}))
	fresh, read, write, prompt := splitOf(t, sink)
	want(t, "PROMPT", prompt, 100)
	want(t, "TIN", fresh, 0)
	want(t, "CR", read, 100)
	want(t, "CW", write, 0)
	if fresh != nil && read != nil && write != nil && prompt != nil {
		if sum := *fresh + *read + *write; sum != *prompt {
			t.Errorf("TIN + CR + CW = %d, PROMPT = %d, want them equal", sum, *prompt)
		}
	}
	sp, _ := a.SessionSpend(testCoords.AgentID)
	if sp.CacheReadIn != 100 || sp.Out != 7 {
		t.Errorf("SessionSpend CacheReadIn = %d, Out = %d, want 100 and 7: the sample still counts", sp.CacheReadIn, sp.Out)
	}
}
