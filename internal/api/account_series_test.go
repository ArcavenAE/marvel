package api

import (
	"testing"
	"time"
)

func pctWin(name string, pct float64, resets time.Time) AccountWindow {
	return AccountWindow{Name: name, UsedPercent: &pct, ResetsAt: resets}
}

var seriesT0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func seriesKey() AccountKey { return AccountKey{Harness: "claude", Backend: BackendDefaultName} }

func TestSeriesKeepsEveryReadingStampedWithClusterAccountAndTime(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	reset := seriesT0.Add(3 * time.Hour)
	s.Append(k, []AccountWindow{pctWin("five_hour", 10, reset), pctWin("seven_day", 40, seriesT0.Add(48*time.Hour))}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("five_hour", 12, reset)}, "ws/a", seriesT0.Add(5*time.Minute))

	got := s.Samples(k, "five_hour", time.Time{}, time.Time{})
	if len(got) != 2 {
		t.Fatalf("five_hour samples = %d, want 2 (a series, not the latest only)", len(got))
	}
	if got[0].Cluster != "kinu" || got[0].Account != k.String() || got[0].Window != "five_hour" || !got[0].At.Equal(seriesT0) || got[0].UsedPercent != 10 {
		t.Fatalf("first sample = %+v, want cluster kinu, account %q, five_hour, 10%% at t0", got[0], k.String())
	}
	if got[1].UsedPercent != 12 {
		t.Fatalf("second sample = %+v, want 12%%", got[1])
	}
	if n := len(s.Samples(k, "seven_day", time.Time{}, time.Time{})); n != 1 {
		t.Fatalf("seven_day samples = %d, want 1", n)
	}
}

func TestSeriesDropsWindowsWithNoPercentAndKeepsOutOfOrderSorted(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	s.Append(k, []AccountWindow{{Name: "five_hour"}}, "ws/a", seriesT0)
	if n := len(s.Samples(k, "five_hour", time.Time{}, time.Time{})); n != 0 {
		t.Fatalf("a window with no percentage was stored (%d samples); it must not read as zero", n)
	}
	s.Append(k, []AccountWindow{pctWin("five_hour", 20, time.Time{})}, "ws/a", seriesT0.Add(10*time.Minute))
	s.Append(k, []AccountWindow{pctWin("five_hour", 15, time.Time{})}, "ws/a", seriesT0.Add(5*time.Minute))
	got := s.Samples(k, "five_hour", time.Time{}, time.Time{})
	if len(got) != 2 || got[0].UsedPercent != 15 || got[1].UsedPercent != 20 {
		t.Fatalf("samples = %+v, want 15 then 20 in time order", got)
	}
}

func TestSeriesDropsAReadingOlderThanRetention(t *testing.T) {
	s := NewAccountSeries("kinu", 24*time.Hour)
	k := seriesKey()
	s.Append(k, []AccountWindow{pctWin("five_hour", 5, time.Time{})}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("five_hour", 6, time.Time{})}, "ws/a", seriesT0.Add(25*time.Hour))
	got := s.Samples(k, "five_hour", time.Time{}, time.Time{})
	if len(got) != 1 || got[0].UsedPercent != 6 {
		t.Fatalf("samples = %+v, want only the reading inside the retention", got)
	}
}

func TestSeriesDoesNotRepeatAnUnchangedReadingWithinAMinute(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	w := pctWin("five_hour", 30, seriesT0.Add(time.Hour))
	s.Append(k, []AccountWindow{w}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{w}, "ws/a", seriesT0.Add(10*time.Second))
	s.Append(k, []AccountWindow{w}, "ws/a", seriesT0.Add(2*time.Minute))
	if n := len(s.Samples(k, "five_hour", time.Time{}, time.Time{})); n != 2 {
		t.Fatalf("samples = %d, want 2 (the repeat inside a minute is not kept)", n)
	}
}

