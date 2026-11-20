package hostvitals

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// clockAt is a sampler clock that returns t.
func clockAt(t time.Time) func() time.Time { return func() time.Time { return t } }

// find returns the threshold with the given name.
func find(th []Threshold, name string) (Threshold, bool) {
	for _, t := range th {
		if t.Name == name {
			return t, true
		}
	}
	return Threshold{}, false
}

// The sample time is reported in UTC, whatever zone the host clock is in, so
// the table and the JSON read alike.
func TestSnapshotTimeIsUTC(t *testing.T) {
	zone := time.FixedZone("west", -6*3600)
	s := New(true, func() Reading { return swapReading(1, 100) }, nil)
	s.now = clockAt(time.Date(2026, 11, 20, 16, 4, 31, 0, zone))
	s.Sample()
	got := s.Snapshot()
	if got.At == nil || got.At.Location() != time.UTC || got.At.Hour() != 22 {
		t.Errorf("At = %v, want 22:04:31 UTC", got.At)
	}
}

// A sampler nobody turned on says so and shows nothing else: no sample, no
// thresholds, no gaps, since there is nothing to be missing from.
func TestSnapshotOfAnOffSampler(t *testing.T) {
	got := New(false, func() Reading { return swapReading(1, 100) }, nil).Snapshot()
	if got.Enabled || got.Sampled || got.At != nil || len(got.Thresholds) != 0 || len(got.Gaps) != 0 {
		t.Errorf("off snapshot = %+v, want enabled false and nothing else", got)
	}
}

// An enabled sampler that has not sampled yet says enabled, not sampled, and
// has no time. The gaps still show, since they do not depend on a reading.
func TestSnapshotBeforeTheFirstSample(t *testing.T) {
	got := New(true, func() Reading { return swapReading(1, 100) }, nil).Snapshot()
	if !got.Enabled || got.Sampled || got.At != nil {
		t.Errorf("snapshot = %+v, want enabled true, sampled false, no time", got)
	}
	if len(got.Thresholds) != 0 {
		t.Errorf("thresholds before a sample = %+v, want none", got.Thresholds)
	}
	if len(got.Gaps) == 0 {
		t.Error("no gaps before the first sample, want the design-listed vitals not held yet")
	}
}

// The snapshot carries the reading and the time of the sample it was taken
// from, and building it does not read the host again.
func TestSnapshotCarriesTheSampleAndItsTime(t *testing.T) {
	at := time.Date(2026, 11, 20, 21, 55, 0, 0, time.UTC)
	want := swapReading(40, 100)
	reads := 0
	s := New(true, func() Reading { reads++; return want }, nil)
	s.now = clockAt(at)
	s.Sample()

	got := s.Snapshot()
	_ = s.Snapshot()
	if !got.Sampled || got.At == nil || !got.At.Equal(at) {
		t.Fatalf("snapshot = %+v, want sampled at %v", got, at)
	}
	if got.Reading != want {
		t.Errorf("reading = %+v, want %+v", got.Reading, want)
	}
	if reads != 1 {
		t.Errorf("the host was read %d times, want 1 (Snapshot must not read it)", reads)
	}
}

// The swap threshold reports under, over or gap, with the same strict line
// the crossing events use: exactly 50% is under.
func TestSnapshotSwapThresholdStates(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Reading
		want string
	}{
		{"well under", swapReading(40, 100), "under"},
		{"exactly on the line", swapReading(50, 100), "under"},
		{"just over", swapReading(51, 100), "over"},
		{"no swap configured", swapReading(0, 0), "under"},
		{"host gave no swap figure", Reading{}, "gap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(true, func() Reading { return tc.r }, nil)
			s.Sample()
			th, ok := find(s.Snapshot().Thresholds, "swap used")
			if !ok {
				t.Fatalf("no swap used threshold in %+v", s.Snapshot().Thresholds)
			}
			if th.State != tc.want {
				t.Errorf("state = %q, want %q (%+v)", th.State, tc.want, th)
			}
			if th.Line != "over 50% of total" {
				t.Errorf("line = %q, want %q", th.Line, "over 50% of total")
			}
		})
	}
}

// Memory pressure is sampled, and the design names no line for it, so it
// shows its figure with state none, never a made-up line.
func TestSnapshotMemoryPressureHasNoLine(t *testing.T) {
	r := Reading{KernelPressureOK: true, PressureLevel: 2, MemoryFreePct: 17}
	s := New(true, func() Reading { return r }, nil)
	s.Sample()
	th, ok := find(s.Snapshot().Thresholds, "memory pressure")
	if !ok {
		t.Fatalf("no memory pressure entry in %+v", s.Snapshot().Thresholds)
	}
	if th.State != "none" {
		t.Errorf("state = %q, want none", th.State)
	}
	if th.Detail == "" {
		t.Error("detail is empty, want the level and the free share")
	}
}

// Each vital the design lists and the sampler does not hold yet is a gap
// line naming its design section, so the build that adds the vital removes
// the line. The order is the design's table order.
func TestSnapshotGapsNameTheirDesignSections(t *testing.T) {
	s := New(true, func() Reading { return swapReading(1, 100) }, nil)
	s.Sample()
	var got [][2]string
	for _, g := range s.Snapshot().Gaps {
		if g.Reason == "" {
			t.Errorf("gap %q has no reason", g.Vital)
		}
		got = append(got, [2]string{g.Vital, g.Section})
	}
	want := [][2]string{
		{"orphan CPU", "2.1"},
		{"SSD wear", "2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gaps = %v, want %v", got, want)
	}
}

// The JSON a script reads keeps its key names.
func TestSnapshotJSONKeys(t *testing.T) {
	s := New(true, func() Reading { return swapReading(60, 100) }, nil)
	s.now = clockAt(time.Date(2026, 11, 20, 21, 55, 0, 0, time.UTC))
	s.Sample()
	raw, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"enabled", "sampled", "at", "reading", "thresholds", "gaps"} {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON has no %q key: %s", k, raw)
		}
	}
	ths, _ := m["thresholds"].([]any)
	if len(ths) == 0 {
		t.Fatalf("no thresholds in %s", raw)
	}
	first, _ := ths[0].(map[string]any)
	for _, k := range []string{"name", "line", "state"} {
		if _, ok := first[k]; !ok {
			t.Errorf("threshold has no %q key: %v", k, first)
		}
	}
	gaps, _ := m["gaps"].([]any)
	if len(gaps) == 0 {
		t.Fatalf("no gaps in %s", raw)
	}
	g0, _ := gaps[0].(map[string]any)
	for _, k := range []string{"vital", "section", "reason"} {
		if _, ok := g0[k]; !ok {
			t.Errorf("gap has no %q key: %v", k, g0)
		}
	}
}
