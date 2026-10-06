package usage

import (
	"math"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// settableClock lets a test place samples at chosen times.
type settableClock struct{ now time.Time }

func (c *settableClock) Now() time.Time { return c.now }

func newRateAccountant(t *testing.T) (*Accountant, *recordSink, *settableClock) {
	t.Helper()
	sink := newRecordSink()
	clk := &settableClock{now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	return New(sink, NewResolver(Table{}), WithClock(clk.Now)), sink, clk
}

func outEvent(out int) rtevents.Event {
	r := additive(1000, 0, 0)
	r.Out = out
	return turnEvent(claudecode.Harness, r)
}

func rateAt(t *testing.T, a *Accountant, at time.Time) float64 {
	t.Helper()
	r, ok := a.OutRate(testCoords.AgentID, at)
	if !ok {
		t.Fatalf("OutRate at %s: never sampled, want a rate", at)
	}
	return r
}

// A stream that stops loses its rate on its own: half after one half-life,
// next to nothing after many, and never frozen at its last value. The
// reading stays a number (0-ish), not absent.
func TestOutRateDecaysToZeroWhenSamplesStop(t *testing.T) {
	t.Parallel()
	a, _, clk := newRateAccountant(t)
	t0 := clk.now
	a.Observe(testCoords, outEvent(1000))

	r0 := rateAt(t, a, t0)
	if r0 <= 0 {
		t.Fatalf("rate at the sample = %v, want positive", r0)
	}
	if got := rateAt(t, a, t0.Add(OutRateHalfLife)); math.Abs(got-r0/2) > r0*0.01 {
		t.Errorf("rate after one half-life = %v, want about %v", got, r0/2)
	}
	prev := r0
	for _, n := range []int{1, 2, 5, 10, 20} {
		got := rateAt(t, a, t0.Add(time.Duration(n)*OutRateHalfLife))
		if got > prev || got < 0 {
			t.Errorf("rate after %d half-lives = %v, want it non-negative and no higher than %v", n, got, prev)
		}
		prev = got
	}
	if prev > r0/1e5 {
		t.Errorf("rate after 20 half-lives = %v, want it near zero (below %v)", prev, r0/1e5)
	}
}

// A session nobody has sampled has no rate, and that is not a rate of 0.
func TestRateNilWhenNeverSampled(t *testing.T) {
	t.Parallel()
	a, sink, clk := newRateAccountant(t)

	if _, ok := a.OutRate("ws/never-seen", clk.now); ok {
		t.Error("an unknown session reported a rate")
	}

	// A session start names a model and spends nothing: no sample, no rate,
	// and no reading to carry one.
	a.Observe(testCoords, startedEvent("claude-opus-4-8"))
	if _, ok := a.OutRate(testCoords.AgentID, clk.now); ok {
		t.Error("a session that only started reported a rate")
	}
	if got, ok := sink.get(testCoords.AgentID); ok && !got.OutRate.ObservedAt.IsZero() {
		t.Errorf("a reading before any sample carries an observed rate %+v", got.OutRate)
	}

	a.Observe(testCoords, outEvent(0))
	if _, ok := a.OutRate(testCoords.AgentID, clk.now); !ok {
		t.Error("a first sample with no output left the rate absent; it is a measured 0")
	}
}

// The rate is a decayed level, not the delta of the last two samples: two
// large samples a millisecond apart do not read as a million tokens a
// second, and a steady stream reads as its own throughput.
func TestRateNotInstantaneousDelta(t *testing.T) {
	t.Parallel()
	a, _, clk := newRateAccountant(t)
	a.Observe(testCoords, outEvent(1000))
	clk.now = clk.now.Add(time.Millisecond)
	a.Observe(testCoords, outEvent(1000))
	if got := rateAt(t, a, clk.now); got <= 0 || got > 200 {
		t.Errorf("rate after 2000 tokens in 1ms = %v t/s, want a smoothed figure under 200 (the delta would be 2000000)", got)
	}

	b, _, bclk := newRateAccountant(t)
	for i := 0; i < 300; i++ {
		b.Observe(testCoords, outEvent(100))
		bclk.now = bclk.now.Add(time.Second)
	}
	if got := rateAt(t, b, bclk.now); math.Abs(got-100) > 3 {
		t.Errorf("steady 100 tokens a second reads %v t/s, want about 100", got)
	}
}

// The reading the accountant writes carries the rate with its as-of stamp,
// and the stamp expires: a zero valid_until would mean never stale.
func TestOutRateRidesTheReading(t *testing.T) {
	t.Parallel()
	a, sink, clk := newRateAccountant(t)
	a.Observe(testCoords, outEvent(1000))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	cell := got.OutRate
	if !cell.ObservedAt.Equal(clk.now) {
		t.Errorf("observed_at = %v, want the sample time %v", cell.ObservedAt, clk.now)
	}
	if want := clk.now.Add(OutRateValidFor); !cell.ValidUntil.Equal(want) {
		t.Errorf("valid_until = %v, want %v", cell.ValidUntil, want)
	}
	if want := rateAt(t, a, clk.now); math.Abs(cell.Value-want) > 1e-9 {
		t.Errorf("value = %v, want the accountant's own rate %v", cell.Value, want)
	}
}

// A feed that reports running totals (codex) rates the new tokens, not the
// total again: two turns of 1000 read the same as an additive feed's.
func TestCumulativeFeedRatesTheDelta(t *testing.T) {
	t.Parallel()
	cum, _, cclk := newRateAccountant(t)
	cum.Observe(testCoords, codexStarted())
	for _, total := range []int{1000, 2000} {
		r := rtevents.RequestUsage{Layout: rtevents.LayoutSubsumptive, In: 5000, Out: total}
		cum.Observe(testCoords, turnEvent(codex.Harness, r))
		cclk.now = cclk.now.Add(time.Second)
	}
	add, _, aclk := newRateAccountant(t)
	for i := 0; i < 2; i++ {
		add.Observe(testCoords, outEvent(1000))
		aclk.now = aclk.now.Add(time.Second)
	}
	got, want := rateAt(t, cum, cclk.now), rateAt(t, add, aclk.now)
	if math.Abs(got-want) > want*0.001 {
		t.Errorf("cumulative feed rate = %v, additive feed rate = %v, want them equal", got, want)
	}
}

// A renderer ages a stored cell with DecayedRate, and gets the figure the
// accountant itself would report at that time.
func TestDecayedRateMatchesTheAccountant(t *testing.T) {
	t.Parallel()
	a, sink, clk := newRateAccountant(t)
	a.Observe(testCoords, outEvent(1000))
	cell := mustReading(t, sink).OutRate

	later := clk.now.Add(3 * OutRateHalfLife)
	if got, want := DecayedRate(cell, later), rateAt(t, a, later); math.Abs(got-want) > 1e-9 {
		t.Errorf("DecayedRate = %v, accountant = %v, want them equal", got, want)
	}
	if got := DecayedRate(cell, cell.ObservedAt); math.Abs(got-cell.Value) > 1e-12 {
		t.Errorf("DecayedRate at the observation = %v, want the stored %v", got, cell.Value)
	}
	if got := DecayedRate(cell, cell.ObservedAt.Add(-time.Hour)); got != cell.Value {
		t.Errorf("DecayedRate before the observation = %v, want the stored %v (a clock that stepped back decays nothing)", got, cell.Value)
	}
}

// A cumulative total that falls (the harness restarted its count) is not
// negative output: the rate never goes below zero.
func TestCumulativeTotalFallingNeverGivesANegativeRate(t *testing.T) {
	t.Parallel()
	a, _, clk := newRateAccountant(t)
	for _, total := range []int{5000, 100} {
		r := rtevents.RequestUsage{Layout: rtevents.LayoutSubsumptive, In: 5000, Out: total}
		a.Observe(testCoords, turnEvent(codex.Harness, r))
		clk.now = clk.now.Add(time.Second)
	}
	if got := rateAt(t, a, clk.now); got < 0 {
		t.Errorf("rate = %v after the total fell, want it non-negative", got)
	}
}

// A sample stamped before the previous one (the clock stepped back) adds its
// tokens without inflating the count: the rate stays finite and the
// observation time does not move backwards.
func TestRateSurvivesAClockThatStepsBack(t *testing.T) {
	t.Parallel()
	a, sink, clk := newRateAccountant(t)
	a.Observe(testCoords, outEvent(1000))
	first := clk.now
	clk.now = clk.now.Add(-time.Hour)
	a.Observe(testCoords, outEvent(1000))

	cell := mustReading(t, sink).OutRate
	if cell.ObservedAt.Before(first) {
		t.Errorf("observed_at = %v moved before the earlier sample %v", cell.ObservedAt, first)
	}
	if got := rateAt(t, a, first); math.IsInf(got, 0) || math.IsNaN(got) || got <= 0 || got > 200 {
		t.Errorf("rate = %v after a backwards sample, want a finite figure for 2000 tokens", got)
	}
}

func mustReading(t *testing.T, sink *recordSink) api.SessionContext {
	t.Helper()
	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	return got
}

// codexStarted is codex's session-start event. It names no model, which is
// all the accountant needs to know the stream began under this daemon.
func codexStarted() rtevents.Event {
	return rtevents.Event{
		SchemaVersion: rtevents.SchemaVersion,
		Event:         rtevents.KindSessionStarted,
		Harness:       codex.Harness,
		Data:          rtevents.SessionStartedData{},
	}
}

func codexTotal(out int) rtevents.Event {
	return turnEvent(codex.Harness, rtevents.RequestUsage{Layout: rtevents.LayoutSubsumptive, In: 5000, Out: out})
}

// A daemon restart or reexec leaves the accountant fresh while a codex
// session keeps running, so its first sample is a running total of output
// produced before this accountant existed. That total is a baseline, not a
// burst: a 500k total read as 17k tokens a second at once, and still 271 two
// minutes on, and made a real 20 tokens-a-second turn read 12k.
func TestFreshAccountantFedALargeCumulativeTotalReportsNoBurst(t *testing.T) {
	t.Parallel()
	a, _, clk := newRateAccountant(t)
	a.Observe(testCoords, codexTotal(500_000))
	if got := rateAt(t, a, clk.now); got != 0 {
		t.Errorf("rate at the first cumulative total = %v t/s, want 0 (a baseline, measured)", got)
	}

	clk.now = clk.now.Add(time.Second)
	a.Observe(testCoords, codexTotal(500_020))
	if got := rateAt(t, a, clk.now); got <= 0 || got > 5 {
		t.Errorf("rate after a real 20 token turn = %v t/s, want a small figure under 5", got)
	}
}

// A session that began under this daemon starts from zero, so its first
// total is all new output and counts.
func TestStartedSessionsFirstCumulativeTotalCounts(t *testing.T) {
	t.Parallel()
	a, _, clk := newRateAccountant(t)
	a.Observe(testCoords, codexStarted())
	a.Observe(testCoords, codexTotal(1000))
	if got := rateAt(t, a, clk.now); got <= 0 {
		t.Errorf("rate after the first total of a started session = %v, want positive", got)
	}
}

// The renderer must look at ObservedAt: DecayedRate says 0 for a cell that
// was never sampled, which is the same figure as a measured quiet stream.
func TestDecayedRateOfAnUnsampledCellIsZeroAndIndistinguishable(t *testing.T) {
	t.Parallel()
	var unsampled asof.Cell[float64]
	if got := DecayedRate(unsampled, time.Now()); got != 0 {
		t.Errorf("DecayedRate of a never-sampled cell = %v, want 0", got)
	}
}
