package timing

import (
	"sync"
	"testing"
	"time"
)

var wantBounds = []time.Duration{
	1 * time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond,
	10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond,
	1 * time.Second, 2 * time.Second, 5 * time.Second,
	10 * time.Second, 30 * time.Second,
}

func TestBoundsAreLogSpacedFrom1msTo30s(t *testing.T) {
	t.Parallel()
	got := Bounds()
	if len(got) != len(wantBounds) {
		t.Fatalf("Bounds() has %d entries, want %d (1ms to 30s)", len(got), len(wantBounds))
	}
	for i, w := range wantBounds {
		if got[i] != w {
			t.Errorf("Bounds()[%d] = %s, want %s", i, got[i], w)
		}
	}
}

func TestBoundsReturnsACopy(t *testing.T) {
	t.Parallel()
	b := Bounds()
	if len(b) == 0 {
		t.Fatal("Bounds() is empty")
	}
	b[0] = time.Hour
	if again := Bounds(); again[0] != time.Millisecond {
		t.Errorf("a caller's write changed the shared bounds: Bounds()[0] = %s", again[0])
	}
}

// where returns the index of the one bucket holding a single observation, or
// len(Buckets) for the overflow count, or -1 when the counts are not exactly
// one in exactly one place.
func where(s Snapshot) int {
	idx, total := -1, uint64(0)
	for i, b := range s.Buckets {
		total += b.Count
		if b.Count == 1 {
			idx = i
		}
	}
	total += s.Overflow
	if s.Overflow == 1 {
		idx = len(s.Buckets)
	}
	if total != 1 {
		return -1
	}
	return idx
}

func TestObservePlacesADurationAtTheBucketEdges(t *testing.T) {
	t.Parallel()
	last := len(wantBounds) - 1
	type tc struct {
		name string
		d    time.Duration
		want int
	}
	cases := []tc{
		{"zero", 0, 0},
		{"negative", -time.Second, 0},
		{"under the first bound", 500 * time.Microsecond, 0},
		{"just past the last bound", wantBounds[last] + time.Nanosecond, last + 1},
		{"an hour", time.Hour, last + 1},
	}
	for i, b := range wantBounds {
		cases = append(cases,
			tc{"on bound " + b.String(), b, i},
			tc{"1ns past bound " + b.String(), b + time.Nanosecond, i + 1},
		)
	}
	for _, c := range cases {
		h := NewHistogram(0)
		h.Observe(c.d)
		s := h.Snapshot()
		if len(s.Buckets) != len(wantBounds) {
			t.Fatalf("%s: Snapshot has %d buckets, want %d", c.name, len(s.Buckets), len(wantBounds))
		}
		if got := where(s); got != c.want {
			t.Errorf("%s: observation landed in bucket %d, want %d (%d means the overflow)", c.name, got, c.want, len(wantBounds))
		}
	}
}

func TestSnapshotBucketsCarryTheirBounds(t *testing.T) {
	t.Parallel()
	s := NewHistogram(0).Snapshot()
	if len(s.Buckets) != len(wantBounds) {
		t.Fatalf("Snapshot has %d buckets, want %d", len(s.Buckets), len(wantBounds))
	}
	for i, w := range wantBounds {
		if s.Buckets[i].UpperBound != w {
			t.Errorf("Buckets[%d].UpperBound = %s, want %s", i, s.Buckets[i].UpperBound, w)
		}
	}
}

func TestCountSumAndMax(t *testing.T) {
	t.Parallel()
	h := NewHistogram(0)
	for _, d := range []time.Duration{3 * time.Millisecond, 40 * time.Millisecond, 7 * time.Millisecond} {
		h.Observe(d)
	}
	s := h.Snapshot()
	if s.Count != 3 {
		t.Errorf("Count = %d, want 3", s.Count)
	}
	if s.Sum != 50*time.Millisecond {
		t.Errorf("Sum = %s, want 50ms", s.Sum)
	}
	if s.Max != 40*time.Millisecond {
		t.Errorf("Max = %s, want 40ms (a later, shorter call must not lower it)", s.Max)
	}
}

func TestANegativeDurationAddsNothingToTheSum(t *testing.T) {
	t.Parallel()
	h := NewHistogram(0)
	h.Observe(-time.Second)
	h.Observe(2 * time.Millisecond)
	s := h.Snapshot()
	if s.Count != 2 || s.Sum != 2*time.Millisecond || s.Max != 2*time.Millisecond {
		t.Errorf("Count/Sum/Max = %d/%s/%s, want 2/2ms/2ms (a clock step back counts as zero)", s.Count, s.Sum, s.Max)
	}
}

func TestOverrunIsStrictlyOverTheBudget(t *testing.T) {
	t.Parallel()
	h := NewHistogram(500 * time.Millisecond)
	if h.Observe(500 * time.Millisecond) {
		t.Error("Observe(budget) reported an overrun; only a longer call is one")
	}
	if !h.Observe(501 * time.Millisecond) {
		t.Error("Observe(budget+1ms) did not report an overrun")
	}
	if !h.Observe(time.Minute) {
		t.Error("Observe(1m) did not report an overrun")
	}
	s := h.Snapshot()
	if s.Overruns != 2 {
		t.Errorf("Overruns = %d, want 2", s.Overruns)
	}
	if s.Count != 3 {
		t.Errorf("Count = %d, want 3: an overrun is still an observation", s.Count)
	}
}

