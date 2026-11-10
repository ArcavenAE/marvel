package team

import (
	"os"
	"path/filepath"
	"strings"
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

// The request's instant is truncated to the second before it is compared, so
// on a coarse-timestamp filesystem a handoff stamped in the request's second
// counts. A request at a sub-second instant and a file stamped earlier in that
// second must shift; without the truncation the file reads as older.
func TestAHandoffStampedEarlierInTheRequestsSecondShiftsTheSeat(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-handoff-subsecond", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.clock.Advance(700 * time.Millisecond)
	f.evaluate() // asks at listEpoch + 700ms

	f.ctrl.handoffProbes.wait()
	writeHandoff(t, dir, s.Name, "notes\nEND HANDOFF\n")
	sameSecond := listEpoch.Add(100 * time.Millisecond)
	if err := os.Chtimes(filepath.Join(dir, s.Name+".md"), sameSecond, sameSecond); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Minute)
	if got := f.evaluate(); got.Shift.Phase != api.ShiftLaunching {
		t.Fatalf("phase = %q, want launching: the file is stamped in the request's second", got.Shift.Phase)
	}
}

// A finished read answers only for the instant it was made against: a read
// against an earlier request's instant is not served for a later request on
// the same path and marker.
func TestAFinishedHandoffReadAnswersOnlyForItsOwnInstant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.md")
	if err := os.WriteFile(path, []byte("n\nEND HANDOFF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	var p handoffProbes
	now := stamp.Add(time.Hour)
	earlier, later := stamp.Add(-time.Minute), stamp.Add(time.Minute)

	p.complete("k", path, "END HANDOFF", earlier, now)
	p.wait()
	got, known := p.complete("k", path, "END HANDOFF", earlier, now)
	if !known || !got.complete {
		t.Fatalf("read for the earlier instant = %+v, known %v; want a complete answer", got, known)
	}
	p.wait()

	// A later request on the same session and file: the file now predates it.
	if _, known := p.complete("k", path, "END HANDOFF", later, now); known {
		t.Fatal("an answer made for an earlier instant was served for a later one")
	}
	p.wait()
	got, known = p.complete("k", path, "END HANDOFF", later, now)
	if !known || got.complete {
		t.Fatalf("read for the later instant = %+v, known %v; want a known, incomplete answer", got, known)
	}
}

// The repeats after an escalation read the file against the same instant: a
// stale marker is reported absent, and a handoff written after the request is
// reported present.
func TestTheEscalatedRepeatJudgesTheHandoffByTheRequestsInstant(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-handoff-repeat-fresh", maxAgeRole(dir))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	writeHandoff(t, dir, s.Name, "old notes\nEND HANDOFF\n")
	stale := listEpoch.Add(-time.Hour)
	file := filepath.Join(dir, s.Name+".md")
	if err := os.Chtimes(file, stale, stale); err != nil {
		t.Fatal(err)
	}
	window := api.DefaultShiftHandoffWindow

	f.clock.Advance(700 * time.Millisecond)
	f.evaluate() // asks at a sub-second instant
	f.clock.Advance(window + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	f.evaluate() // escalates
	f.ctrl.handoffProbes.wait()

	repeat := func() string {
		t.Helper()
		f.clock.Advance(window + time.Second)
		f.evaluate()
		f.ctrl.handoffProbes.wait()
		f.clock.Advance(time.Second)
		f.evaluate()
		f.ctrl.handoffProbes.wait()
		evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
		return evs[len(evs)-1].Message
	}

	if msg := repeat(); strings.Contains(msg, "marker is now present") || !strings.Contains(msg, "ends in the marker") {
		t.Fatalf("repeat over a stale marker = %q, want the marker reported absent", msg)
	}

	// Stamped earlier in the request's second: it counts, to the second.
	fresh := listEpoch.Add(100 * time.Millisecond)
	if err := os.Chtimes(file, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	if msg := repeat(); !strings.Contains(msg, "marker is now present") {
		t.Fatalf("repeat after a handoff stamped after the request = %q, want the marker reported present", msg)
	}
}
