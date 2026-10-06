package bus

import (
	"testing"
	"time"
)

// TestStatusLeafCarriesObservedAt: the bus status carries when the leaf was
// last probed, not only when its state was entered, so a reader can tell a
// reading from a second ago from one the monitor stopped refreshing.
func TestStatusLeafCarriesObservedAt(t *testing.T) {
	s, _ := enrolledLeaf(t)
	// The test supervisor polls every 200ms, which RFC3339's whole seconds
	// cannot show; use the production cadence so valid_until is distinct.
	s.leafPoll = defaultLeafPoll
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)

	s.observeLeaf(1, t0)
	st := s.Status()
	if st.Leaf != "up" || st.LeafObservedAt != t0.Format(time.RFC3339) {
		t.Fatalf("status leaf=%q observed_at=%q, want up observed at %s", st.Leaf, st.LeafObservedAt, t0.Format(time.RFC3339))
	}
	// The reading expires. A zero valid_until would mean never stale, and a
	// reader would print a stale up forever (marvel#615 review).
	if want := t0.Add(2 * s.leafPoll).Format(time.RFC3339); st.LeafValidUntil != want {
		t.Errorf("valid_until = %q, want %q (two poll intervals after the probe)", st.LeafValidUntil, want)
	}

	// A second probe of the same state moves the probe age and leaves the
	// time the state was entered where it was.
	s.observeLeaf(1, t1)
	st = s.Status()
	if st.LeafObservedAt != t1.Format(time.RFC3339) {
		t.Errorf("observed_at after a second probe = %q, want %q", st.LeafObservedAt, t1.Format(time.RFC3339))
	}
	if st.LeafSince != t0.Format(time.RFC3339) {
		t.Errorf("leaf_since = %q, want %q (the state was entered once)", st.LeafSince, t0.Format(time.RFC3339))
	}
}

// A down reading is a reading too: it carries its probe age.
func TestStatusDownLeafCarriesObservedAt(t *testing.T) {
	s, _ := enrolledLeaf(t)
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.observeLeaf(0, t0)
	st := s.Status()
	if st.Leaf != "down" || st.LeafObservedAt != t0.Format(time.RFC3339) || st.LeafValidUntil == "" {
		t.Fatalf("status leaf=%q observed_at=%q valid_until=%q, want a down reading with both set", st.Leaf, st.LeafObservedAt, st.LeafValidUntil)
	}
}

// No reading, no age: before the first poll the state is "unknown" and
// carries none, and a leaf that promised no link carries none either.
func TestStatusLeafWithoutReadingCarriesNoObservedAt(t *testing.T) {
	s, _ := enrolledLeaf(t)
	if st := s.Status(); st.Leaf != "unknown" || st.LeafObservedAt != "" || st.LeafValidUntil != "" {
		t.Errorf("before the first poll: leaf=%q observed_at=%q valid_until=%q, want unknown and neither set", st.Leaf, st.LeafObservedAt, st.LeafValidUntil)
	}

	u, _, _ := newTestSupervisor(t, "nats-leaf://hub.example:7442") // no seed: unenrolled
	u.observeLeaf(0, time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC))
	if st := u.Status(); st.Leaf != "unenrolled" || st.LeafObservedAt != "" {
		t.Errorf("unenrolled: leaf=%q observed_at=%q, want unenrolled and no age", st.Leaf, st.LeafObservedAt)
	}
}
