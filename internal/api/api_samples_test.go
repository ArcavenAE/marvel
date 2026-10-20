package api

import (
	"testing"
	"time"
)

func apiRing(s Session) []int64 {
	var out []int64
	for _, c := range s.APISamples {
		out = append(out, c.MS)
	}
	return out
}

func sameInts(a, b []int64) bool {
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

// holdAPI feeds unchanged beats every minute for d after from, so the feed
// stays alive while the total is flat.
func holdAPI(s *Session, ms int64, from time.Time, d time.Duration) {
	for at := from.Add(time.Minute); at.Before(from.Add(d)); at = at.Add(time.Minute) {
		recordAPISample(s, ms, at)
	}
}

// The API-time ring follows the cost ring's rules: a rising total adds a sample
// per spacing, an unchanged total adds none, a beat inside the spacing replaces
// the last value, and a lower total starts the ring over.
func TestRecordAPISampleRisingUnchangedCloseAndReset(t *testing.T) {
	t.Parallel()
	var s Session
	recordAPISample(&s, 1000, costT0)
	recordAPISample(&s, 1000, costT0.Add(15*time.Second))
	if got := apiRing(s); !sameInts(got, []int64{1000}) {
		t.Fatalf("ring after an unchanged beat = %v, want [1000]", got)
	}
	recordAPISample(&s, 4000, costT0.Add(time.Minute))
	recordAPISample(&s, 4500, costT0.Add(time.Minute+10*time.Second))
	if got := apiRing(s); !sameInts(got, []int64{1000, 4500}) {
		t.Fatalf("ring after a rise and a close beat = %v, want [1000 4500]", got)
	}
	recordAPISample(&s, 200, costT0.Add(2*time.Minute))
	if got := apiRing(s); !sameInts(got, []int64{200}) {
		t.Fatalf("ring after the total dropped = %v, want [200]", got)
	}
}

// A silent feed is not a flat run: 40 minutes of silence starts the ring over
// and stamps the gap, while 40 minutes of unchanged beats keeps its anchor.
func TestRecordAPISampleGapAndFlatRun(t *testing.T) {
	t.Parallel()
	var gap Session
	recordAPISample(&gap, 1000, costT0)
	recordAPISample(&gap, 90000, costT0.Add(40*time.Minute))
	if got := apiRing(gap); !sameInts(got, []int64{90000}) || gap.APIGapAt.IsZero() {
		t.Errorf("after a 40 minute gap ring = %v gap at %v, want [90000] with the gap stamped", got, gap.APIGapAt)
	}
	var flat Session
	recordAPISample(&flat, 1000, costT0)
	holdAPI(&flat, 1000, costT0, 40*time.Minute)
	recordAPISample(&flat, 1500, costT0.Add(40*time.Minute))
	if got := apiRing(flat); !sameInts(got, []int64{1000, 1500}) || !flat.APIGapAt.IsZero() {
		t.Errorf("after a flat run ring = %v gap at %v, want [1000 1500] and no gap", got, flat.APIGapAt)
	}
}

// The heartbeat feeds the ring, and a beat with no API total adds nothing.
func TestHeartbeatFeedsTheAPIRing(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)
	ms := func(v int64) *int64 { return &v }

	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, APIDurationMS: ms(1000)})
	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, APIDurationMS: ms(9000)})
	if n := len(got.APISamples); n == 0 || got.APISamples[n-1].MS != 9000 {
		t.Fatalf("ring after two rising beats = %v, want it to end at 9000", got.APISamples)
	}
	missing := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 12})
	if len(missing.APISamples) != len(got.APISamples) {
		t.Errorf("a beat with no API total changed the ring: %v", missing.APISamples)
	}
	bare := NewStore()
	none := beatTotals(t, bare, gradedSession(t, bare, 0), HeartbeatRequest{ContextPercent: 5})
	if len(none.APISamples) != 0 {
		t.Errorf("a seat that never reported API time holds samples %v, want none", none.APISamples)
	}
}

