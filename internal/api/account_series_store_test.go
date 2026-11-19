package api

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A reading kept through a restart is the point of persisting the series: the
// quota-per-token figure needs readings from before the restart, and a reading
// dropped now cannot be taken again.

func attachedSeries(t *testing.T, dir string, now time.Time) *AccountSeries {
	t.Helper()
	s := NewAccountSeries("kinu", DefaultSeriesRetention)
	if err := s.Attach(dir, 0, now); err != nil {
		t.Fatalf("Attach(%q): %v", dir, err)
	}
	return s
}

func TestSeriesKeepsItsReadingsThroughARestart(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	reset := seriesT0.Add(3 * time.Hour)

	before := attachedSeries(t, dir, seriesT0)
	before.Append(k, []AccountWindow{pctWin("five_hour", 10, reset)}, "ws/a", seriesT0)
	before.Append(k, []AccountWindow{pctWin("five_hour", 14, reset)}, "ws/a", seriesT0.Add(10*time.Minute))

	after := attachedSeries(t, dir, seriesT0.Add(time.Hour))
	got := after.Samples(k, "five_hour", time.Time{}, time.Time{})
	if len(got) != 2 {
		t.Fatalf("samples after a restart = %d, want 2", len(got))
	}
	if got[0].UsedPercent != 10 || got[1].UsedPercent != 14 || got[0].Session != "ws/a" || !got[0].At.Equal(seriesT0) {
		t.Fatalf("samples after a restart = %+v, want the two readings as written", got)
	}
}

func TestSeriesDeltaAnswersTheSameAfterARestart(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	reset := seriesT0.Add(3 * time.Hour)

	before := attachedSeries(t, dir, seriesT0)
	before.Append(k, []AccountWindow{pctWin("five_hour", 10, reset)}, "ws/a", seriesT0)
	before.Append(k, []AccountWindow{pctWin("five_hour", 16, reset)}, "ws/a", seriesT0.Add(20*time.Minute))
	want := before.Delta(k, "five_hour", seriesT0, seriesT0.Add(20*time.Minute))
	if want.Delta == nil {
		t.Fatalf("setup: delta before the restart is unknown: %+v", want)
	}

	after := attachedSeries(t, dir, seriesT0.Add(time.Hour))
	got := after.Delta(k, "five_hour", seriesT0, seriesT0.Add(20*time.Minute))
	if got.Delta == nil || *got.Delta != *want.Delta {
		t.Fatalf("delta after a restart = %+v, want %v", got, *want.Delta)
	}
}

func TestSeriesWritesOneFilePerUTCDay(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	reset := seriesT0.Add(40 * time.Hour)
	s := attachedSeries(t, dir, seriesT0)
	s.Append(k, []AccountWindow{pctWin("seven_day", 1, reset)}, "ws/a", time.Date(2026, 11, 10, 23, 50, 0, 0, time.UTC))
	s.Append(k, []AccountWindow{pctWin("seven_day", 2, reset)}, "ws/a", time.Date(2026, 11, 10, 23, 59, 0, 0, time.UTC))
	s.Append(k, []AccountWindow{pctWin("seven_day", 3, reset)}, "ws/a", time.Date(2026, 11, 11, 0, 5, 0, 0, time.UTC))

	for name, lines := range map[string]int{"2026-11-10.jsonl": 2, "2026-11-11.jsonl": 1} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("day file %s: %v", name, err)
		}
		if n := bytes.Count(b, []byte("\n")); n != lines {
			t.Errorf("%s holds %d lines, want %d", name, n, lines)
		}
	}
}

func TestSeriesWritesARepeatedReadingOnce(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	reset := seriesT0.Add(3 * time.Hour)
	s := attachedSeries(t, dir, seriesT0)
	for i := 0; i < 5; i++ {
		s.Append(k, []AccountWindow{pctWin("five_hour", 10, reset)}, "ws/a", seriesT0.Add(time.Duration(i)*5*time.Second))
	}
	b, err := os.ReadFile(filepath.Join(dir, "2026-11-10.jsonl"))
	if err != nil {
		t.Fatalf("day file: %v", err)
	}
	if n := bytes.Count(b, []byte("\n")); n != 1 {
		t.Fatalf("day file holds %d lines for one repeated reading, want 1", n)
	}
}

