package api

import (
	"testing"
	"time"
)

var costT0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func costRing(s Session) []float64 {
	var out []float64
	for _, c := range s.CostSamples {
		out = append(out, c.USD)
	}
	return out
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A rising total adds a sample per spacing, an unchanged total adds none, and a
// beat inside the spacing replaces the last value instead of growing the ring.
func TestRecordCostSampleRisingUnchangedAndClose(t *testing.T) {
	t.Parallel()
	var s Session
	recordCostSample(&s, 1.0, costT0)
	recordCostSample(&s, 1.0, costT0.Add(15*time.Second))
	if got := costRing(s); !sameFloats(got, []float64{1.0}) {
		t.Fatalf("an unchanged total stored %v, want [1]", got)
	}
	recordCostSample(&s, 1.2, costT0.Add(20*time.Second))
	if got := costRing(s); !sameFloats(got, []float64{1.2}) {
		t.Fatalf("a beat inside the spacing gave %v, want the last value replaced: [1.2]", got)
	}
	recordCostSample(&s, 1.5, costT0.Add(2*time.Minute))
	recordCostSample(&s, 1.9, costT0.Add(4*time.Minute))
	if got := costRing(s); !sameFloats(got, []float64{1.2, 1.5, 1.9}) {
		t.Fatalf("a rising total gave %v, want [1.2 1.5 1.9]", got)
	}
}

// A drop starts the ring again from the new value: a restart, /clear or a new
// harness session must never be read as a change across the reset.
func TestRecordCostSampleDropStartsAFreshBaseline(t *testing.T) {
	t.Parallel()
	var s Session
	recordCostSample(&s, 4.0, costT0)
	recordCostSample(&s, 4.5, costT0.Add(time.Minute))
	recordCostSample(&s, 0.2, costT0.Add(2*time.Minute))
	if got := costRing(s); !sameFloats(got, []float64{0.2}) {
		t.Fatalf("after a drop the ring is %v, want [0.2]", got)
	}
	if !s.CostSamples[0].At.Equal(costT0.Add(2 * time.Minute)) {
		t.Errorf("baseline At = %v, want the beat that showed the drop", s.CostSamples[0].At)
	}
	recordCostSample(&s, 0.7, costT0.Add(3*time.Minute))
	if got := costRing(s); !sameFloats(got, []float64{0.2, 0.7}) {
		t.Errorf("a rise after the reset gave %v, want [0.2 0.7]", got)
	}
}

// Old samples go, but one at or before the cutoff stays as the anchor for a
// window that starts there, and a busy seat cannot grow the ring past its cap.
func TestRecordCostSampleKeepsOneAnchorAndHonorsTheCap(t *testing.T) {
	t.Parallel()
	var s Session
	recordCostSample(&s, 1, costT0)
	recordCostSample(&s, 2, costT0.Add(10*time.Minute))
	recordCostSample(&s, 3, costT0.Add(CostSampleKeep+5*time.Minute))
	if got := costRing(s); !sameFloats(got, []float64{2, 3}) {
		t.Fatalf("after the keep age the ring is %v, want [2 3] (one anchor, then the new)", got)
	}

	var busy Session
	at := costT0
	for i := 1; i <= 1000; i++ {
		at = at.Add(costSampleSpacing)
		recordCostSample(&busy, float64(i), at)
	}
	if len(busy.CostSamples) > costSampleCap {
		t.Errorf("ring holds %d samples, cap is %d", len(busy.CostSamples), costSampleCap)
	}
	if last := busy.CostSamples[len(busy.CostSamples)-1].USD; last != 1000 {
		t.Errorf("last sample = %v, want 1000", last)
	}
}

// The heartbeat feeds the ring: a rising total, a drop, and a beat with the
// field missing. A missing field adds nothing and never reads as zero.
func TestHeartbeatFeedsTheCostRing(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, CostTotalUSD: f64(2)})
	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, CostTotalUSD: f64(2.5)})
	if len(got.CostSamples) == 0 || got.CostSamples[len(got.CostSamples)-1].USD != 2.5 {
		t.Fatalf("ring after two rising beats = %v, want it to end at 2.5", got.CostSamples)
	}

	missing := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 12})
	if len(missing.CostSamples) != len(got.CostSamples) {
		t.Errorf("a beat with no cost changed the ring: %v", missing.CostSamples)
	}

	dropped := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 13, CostTotalUSD: f64(0.1)})
	if len(dropped.CostSamples) != 1 || dropped.CostSamples[0].USD != 0.1 {
		t.Errorf("ring after a drop = %v, want one sample at 0.1", dropped.CostSamples)
	}

	bare := NewStore()
	bareToken := gradedSession(t, bare, 0)
	none := beatTotals(t, bare, bareToken, HeartbeatRequest{ContextPercent: 5})
	if len(none.CostSamples) != 0 {
		t.Errorf("a seat that never reported cost holds samples %v, want none", none.CostSamples)
	}
}