func TestAZeroBudgetNeverOverruns(t *testing.T) {
	t.Parallel()
	h := NewHistogram(0)
	if h.Observe(time.Hour) {
		t.Error("a histogram with no budget reported an overrun")
	}
	s := h.Snapshot()
	if s.Overruns != 0 || s.Count != 1 || s.Overflow != 1 {
		t.Errorf("Overruns/Count/Overflow = %d/%d/%d, want 0/1/1", s.Overruns, s.Count, s.Overflow)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	t.Parallel()
	h := NewHistogram(0)
	h.Observe(time.Millisecond)
	s := h.Snapshot()
	if len(s.Buckets) == 0 {
		t.Fatal("Snapshot has no buckets")
	}
	s.Buckets[0].Count = 99
	if got := h.Snapshot().Buckets[0].Count; got != 1 {
		t.Errorf("a write to a Snapshot changed the histogram: Buckets[0].Count = %d, want 1", got)
	}
}

func TestConcurrentObservesAreAllCounted(t *testing.T) {
	t.Parallel()
	const workers, each = 8, 1000
	h := NewHistogram(time.Millisecond)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				h.Observe(2 * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	s := h.Snapshot()
	if s.Count != workers*each || s.Overruns != workers*each {
		t.Errorf("Count/Overruns = %d/%d, want %d/%d", s.Count, s.Overruns, workers*each, workers*each)
	}
	if s.Sum != workers*each*2*time.Millisecond {
		t.Errorf("Sum = %s, want %s", s.Sum, workers*each*2*time.Millisecond)
	}
}

// fakeClock is the throttle's injected clock; no test here sleeps.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)} }

func TestOverrunEventIntervalIsTenMinutes(t *testing.T) {
	t.Parallel()
	if OverrunEventInterval != 10*time.Minute {
		t.Errorf("OverrunEventInterval = %s, want 10m (ruling i451c1113-2)", OverrunEventInterval)
	}
}

func TestThrottleLetsTheFirstCallThrough(t *testing.T) {
	t.Parallel()
	clk := newClock()
	th := NewThrottle(OverrunEventInterval, clk.now)
	ok, suppressed := th.Allow("reconcile")
	if !ok || suppressed != 0 {
		t.Errorf("first Allow = (%v, %d), want (true, 0)", ok, suppressed)
	}
}

func TestThrottleHoldsBackCallsInsideTheInterval(t *testing.T) {
	t.Parallel()
	clk := newClock()
	th := NewThrottle(OverrunEventInterval, clk.now)
	if ok, _ := th.Allow("reconcile"); !ok {
		t.Fatal("the first call was held back")
	}
	clk.advance(OverrunEventInterval - time.Nanosecond)
	if ok, _ := th.Allow("reconcile"); ok {
		t.Error("Allow just inside the interval let a second event through")
	}
}

func TestThrottleReportsTheSuppressedCountAtTheNextEmit(t *testing.T) {
	t.Parallel()
	clk := newClock()
	th := NewThrottle(OverrunEventInterval, clk.now)
	th.Allow("reconcile")
	for range 3 {
		clk.advance(time.Minute)
		if ok, _ := th.Allow("reconcile"); ok {
			t.Fatal("a call inside the interval was allowed")
		}
	}
	clk.advance(OverrunEventInterval)
	ok, suppressed := th.Allow("reconcile")
	if !ok || suppressed != 3 {
		t.Errorf("Allow after the interval = (%v, %d), want (true, 3)", ok, suppressed)
	}
	// The count starts over after an emit.
	clk.advance(time.Minute)
	th.Allow("reconcile")
	clk.advance(OverrunEventInterval)
	ok, suppressed = th.Allow("reconcile")
	if !ok || suppressed != 1 {
		t.Errorf("next emit = (%v, %d), want (true, 1): the count must reset after each emit", ok, suppressed)
	}
}

func TestThrottleAllowsExactlyOneIntervalLater(t *testing.T) {
	t.Parallel()
	clk := newClock()
	th := NewThrottle(OverrunEventInterval, clk.now)
	th.Allow("tmux")
	clk.advance(OverrunEventInterval)
	if ok, _ := th.Allow("tmux"); !ok {
		t.Error("Allow exactly one interval after the last emit was held back")
	}
}

func TestThrottleKeepsEachSiteSeparate(t *testing.T) {
	t.Parallel()
	clk := newClock()
	th := NewThrottle(OverrunEventInterval, clk.now)
	th.Allow("reconcile")
	th.Allow("reconcile")
	if ok, s := th.Allow("rpc/apply"); !ok || s != 0 {
		t.Errorf("first Allow for a second site = (%v, %d), want (true, 0): one site's emit must not hold another's", ok, s)
	}
	clk.advance(OverrunEventInterval)
	if ok, s := th.Allow("reconcile"); !ok || s != 1 {
		t.Errorf("reconcile after the interval = (%v, %d), want (true, 1): the other site's calls must not be counted here", ok, s)
	}
}

func TestThrottleIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()
	clk := newClock()
	th := NewThrottle(OverrunEventInterval, clk.now)
	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if ok, _ := th.Allow("reconcile"); ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if allowed != 1 {
		t.Errorf("%d concurrent callers were allowed in one interval, want exactly 1", allowed)
	}
}