func TestSeriesIsBoundedPerWindow(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	for i := 0; i < SeriesMaxSamples+50; i++ {
		s.Append(k, []AccountWindow{pctWin("five_hour", float64(i%100), time.Time{})}, "ws/a", seriesT0.Add(time.Duration(i)*2*time.Minute))
	}
	if n := len(s.Samples(k, "five_hour", time.Time{}, time.Time{})); n > SeriesMaxSamples {
		t.Fatalf("samples = %d, want at most %d", n, SeriesMaxSamples)
	}
}

func TestSamplesRangeIsInclusive(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	for i := 0; i < 4; i++ {
		s.Append(k, []AccountWindow{pctWin("five_hour", float64(i), time.Time{})}, "ws/a", seriesT0.Add(time.Duration(i)*10*time.Minute))
	}
	got := s.Samples(k, "five_hour", seriesT0.Add(10*time.Minute), seriesT0.Add(20*time.Minute))
	if len(got) != 2 || got[0].UsedPercent != 1 || got[1].UsedPercent != 2 {
		t.Fatalf("samples = %+v, want the readings at +10m and +20m", got)
	}
}

// The run-window delta.

func TestDeltaIsEndMinusStart(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	reset := seriesT0.Add(4 * time.Hour)
	s.Append(k, []AccountWindow{pctWin("five_hour", 10, reset)}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("five_hour", 25.5, reset)}, "ws/a", seriesT0.Add(30*time.Minute))
	d := s.Delta(k, "five_hour", seriesT0.Add(time.Minute), seriesT0.Add(31*time.Minute))
	if d.StartPercent == nil || *d.StartPercent != 10 || d.EndPercent == nil || *d.EndPercent != 25.5 || d.Delta == nil || *d.Delta != 15.5 {
		t.Fatalf("delta = %+v, want start 10, end 25.5, delta 15.5", d)
	}
	if d.Cluster != "kinu" || d.Account != k.String() || d.Window != "five_hour" {
		t.Fatalf("delta not stamped with cluster, account and window: %+v", d)
	}
}

func TestDeltaWithNoReadingAtStartOrEndIsUnknownNeverZero(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	// No reading at all.
	d := s.Delta(k, "five_hour", seriesT0, seriesT0.Add(time.Hour))
	if d.StartPercent != nil || d.EndPercent != nil || d.Delta != nil {
		t.Fatalf("empty series: %+v, want all unknown", d)
	}
	// A reading at the end only.
	s.Append(k, []AccountWindow{pctWin("five_hour", 40, time.Time{})}, "ws/a", seriesT0.Add(time.Hour))
	d = s.Delta(k, "five_hour", seriesT0, seriesT0.Add(time.Hour))
	if d.StartPercent != nil || d.EndPercent == nil || d.Delta != nil {
		t.Fatalf("end only: %+v, want start unknown, end known, delta unknown", d)
	}
	if d.Note == "" {
		t.Fatal("an unknown delta carries no note saying why")
	}
}

func TestDeltaIgnoresAReadingTooOldToDescribeTheMoment(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	s.Append(k, []AccountWindow{pctWin("five_hour", 10, time.Time{})}, "ws/a", seriesT0)
	// The next reading is long after the run's end; the one at t0 is older than
	// ReadingMaxAge at the run's start.
	start := seriesT0.Add(ReadingMaxAge + time.Minute)
	s.Append(k, []AccountWindow{pctWin("five_hour", 50, time.Time{})}, "ws/a", start.Add(time.Minute))
	d := s.Delta(k, "five_hour", start, start.Add(time.Minute))
	if d.StartPercent != nil {
		t.Fatalf("start = %v from a reading older than ReadingMaxAge; want unknown", *d.StartPercent)
	}
	if d.Delta != nil {
		t.Fatalf("delta = %v with an unknown start; want unknown", *d.Delta)
	}
}

