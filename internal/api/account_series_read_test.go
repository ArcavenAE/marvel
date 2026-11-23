package api

import (
	"testing"
	"time"
)

func seriesPct(v float64) []AccountWindow {
	return []AccountWindow{{Name: "five_hour", UsedPercent: &v}, {Name: "seven_day", UsedPercent: &v}}
}

// Newest keeps the windows of the newest reading and drops one whose newest
// sample is older than ReadingMaxAge before it, so a restart never presents an
// old window as part of the current reading.
func TestSeriesNewestDropsAWindowOlderThanTheReadingMaxAge(t *testing.T) {
	s := NewAccountSeries("k", 0)
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	at := time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC)
	old := 9.0
	s.Append(key, []AccountWindow{{Name: "seven_day", UsedPercent: &old}}, "ws/old", at.Add(-2*ReadingMaxAge))
	s.Append(key, []AccountWindow{{Name: "five_hour", UsedPercent: &old}}, "ws/new", at)
	got := s.Newest()
	if len(got) != 1 || len(got[0].Windows) != 1 || got[0].Windows[0].Name != "five_hour" || got[0].Session != "ws/new" || !got[0].At.Equal(at) {
		t.Fatalf("Newest = %+v, want the one five_hour window from ws/new at %v", got, at)
	}
	s.Append(key, seriesPct(30), "ws/new", at.Add(time.Minute))
	got = s.Newest()
	if len(got[0].Windows) != 2 {
		t.Fatalf("after a reading that carries both windows, Newest = %+v, want both", got[0].Windows)
	}
}

// Recent filters by account text and by start, and orders by account, window
// and time.
func TestSeriesRecentFiltersAndOrders(t *testing.T) {
	s := NewAccountSeries("k", 0)
	a := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	b := AccountKey{Harness: "codex", Backend: BackendDefaultName}
	at := time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC)
	s.Append(a, seriesPct(1), "s", at.Add(-2*time.Hour))
	s.Append(a, seriesPct(2), "s", at)
	s.Append(b, seriesPct(3), "s", at)
	if got := s.Recent("", time.Time{}); len(got) != 6 {
		t.Fatalf("all readings = %d, want 6", len(got))
	}
	got := s.Recent("claude", at.Add(-time.Hour))
	if len(got) != 2 || got[0].Window != "five_hour" || got[1].Window != "seven_day" || got[0].UsedPercent != 2 {
		t.Fatalf("Recent(claude, since 1h) = %+v, want the two newest claude windows in window order", got)
	}
	if got := s.Recent("nosuch", time.Time{}); len(got) != 0 {
		t.Errorf("an account text that matches nothing returned %+v", got)
	}
}
