package hostvitals

import (
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

// swapReading is a reading with swap used out of total, in bytes.
func swapReading(used, total uint64) Reading {
	return Reading{SwapOK: true, SwapUsed: used, SwapTotal: total}
}

// recorder keeps every event the sampler emits.
type recorder struct{ got []events.Event }

func (r *recorder) Emit(e events.Event) { r.got = append(r.got, e) }

func (r *recorder) kinds() []events.Kind {
	var k []events.Kind
	for _, e := range r.got {
		k = append(k, e.Kind)
	}
	return k
}

// A sampler nobody turned on reads nothing and says nothing. This is the
// default for every cluster (docs/design/host-vitals.md section 9).
func TestASamplerThatIsOffNeverReads(t *testing.T) {
	reads := 0
	rec := &recorder{}
	s := New(false, func() Reading { reads++; return swapReading(99, 100) }, rec)

	s.Sample()
	s.Sample()

	if reads != 0 {
		t.Errorf("an off sampler read the host %d times, want 0", reads)
	}
	if len(rec.got) != 0 {
		t.Errorf("an off sampler emitted %v, want nothing", rec.kinds())
	}
	if _, ok := s.Latest(); ok {
		t.Error("an off sampler holds a sample, want none")
	}
}

// The latest reading is held in memory for a reader to take.
func TestAnOnSamplerHoldsTheLatestReading(t *testing.T) {
	want := swapReading(10, 100)
	s := New(true, func() Reading { return want }, &recorder{})

	if _, ok := s.Latest(); ok {
		t.Fatal("a sampler that has not sampled holds a sample")
	}
	s.Sample()
	got, ok := s.Latest()
	if !ok || got != want {
		t.Errorf("Latest() = %+v, %v; want %+v, true", got, ok, want)
	}
}

// Swap over half of the total is a crossing, reported once when it is first
// passed and once when it is no longer passed. A sample that stays on the
// same side of the line says nothing.
func TestSwapCrossingIsReportedOnTheEdgesOnly(t *testing.T) {
	readings := []Reading{
		swapReading(40, 100), // under
		swapReading(51, 100), // crosses
		swapReading(90, 100), // still over
		swapReading(50, 100), // exactly half is not over
		swapReading(10, 100), // still under
	}
	i := 0
	rec := &recorder{}
	s := New(true, func() Reading { r := readings[i]; i++; return r }, rec)

	for range readings {
		s.Sample()
	}

	want := []events.Kind{events.KindHostVitalsCrossed, events.KindHostVitalsCleared}
	got := rec.kinds()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for _, e := range rec.got {
		if e.Severity != events.SeverityWarning && e.Kind == events.KindHostVitalsCrossed {
			t.Errorf("a crossing carries severity %q, want warning", e.Severity)
		}
	}
}

// A host with no swap configured has nothing to cross.
func TestAHostWithNoSwapNeverCrosses(t *testing.T) {
	rec := &recorder{}
	s := New(true, func() Reading { return swapReading(0, 0) }, rec)
	s.Sample()
	if len(rec.got) != 0 {
		t.Errorf("emitted %v on a host with no swap, want nothing", rec.kinds())
	}
}

// A host that cannot give a swap reading is a gap, never a crossing and
// never a clear of one that was reported.
func TestAMissingSwapReadingLeavesACrossingStanding(t *testing.T) {
	readings := []Reading{swapReading(90, 100), {}, swapReading(10, 100)}
	i := 0
	rec := &recorder{}
	s := New(true, func() Reading { r := readings[i]; i++; return r }, rec)
	for range readings {
		s.Sample()
	}
	want := []events.Kind{events.KindHostVitalsCrossed, events.KindHostVitalsCleared}
	got := rec.kinds()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("events = %v, want %v (the gap in between emits nothing)", got, want)
	}
}

// The sampler's whole surface is a reader and an emitter: it holds no way to
// signal, kill, restart or refuse anything (host-health-guard.md HH-1, and
// host-vitals.md section 5). This pins that a crossing changes nothing but
// the events ring.
func TestACrossingActsOnNothing(t *testing.T) {
	rec := &recorder{}
	s := New(true, func() Reading { return swapReading(100, 100) }, rec)
	s.Sample()
	if len(rec.got) != 1 || rec.got[0].Kind != events.KindHostVitalsCrossed {
		t.Fatalf("events = %v, want one crossing", rec.kinds())
	}
	if rec.got[0].Session != "" || rec.got[0].Team != "" {
		t.Errorf("a host crossing names a seat: %+v", rec.got[0])
	}
}

// Exactly half is not over the line, and a sample that lands on it from under
// says nothing. The row that reaches 50 from above (the edge test) cannot tell
// ">" from ">=" because the clear that follows hides it.
func TestExactlyHalfFromUnderIsNotACrossing(t *testing.T) {
	readings := []Reading{swapReading(40, 100), swapReading(50, 100), swapReading(40, 100)}
	i := 0
	rec := &recorder{}
	s := New(true, func() Reading { r := readings[i]; i++; return r }, rec)
	for range readings {
		s.Sample()
	}
	if len(rec.got) != 0 {
		t.Errorf("events = %v, want none for a sample that reaches exactly 50%%", rec.kinds())
	}
}

// A gap while swap is over the line leaves the crossing standing: the next
// reading that is still over says nothing, because nothing was cleared. A gap
// that quietly cleared the crossing would report a second one here.
func TestAGapDoesNotClearACrossingThatTheNextReadingRepeats(t *testing.T) {
	readings := []Reading{swapReading(90, 100), {}, swapReading(90, 100)}
	i := 0
	rec := &recorder{}
	s := New(true, func() Reading { r := readings[i]; i++; return r }, rec)
	for range readings {
		s.Sample()
	}
	got := rec.kinds()
	if len(got) != 1 || got[0] != events.KindHostVitalsCrossed {
		t.Fatalf("events = %v, want exactly one crossing and no clear", got)
	}
}