func TestDeltaAcrossAWindowResetIsUnknown(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	s.Append(k, []AccountWindow{pctWin("five_hour", 90, seriesT0.Add(10*time.Minute))}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("five_hour", 5, seriesT0.Add(5*time.Hour+10*time.Minute))}, "ws/a", seriesT0.Add(20*time.Minute))
	d := s.Delta(k, "five_hour", seriesT0, seriesT0.Add(20*time.Minute))
	if d.StartPercent == nil || d.EndPercent == nil {
		t.Fatalf("both readings exist and must be reported: %+v", d)
	}
	if d.Delta != nil {
		t.Fatalf("delta = %v across a reset; want unknown (the window restarted)", *d.Delta)
	}
	if d.Note == "" {
		t.Fatal("no note saying the window reset")
	}
}

func TestDeltaRejectsAnEndBeforeStart(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	s.Append(k, []AccountWindow{pctWin("five_hour", 10, time.Time{})}, "ws/a", seriesT0)
	d := s.Delta(k, "five_hour", seriesT0.Add(time.Hour), seriesT0)
	if d.Delta != nil || d.Note == "" {
		t.Fatalf("end before start: %+v, want unknown with a note", d)
	}
}

// A drop with a changed resets_at is a rollover, not a negative burn.
func TestAccountSeriesSurvivesResetRollover(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	s.Append(k, []AccountWindow{pctWin("seven_day", 92, seriesT0.Add(time.Hour))}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("seven_day", 3, seriesT0.Add(7*24*time.Hour+time.Hour))}, "ws/a", seriesT0.Add(2*time.Hour))
	d := s.Delta(k, "seven_day", seriesT0, seriesT0.Add(2*time.Hour))
	if !d.Rollover {
		t.Fatalf("a drop with a changed resets_at did not read as a rollover: %+v", d)
	}
	if d.Delta != nil {
		t.Fatalf("delta = %v across a rollover, want unknown and not a negative burn", *d.Delta)
	}
}

func TestDeltaOfAFallInsideOneWindowIsUnknown(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	reset := seriesT0.Add(time.Hour)
	s.Append(k, []AccountWindow{pctWin("five_hour", 50, reset)}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("five_hour", 40, reset)}, "ws/a", seriesT0.Add(10*time.Minute))
	d := s.Delta(k, "five_hour", seriesT0, seriesT0.Add(10*time.Minute))
	if d.Rollover || d.Delta != nil || d.Note == "" {
		t.Fatalf("delta = %+v, want unknown with a note and no rollover", d)
	}
}

// The rule fires on any change of resets_at, not only on a drop: a window that
// reset and then burned past the start figure is still not a burn of the
// difference.
func TestDeltaAcrossAResetThatRoseIsStillARollover(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	s.Append(k, []AccountWindow{pctWin("five_hour", 10, seriesT0.Add(time.Hour))}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("five_hour", 60, seriesT0.Add(6*time.Hour))}, "ws/a", seriesT0.Add(2*time.Hour))
	d := s.Delta(k, "five_hour", seriesT0, seriesT0.Add(2*time.Hour))
	if !d.Rollover || d.Delta != nil {
		t.Fatalf("delta = %+v, want a rollover with no delta although the figure rose", d)
	}
}

// A reading with no resets_at beside one that has it also reads as a
// rollover: the two cannot be shown to be one window, so the delta is unknown.
func TestDeltaBetweenAStampedAndAnUnstampedResetIsUnknown(t *testing.T) {
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	k := seriesKey()
	s.Append(k, []AccountWindow{pctWin("five_hour", 10, seriesT0.Add(time.Hour))}, "ws/a", seriesT0)
	s.Append(k, []AccountWindow{pctWin("five_hour", 20, time.Time{})}, "ws/a", seriesT0.Add(10*time.Minute))
	d := s.Delta(k, "five_hour", seriesT0, seriesT0.Add(10*time.Minute))
	if d.Delta != nil || !d.Rollover {
		t.Fatalf("delta = %+v, want unknown", d)
	}
}
