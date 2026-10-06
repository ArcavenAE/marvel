package bus

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// leafDownEvents returns the bus.leaf.down events in the ring.
func leafDownEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindBusLeafDown}, 0)
}

// enrolledLeaf is a supervisor with a hub and a leaf seed in the store, not
// started: the leaf record is exercised through observeLeaf at chosen times.
func enrolledLeaf(t *testing.T) (*Supervisor, *events.Ring) {
	t.Helper()
	s, m, ring := newTestSupervisor(t, "nats-leaf://hub.example:7442")
	m.hasLeafSeed = func() bool { return true }
	return s, ring
}

// TestEnrolledLeafDownAtFirstPollIsReported: a leaf already down at the first
// poll after a start or reexec used to stay silent until it came up
// (wake-service.md section 4). An enrolled one is reported once.
func TestEnrolledLeafDownAtFirstPollIsReported(t *testing.T) {
	s, ring := enrolledLeaf(t)
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.observeLeaf(0, t0)
	s.observeLeaf(0, t0.Add(30*time.Second))
	if got := leafDownEvents(ring); len(got) != 1 {
		t.Fatalf("bus.leaf.down events = %d, want 1 for an enrolled leaf down at the first poll", len(got))
	}
}

// TestUnenrolledHubNeverGetsLeafDown: no link was ever promised, so neither
// the first poll nor a long wait reports one (the unenrolled state has its own
// event).
func TestUnenrolledHubNeverGetsLeafDown(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "nats-leaf://hub.example:7442")
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		s.observeLeaf(0, t0.Add(time.Duration(i)*time.Hour))
	}
	if got := leafDownEvents(ring); len(got) != 0 {
		t.Fatalf("bus.leaf.down events = %d for an unenrolled hub, want 0", len(got))
	}
}

// TestLeafDownRepeatsWhileItLasts: one event when the link drops, then one
// per repeat interval while it stays down, each naming how long.
func TestLeafDownRepeatsWhileItLasts(t *testing.T) {
	s, ring := enrolledLeaf(t)
	s.leafRepeatEvery = 10 * time.Minute
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.observeLeaf(1, t0) // up
	s.observeLeaf(0, t0.Add(30*time.Second))
	for _, m := range []int{5, 10, 15, 25} {
		s.observeLeaf(0, t0.Add(30*time.Second+time.Duration(m)*time.Minute))
	}
	got := leafDownEvents(ring)
	if len(got) != 3 {
		t.Fatalf("bus.leaf.down events = %d, want 3 (the drop, then at 10m and 25m of 10m repeats)", len(got))
	}
	if !strings.Contains(got[1].Message, "down for 10m") {
		t.Errorf("first repeat = %q, want it to name 10m", got[1].Message)
	}
	if !strings.Contains(got[2].Message, "down for 25m") {
		t.Errorf("second repeat = %q, want it to name 25m", got[2].Message)
	}
}

// TestDetachedLeafDoesNotRepeat: a leaf the operator disconnected is down by
// choice, so a long wait is not an alarm.
func TestDetachedLeafDoesNotRepeat(t *testing.T) {
	s, ring := enrolledLeaf(t)
	s.leafRepeatEvery = 10 * time.Minute
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.observeLeaf(1, t0)
	s.mgr.leafAttached.Store(false)
	s.observeLeaf(0, t0.Add(time.Minute))
	before := len(leafDownEvents(ring))
	if before != 1 {
		t.Fatalf("the drop of a disconnected leaf was reported %d times, want once", before)
	}
	s.observeLeaf(0, t0.Add(3*time.Hour))
	if after := len(leafDownEvents(ring)); after != before {
		t.Fatalf("a detached leaf repeated bus.leaf.down: %d events, was %d", after, before)
	}
}

// TestStatusShowsHowLongTheLeafHasBeenDown: the bus status carries when the
// state was entered and how long ago, so "down" reads apart from "down for
// two hours".
func TestStatusShowsHowLongTheLeafHasBeenDown(t *testing.T) {
	s, _ := enrolledLeaf(t)
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.observeLeaf(0, t0)
	s.now = func() time.Time { return t0.Add(2*time.Hour + 9*time.Minute) }
	st := s.Status()
	if st.Leaf != "down" || st.LeafFor != "2h9m" || st.LeafSince != t0.Format(time.RFC3339) {
		t.Fatalf("status leaf=%q since=%q for=%q, want down since %s for 2h9m", st.Leaf, st.LeafSince, st.LeafFor, t0.Format(time.RFC3339))
	}
	s.observeLeaf(1, t0.Add(3*time.Hour))
	s.now = func() time.Time { return t0.Add(3*time.Hour + 40*time.Second) }
	if st := s.Status(); st.Leaf != "up" || st.LeafFor != "40s" {
		t.Fatalf("status after the link came up: leaf=%q for=%q, want up for 40s", st.Leaf, st.LeafFor)
	}
}

// TestLeafEnrolledWhileDownIsReported: a hub with no seed is silent by design,
// but once the seed is stored the leaf has promised a link, so a leaf still
// down is reported on the next poll and repeats from there (marvel#600 review).
func TestLeafEnrolledWhileDownIsReported(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "nats-leaf://hub.example:7442")
	var seeded atomic.Bool
	m.hasLeafSeed = seeded.Load
	s.leafRepeatEvery = time.Hour
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.observeLeaf(0, t0)
	s.observeLeaf(0, t0.Add(time.Hour))
	if got := len(leafDownEvents(ring)); got != 0 {
		t.Fatalf("bus.leaf.down events = %d before the seed, want 0", got)
	}
	seeded.Store(true) // credential put bus/leaf
	s.observeLeaf(0, t0.Add(2*time.Hour))
	if got := len(leafDownEvents(ring)); got != 1 {
		t.Fatalf("bus.leaf.down events = %d after the seed arrived on a down leaf, want 1", got)
	}
	s.observeLeaf(0, t0.Add(3*time.Hour))
	if got := len(leafDownEvents(ring)); got != 2 {
		t.Fatalf("bus.leaf.down events = %d an hour later, want the repeat (2)", got)
	}
}

// TestLeafAttachedWhileDownIsReported: the same for an operator connect. A
// leaf found down while disconnected is silent; connecting it makes the down
// state a promise broken, so it is reported and repeats.
func TestLeafAttachedWhileDownIsReported(t *testing.T) {
	s, ring := enrolledLeaf(t)
	s.leafRepeatEvery = time.Hour
	s.mgr.leafAttached.Store(false)
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.observeLeaf(0, t0)
	s.observeLeaf(0, t0.Add(time.Hour))
	if got := len(leafDownEvents(ring)); got != 0 {
		t.Fatalf("bus.leaf.down events = %d while disconnected, want 0", got)
	}
	s.mgr.leafAttached.Store(true) // marvel bus connect
	s.observeLeaf(0, t0.Add(2*time.Hour))
	if got := len(leafDownEvents(ring)); got != 1 {
		t.Fatalf("bus.leaf.down events = %d after connecting a down leaf, want 1", got)
	}
	s.observeLeaf(0, t0.Add(3*time.Hour))
	if got := len(leafDownEvents(ring)); got != 2 {
		t.Fatalf("bus.leaf.down events = %d an hour later, want the repeat (2)", got)
	}
}
