package bus

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// A recovery crosses a warning-and-above tap only when the down it ends was
// reported, and every leaf event says when its state was entered
// (aae-orc-x2wz4, marvel docs/design/leaf-down-bus-event.md).

func leafUpEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindBusLeafUp}, 0)
}

var sinceT0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func TestLeafUpAfterReportedDownIsWarning(t *testing.T) {
	s, ring := enrolledLeaf(t)
	s.observeLeaf(1, sinceT0)
	s.observeLeaf(0, sinceT0.Add(30*time.Second)) // the drop, reported
	if got := len(leafDownEvents(ring)); got != 1 {
		t.Fatalf("setup: bus.leaf.down events = %d, want the drop reported once", got)
	}
	upAt := sinceT0.Add(time.Minute)
	s.observeLeaf(1, upAt)
	ups := leafUpEvents(ring)
	if len(ups) != 2 {
		t.Fatalf("bus.leaf.up events = %d, want 2 (the first poll, then the recovery)", len(ups))
	}
	rec := ups[1]
	if rec.Severity != events.SeverityWarning {
		t.Errorf("recovery after a reported down has severity %q, want warning", rec.Severity)
	}
	if want := upAt.UTC().Format(time.RFC3339); rec.Since != want {
		t.Errorf("recovery since = %q, want %q (when the up state was entered)", rec.Since, want)
	}
}

func TestLeafDownCarriesWhenItDropped(t *testing.T) {
	s, ring := enrolledLeaf(t)
	s.observeLeaf(1, sinceT0)
	dropAt := sinceT0.Add(30 * time.Second)
	s.observeLeaf(0, dropAt)
	downs := leafDownEvents(ring)
	if len(downs) != 1 {
		t.Fatalf("bus.leaf.down events = %d, want 1", len(downs))
	}
	if want := dropAt.UTC().Format(time.RFC3339); downs[0].Since != want {
		t.Errorf("down since = %q, want %q", downs[0].Since, want)
	}
}

func TestLeafDownRepeatCarriesOriginalSince(t *testing.T) {
	s, ring := enrolledLeaf(t)
	s.leafRepeatEvery = 10 * time.Minute
	s.observeLeaf(1, sinceT0)
	dropAt := sinceT0.Add(30 * time.Second)
	s.observeLeaf(0, dropAt)
	s.observeLeaf(0, dropAt.Add(10*time.Minute))
	s.observeLeaf(0, dropAt.Add(20*time.Minute))
	downs := leafDownEvents(ring)
	if len(downs) != 3 {
		t.Fatalf("bus.leaf.down events = %d, want 3 (the drop and two repeats)", len(downs))
	}
	want := dropAt.UTC().Format(time.RFC3339)
	for i, d := range downs {
		if d.Since != want {
			t.Errorf("down event %d since = %q, want the original drop %q", i, d.Since, want)
		}
	}
}

// Controls: both pass before the change and must keep passing.

func TestLeafUpAfterUnreportedDownIsInfo(t *testing.T) {
	// No seed in the store, so no leaf was promised and a down at the first
	// poll is not reported; its recovery is not a warning either.
	s, _, ring := newTestSupervisor(t, "nats-leaf://hub.example.test:7442")
	s.observeLeaf(0, sinceT0)
	if got := len(leafDownEvents(ring)); got != 0 {
		t.Fatalf("setup: bus.leaf.down events = %d, want the down left unreported", got)
	}
	s.observeLeaf(1, sinceT0.Add(time.Minute))
	ups := leafUpEvents(ring)
	if len(ups) != 1 {
		t.Fatalf("bus.leaf.up events = %d, want 1", len(ups))
	}
	if ups[0].Severity != events.SeverityInfo {
		t.Errorf("recovery after an unreported down has severity %q, want info", ups[0].Severity)
	}
}

func TestLeafUpAtFirstPollIsInfo(t *testing.T) {
	s, ring := enrolledLeaf(t)
	s.observeLeaf(1, sinceT0)
	ups := leafUpEvents(ring)
	if len(ups) != 1 {
		t.Fatalf("bus.leaf.up events = %d, want 1", len(ups))
	}
	if ups[0].Severity != events.SeverityInfo {
		t.Errorf("an up at the first poll has severity %q, want info", ups[0].Severity)
	}
}