func apiSeat(total int64, beat time.Duration, samples ...APISample) Session {
	s := Session{Name: "squad-worker-g1-0", Workspace: "ws", Team: "squad", Role: "worker", State: SessionRunning}
	s.APIDurationMS = &total
	s.APISamples = samples
	s.APISeenAt = costT0.Add(-beat)
	return s
}

func apiAt(ago time.Duration, ms int64) APISample {
	return APISample{At: costT0.Add(-ago), MS: ms}
}

// ACTIVE% for a feed seat is the API-time increase over 15 minutes, as a share
// of those 15 minutes, held to 0..100, with the dash and ? states of the RATE
// cell.
func TestAPIActivePct(t *testing.T) {
	t.Parallel()
	gap := apiSeat(1_090_000, 15*time.Second, apiAt(16*time.Minute, 1_000_000))
	gap.APIGapAt = costT0.Add(-5 * time.Minute)
	for _, tc := range []struct {
		name string
		s    Session
		pct  float64
		kind APIActivity
	}{
		{"rising", apiSeat(1_090_000, 15*time.Second, apiAt(16*time.Minute, 1_000_000)), 10, APIActivityReading},
		{"anchor is the newest sample at or before the window start", apiSeat(1_090_000, 15*time.Second,
			apiAt(40*time.Minute, 100_000), apiAt(16*time.Minute, 1_000_000), apiAt(5*time.Minute, 1_050_000)), 10, APIActivityReading},
		{"flat", apiSeat(1_000_000, 15*time.Second, apiAt(30*time.Minute, 1_000_000)), 0, APIActivityReading},
		{"clamped", apiSeat(5_000_000, 15*time.Second, apiAt(16*time.Minute, 1_000_000)), 100, APIActivityReading},
		{"fresh reset", apiSeat(6000, 15*time.Second, apiAt(2*time.Minute, 5000)), 0, APIActivityPending},
		{"no samples", apiSeat(6000, 15*time.Second), 0, APIActivityPending},
		{"silent", apiSeat(1_090_000, 20*time.Minute, apiAt(40*time.Minute, 1_000_000)), 0, APIActivityStale},
		{"gap in window", gap, 0, APIActivityStale},
		{"no feed", Session{Name: "n"}, 0, APIActivityNone},
	} {
		pct, kind := APIActivePct(tc.s, costT0)
		if kind != tc.kind || pct != tc.pct {
			t.Errorf("%s: got %v, %v want %v, %v", tc.name, pct, kind, tc.pct, tc.kind)
		}
	}
}

// --load averages whatever ACTIVE% each seat shows: a feed seat counts at its
// API-time reading, a seat still pending or silent has none, and a seat with no
// feed keeps its tick reading.
func TestRollTeamLoadReadsAPITimeForFeedSeats(t *testing.T) {
	feed := apiSeat(1_090_000, 15*time.Second, apiAt(16*time.Minute, 1_000_000))
	feed.ActiveTicks = readTicks(100, 100)
	feed.ContextAt = feed.ActiveTicks.At
	pending := apiSeat(6000, 15*time.Second, apiAt(2*time.Minute, 5000))
	pending.Name = "squad-worker-g1-1"
	pending.ActiveTicks = readTicks(100, 100)
	pending.ContextAt = pending.ActiveTicks.At
	ticks := loadSeat("squad-worker-g1-2", "worker", SessionRunning, readTicks(50, 100))
	got := RollTeamLoad(loadTeam(), []Session{feed, pending, ticks}, costT0)
	if got.ActiveMeasured != 2 || got.ActiveMeanPct != 30 {
		t.Errorf("mean %v over %d seats, want 30 over 2 (10 from API time, 50 from ticks, the pending seat left out)", got.ActiveMeanPct, got.ActiveMeasured)
	}
}