func TestSeriesAttachRemovesDayFilesPastTheRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"2026-08-01.jsonl", "2026-08-20.jsonl", "2026-08-21.jsonl", "2026-08-22.jsonl", "2026-11-18.jsonl"} {
		line := `{"key":{"harness":"claude","backend":"default"},"sample":{"cluster":"kinu","account":"a","window":"five_hour","used_percent":5,"at":"` + name[:10] + `T01:00:00Z"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewAccountSeries("kinu", DefaultSeriesRetention).Attach(dir, 90*24*time.Hour, now); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	for name, want := range map[string]bool{
		"2026-08-01.jsonl": false, // 110 days old
		"2026-08-20.jsonl": false, // its day ended 90 days and 12 hours ago
		"2026-08-21.jsonl": true,  // its day still holds readings inside 90 days
		"2026-08-22.jsonl": true,
		"2026-11-18.jsonl": true,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if got := err == nil; got != want {
			t.Errorf("%s present = %v, want %v", name, got, want)
		}
	}
}

func TestSeriesAttachLoadsOnlyTheMemoryWindow(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)

	old := attachedSeries(t, dir, now)
	old.Append(k, []AccountWindow{pctWin("seven_day", 30, reset)}, "ws/a", now.Add(-20*24*time.Hour))
	old.Append(k, []AccountWindow{pctWin("seven_day", 50, reset)}, "ws/a", now.Add(-2*24*time.Hour))

	after := attachedSeries(t, dir, now)
	got := after.Samples(k, "seven_day", time.Time{}, time.Time{})
	if len(got) != 1 || got[0].UsedPercent != 50 {
		t.Fatalf("memory after a restart = %+v, want only the reading inside the eight-day window", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-10-30.jsonl")); err != nil {
		t.Fatalf("the 20-day-old day file must stay on disk inside the retention: %v", err)
	}
}

func TestSeriesAttachSkipsAMalformedLine(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	good := `{"key":{"harness":"claude","backend":"default"},"sample":{"cluster":"kinu","account":"` + k.String() + `","window":"five_hour","used_percent":7,"at":"2026-11-10T12:00:00Z"}}`
	body := strings.Join([]string{`{"key":`, good, `not json at all`}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-11-10.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s := attachedSeries(t, dir, seriesT0)
	got := s.Samples(k, "five_hour", time.Time{}, time.Time{})
	if len(got) != 1 || got[0].UsedPercent != 7 {
		t.Fatalf("samples = %+v, want the one good line kept and the bad lines skipped", got)
	}
}

func TestSeriesPersistRetentionDefaultsToNinetyDays(t *testing.T) {
	if DefaultPersistRetention != 90*24*time.Hour {
		t.Fatalf("DefaultPersistRetention = %v, want 90 days (the operator's ruling on retention)", DefaultPersistRetention)
	}
	dir := t.TempDir()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"2026-08-20.jsonl", "2026-08-25.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A retention of zero means the default.
	if err := NewAccountSeries("kinu", DefaultSeriesRetention).Attach(dir, 0, now); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08-20.jsonl")); err == nil {
		t.Errorf("a 91-day-old file survived a zero retention, which should mean 90 days")
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08-25.jsonl")); err != nil {
		t.Errorf("an 86-day-old file was removed under the default: %v", err)
	}
}

// A crash or a full disk mid-write leaves a last line with no newline. The next
// append must start a new line, or the two readings fuse into one line that
// parses as neither.
func TestSeriesAppendAfterATornLastLineKeepsBothNewReadings(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		want int
	}{
		{"a torn line alone", `{"key":{"harness":"claude"},"sam`, 2},
		{"a good line then a torn one", `{"key":{"harness":"claude","backend":"default"},"sample":{"cluster":"kinu","account":"x","window":"five_hour","used_percent":3,"at":"2026-11-10T11:00:00Z"}}` + "\n" + `{"key":{"harness":"cl`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			k := seriesKey()
			if err := os.WriteFile(filepath.Join(dir, "2026-11-10.jsonl"), []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			reset := seriesT0.Add(3 * time.Hour)
			s := attachedSeries(t, dir, seriesT0)
			s.Append(k, []AccountWindow{pctWin("five_hour", 10, reset)}, "ws/a", seriesT0)
			s.Append(k, []AccountWindow{pctWin("five_hour", 20, reset)}, "ws/a", seriesT0.Add(10*time.Minute))

			got := attachedSeries(t, dir, seriesT0.Add(time.Hour)).Samples(k, "five_hour", time.Time{}, time.Time{})
			if len(got) != tc.want {
				t.Fatalf("readings after the restart = %d, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

// A reading the memory cache drops in the same call (older than the window
// before the newest one held) is not a kept reading, so it is not written.
func TestSeriesDoesNotWriteAReadingTheCacheDropped(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	s := attachedSeries(t, dir, now)
	s.Append(k, []AccountWindow{pctWin("five_hour", 10, reset)}, "ws/a", now)
	s.Append(k, []AccountWindow{pctWin("five_hour", 5, reset)}, "ws/a", now.Add(-10*24*time.Hour))

	if got := s.Samples(k, "five_hour", time.Time{}, time.Time{}); len(got) != 1 {
		t.Fatalf("setup: memory holds %d readings, want 1", len(got))
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-09.jsonl")); err == nil {
		t.Fatalf("a day file exists for a reading the cache dropped")
	}
}

// A retention shorter than the memory window must not shorten the window: the
// persisted retention is raised to it, so a restart still reads eight days.
func TestSeriesRetentionBelowTheMemoryWindowKeepsTheWindow(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	first := attachedSeries(t, dir, now)
	first.Append(k, []AccountWindow{pctWin("five_hour", 10, reset)}, "ws/a", now.Add(-30*time.Hour))

	second := NewAccountSeries("kinu", DefaultSeriesRetention)
	if err := second.Attach(dir, time.Hour, now); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if got := second.Samples(k, "five_hour", time.Time{}, time.Time{}); len(got) != 1 {
		t.Fatalf("readings after a restart with a 1h retention = %d, want 1 (the window floors it)", len(got))
	}
}

// A line longer than any scanner buffer is one bad line, not the end of the file.
func TestSeriesLoadsPastAnOverlongLine(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	good := `{"key":{"harness":"claude","backend":"default"},"sample":{"cluster":"kinu","account":"` + k.String() + `","window":"five_hour","used_percent":7,"at":"2026-11-10T12:00:00Z"}}`
	second := strings.Replace(good, `"used_percent":7`, `"used_percent":9`, 1)
	second = strings.Replace(second, "12:00:00Z", "12:30:00Z", 1)
	body := good + "\n" + strings.Repeat("x", 3<<20) + "\n" + second + "\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-11-10.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := attachedSeries(t, dir, seriesT0).Samples(k, "five_hour", time.Time{}, time.Time{})
	if len(got) != 2 {
		t.Fatalf("readings = %d, want the 2 good lines around the overlong one: %+v", len(got), got)
	}
}

// Disk I/O does not hold the lock that Samples and Delta take: a write that
// stalls must not stall a reader.
func TestSeriesWriteDoesNotBlockAReader(t *testing.T) {
	dir := t.TempDir()
	k := seriesKey()
	s := attachedSeries(t, dir, seriesT0)
	entered, release := make(chan struct{}), make(chan struct{})
	s.store.beforeWrite = func() {
		close(entered)
		<-release
	}
	appended := make(chan struct{})
	go func() {
		s.Append(k, []AccountWindow{pctWin("five_hour", 10, seriesT0.Add(time.Hour))}, "ws/a", seriesT0)
		close(appended)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the write never started")
	}
	read := make(chan struct{})
	go func() {
		s.Samples(k, "five_hour", time.Time{}, time.Time{})
		close(read)
	}()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		close(release)
		<-appended
		t.Fatal("Samples waited for a stalled write")
	}
	close(release)
	<-appended
}
