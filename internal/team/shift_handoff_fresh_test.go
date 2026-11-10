package team

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A handoff that ends in the marker but was written before the shift was
// requested describes an earlier moment, so it does not start the shift
// (marvel#1387). One written after the request does.
func TestAHandoffWrittenBeforeTheRequestDoesNotShiftTheSeat(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-handoff-stale", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	// The seat wrote its handoff an hour before marvel asked.
	writeHandoff(t, dir, s.Name, "old notes\nEND HANDOFF\n")
	stale := listEpoch.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, s.Name+".md"), stale, stale); err != nil {
		t.Fatal(err)
	}

	f.evaluate() // asks
	for range 3 {
		f.ctrl.handoffProbes.wait()
		f.clock.Advance(time.Minute)
		if got := f.evaluate(); got.Shift.Phase != api.ShiftNone {
			t.Fatalf("phase = %q, want none: the marker is older than the request", got.Shift.Phase)
		}
	}
	if n := f.count(events.KindShiftAutoTriggered); n != 0 {
		t.Fatalf("autotriggered events = %d, want none", n)
	}

	// The seat answers the request: a rewrite after it counts.
	f.ctrl.handoffProbes.wait()
	fresh := listEpoch.Add(time.Minute)
	writeHandoff(t, dir, s.Name, "current notes\nEND HANDOFF\n")
	if err := os.Chtimes(filepath.Join(dir, s.Name+".md"), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	if got := f.evaluate(); got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching once the handoff is rewritten after the request", got.Shift.Phase)
	}
}

// A handoff written after the request shifts the seat, as before.
func TestAHandoffWrittenAfterTheRequestShiftsTheSeat(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-handoff-fresh", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate() // asks at listEpoch

	f.ctrl.handoffProbes.wait()
	writeHandoff(t, dir, s.Name, "notes\nEND HANDOFF\n")
	after := listEpoch.Add(30 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, s.Name+".md"), after, after); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	if got := f.evaluate(); got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching", got.Shift.Phase)
	}
}

// The comparison is made to the second, for filesystems that keep whole-second
// timestamps: a file stamped in the same second as the request counts.
func TestAHandoffStampedInTheRequestsSecondCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.md")
	if err := os.WriteFile(path, []byte("n\nEND HANDOFF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := time.Date(2026, 11, 10, 20, 0, 0, 700_000_000, time.UTC)
	for _, tc := range []struct {
		name  string
		stamp time.Time
		want  bool
	}{
		{"same second, earlier fraction", time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC), true},
		{"previous second", time.Date(2026, 11, 10, 19, 59, 59, 900_000_000, time.UTC), false},
		{"after", req.Add(time.Second), true},
	} {
		if err := os.Chtimes(path, tc.stamp, tc.stamp); err != nil {
			t.Fatal(err)
		}
		if got := handoffCompleteSince(path, "END HANDOFF", req.Truncate(time.Second)); got != tc.want {
			t.Errorf("%s: complete = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A stale handoff leaves the window and the escalation as they were: past the
// window the request escalates and the seat keeps running.
func TestAStaleHandoffEscalatesAtTheWindowAndNeverShifts(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-handoff-stale-escalates", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	writeHandoff(t, dir, s.Name, "old notes\nEND HANDOFF\n")
	stale := listEpoch.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, s.Name+".md"), stale, stale); err != nil {
		t.Fatal(err)
	}

	f.evaluate() // asks
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	got := f.evaluate()

	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("phase = %q, want none", got.Shift.Phase)
	}
	if !got.ShiftRequests[testShiftRole].Escalated {
		t.Fatal("the request should be escalated once the window passed with only a stale handoff")
	}
	if n := f.count(events.KindShiftHandoffMissing); n != 1 {
		t.Fatalf("handoff-missing events = %d, want 1", n)
	}
}
