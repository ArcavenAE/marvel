package hostvitals

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/procargs"
)

// orphanRig drives a sampler one pass a minute, with seat roots and markers it
// controls, and counts the marker reads.
type orphanRig struct {
	t       *testing.T
	s       *Sampler
	rec     *recorder
	now     time.Time
	roots   []int
	markers map[int]procargs.Marker
	failing map[int]bool
	reads   map[int]int
	el      float64 // elapsed seconds of the process the feed helper drives
}

func newRig(t *testing.T) *orphanRig {
	t.Helper()
	r := &orphanRig{
		t: t, rec: &recorder{}, now: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		markers: map[int]procargs.Marker{}, failing: map[int]bool{}, reads: map[int]int{},
	}
	r.s = New(true, func() Reading { return swapReading(1, 100) }, r.rec)
	r.s.now = func() time.Time { return r.now }
	r.s.SetOrphanSources(OrphanSources{
		Roots: func() []int { return r.roots },
		Marker: func(pid int) (procargs.Marker, error) {
			r.reads[pid]++
			if r.failing[pid] {
				return procargs.Marker{}, errors.New("operation not permitted")
			}
			return r.markers[pid], nil
		},
	})
	return r
}

// pass advances the clock a minute and samples the table.
func (r *orphanRig) pass(procs ...Proc) {
	r.t.Helper()
	r.now = r.now.Add(time.Minute)
	r.s.SampleProcs(procs, true)
}

func (r *orphanRig) orphans() []Orphan { return r.s.Snapshot().Orphans }

func (r *orphanRig) vital() Threshold { return vital(r.t, r.s, "orphan CPU") }

// op builds a process with a parent, CPU seconds and elapsed seconds.
func op(pid, ppid int, name string, cpu, elapsed float64) Proc {
	return Proc{PID: pid, PPID: ppid, RSSBytes: 1 << 20, ElapsedSeconds: elapsed, CPUSeconds: cpu, Name: name}
}

// A process that was under a seat's pane tree on the pass before and has parent
// pid 1 now is marked, on the pass that sees it, with the CPU it had then.
func TestAnOrphanFromASeatTreeIsMarkedOnTheNextPass(t *testing.T) {
	r := newRig(t)
	r.roots = []int{100}
	r.pass(op(100, 50, "sh", 1, 600), op(101, 100, "tmux", 10, 600))
	if got := r.orphans(); len(got) != 0 {
		t.Fatalf("under the seat, orphans = %+v, want none", got)
	}
	r.pass(op(100, 50, "sh", 1, 660), op(101, 1, "tmux", 12, 660))
	got := r.orphans()
	if len(got) != 1 || got[0].PID != 101 || got[0].Name != "tmux" || got[0].Reason != ReasonSeatTree {
		t.Fatalf("orphans = %+v, want pid 101 tmux marked by %q", got, ReasonSeatTree)
	}
	if !got[0].MarkedAt.Equal(r.now) || got[0].CPUSecondsSinceMarked != 0 {
		t.Errorf("marked at %v with %v CPU since, want %v and 0", got[0].MarkedAt, got[0].CPUSecondsSinceMarked, r.now)
	}
	r.pass(op(100, 50, "sh", 1, 720), op(101, 1, "tmux", 15, 720))
	if got := r.orphans(); len(got) != 1 || got[0].CPUSecondsSinceMarked != 3 {
		t.Errorf("a pass later, orphans = %+v, want 3 CPU seconds since marked", got)
	}
}

// A seat's own pane process that loses its parent is an orphan as well.
func TestASeatRootThatLosesItsParentIsMarked(t *testing.T) {
	r := newRig(t)
	r.roots = []int{100}
	r.pass(op(100, 50, "sh", 1, 600))
	r.pass(op(100, 1, "sh", 2, 660))
	if got := r.orphans(); len(got) != 1 || got[0].PID != 100 || got[0].Reason != ReasonSeatTree {
		t.Errorf("orphans = %+v, want the seat root pid 100", got)
	}
}

// A process that was never under a seat, and carries no marker, is not an
// orphan when it loses its parent. A descendant of a seat in the same passes is
// the positive control.
func TestAnUnrelatedProcessThatLosesItsParentIsNotAnOrphan(t *testing.T) {
	r := newRig(t)
	r.roots = []int{100}
	r.pass(op(100, 50, "sh", 1, 600), op(101, 100, "tmux", 1, 600), op(500, 5, "other", 1, 600))
	r.pass(op(100, 50, "sh", 1, 660), op(101, 1, "tmux", 2, 660), op(500, 1, "other", 2, 660))
	got := r.orphans()
	if len(got) != 1 || got[0].PID != 101 {
		t.Errorf("orphans = %+v, want only pid 101 (500 was never a seat's)", got)
	}
}

// A process already orphaned when the sampler first sees it is matched by its
// marker, and carries the MARVEL_SESSION value; one with no marker, and one
// whose environment cannot be read, are not marked.
func TestAnOrphanOlderThanTheDaemonIsMatchedByItsMarker(t *testing.T) {
	r := newRig(t)
	r.markers[300] = procargs.Marker{Session: "aae/seat-1"}
	r.markers[303] = procargs.Marker{MarvelPath: true}
	r.failing[302] = true
	r.pass(op(300, 1, "tmux", 4000, 90000), op(301, 1, "launchd-child", 1, 90000), op(302, 1, "unreadable", 1, 90000), op(303, 1, "go", 9, 3000))
	got := r.orphans()
	byPID := map[int]Orphan{}
	for _, o := range got {
		byPID[o.PID] = o
	}
	if len(got) != 2 {
		t.Fatalf("orphans = %+v, want pids 300 and 303 only", got)
	}
	if o := byPID[300]; o.Reason != ReasonMarker || o.Session != "aae/seat-1" || !o.MarkedAt.Equal(r.now) {
		t.Errorf("pid 300 = %+v, want reason %q, session aae/seat-1, marked now", o, ReasonMarker)
	}
	if o := byPID[303]; o.Reason != ReasonMarker || o.Session != "" {
		t.Errorf("pid 303 = %+v, want a marker with no session (a marvel path only)", o)
	}
}

// The marker is read once per process, and only for a process whose parent is
// pid 1: a process with another parent is never read, and a parentless process
// with no marker is not read again on the passes after.
func TestMarkerIsReadOnceAndOnlyForParentlessProcesses(t *testing.T) {
	r := newRig(t)
	r.markers[300] = procargs.Marker{Session: "s"}
	for i := 0; i < 3; i++ {
		r.pass(op(300, 1, "tmux", float64(i), 5000+float64(i)*60), op(301, 1, "plain", 1, 5000+float64(i)*60), op(400, 5, "child", 1, 5000+float64(i)*60))
	}
	if r.reads[300] != 1 || r.reads[301] != 1 {
		t.Errorf("reads of 300 and 301 = %d and %d over three passes, want 1 each", r.reads[300], r.reads[301])
	}
	if r.reads[400] != 0 {
		t.Errorf("a process whose parent is not pid 1 was read %d times, want 0", r.reads[400])
	}
	if len(r.orphans()) != 1 {
		t.Errorf("orphans = %+v, want 300 only", r.orphans())
	}
}

// An orphan that exits is dropped, and a pid reused by a new process (its
// elapsed time went backwards) is not the same orphan.
func TestAnOrphanThatExitsOrIsReusedIsDropped(t *testing.T) {
	r := newRig(t)
	r.markers[300] = procargs.Marker{Session: "s"}
	r.pass(op(300, 1, "tmux", 5, 5000), op(301, 1, "tmux", 5, 5000))
	r.markers[301] = procargs.Marker{}
	if len(r.orphans()) != 1 || r.orphans()[0].PID != 300 {
		t.Fatalf("setup: orphans = %+v, want 300", r.orphans())
	}
	r.pass(op(301, 1, "tmux", 5, 5060))
	if got := r.orphans(); len(got) != 0 {
		t.Errorf("after pid 300 exited, orphans = %+v, want none", got)
	}
	r2 := newRig(t)
	r2.markers[300] = procargs.Marker{Session: "s"}
	r2.pass(op(300, 1, "tmux", 5, 5000))
	r2.markers[300] = procargs.Marker{}
	r2.pass(op(300, 1, "newcomer", 0.5, 20))
	if got := r2.orphans(); len(got) != 0 {
		t.Errorf("pid reused by a younger process, orphans = %+v, want none", got)
	}
}

// feed runs the orphan through n minutes at the given CPU seconds a minute,
// from the pass that marks it at 1000 CPU seconds.
func (r *orphanRig) feed(n int, perMinute float64, cpu *float64) {
	r.t.Helper()
	for i := 0; i < n; i++ {
		*cpu += perMinute
		r.el += 60
		r.pass(op(300, 1, "tmux", *cpu, r.el))
	}
}

// An orphan over 90% of a core across a whole 30 minute window crosses once,
// and clears once the window's share falls back to the line.
func TestOrphanCPUCrossesAfterThirtyMinutesAndClears(t *testing.T) {
	r := newRig(t)
	r.markers[300] = procargs.Marker{Session: "aae/seat-1"}
	cpu := 1000.0
	r.el = 90000
	r.pass(op(300, 1, "tmux", cpu, r.el))
	r.feed(29, 58, &cpu)
	if th := r.vital(); th.State != "under" {
		t.Fatalf("29 minutes in, state = %q, want under: the window is not watched yet (%+v)", th.State, th)
	}
	if o := r.orphans(); len(o) != 1 || o[0].Share != nil {
		t.Fatalf("29 minutes in, orphans = %+v, want one with no share yet", o)
	}
	if len(r.rec.got) != 0 {
		t.Fatalf("29 minutes in, events = %v, want none", r.rec.kinds())
	}
	r.feed(1, 58, &cpu)
	th := r.vital()
	if th.State != "over" || th.Line != "over 90% of a core for 30 minutes" {
		t.Fatalf("30 minutes in, threshold = %+v, want over", th)
	}
	if got := r.rec.kinds(); len(got) != 1 || got[0] != events.KindHostVitalsCrossed {
		t.Fatalf("30 minutes in, events = %v, want one crossed", got)
	}
	msg := r.rec.got[0].Message
	for _, want := range []string{"orphan CPU", "tmux", "300"} {
		if !strings.Contains(msg, want) {
			t.Errorf("crossing message lacks %q: %s", want, msg)
		}
	}
	o := r.orphans()[0]
	if o.CPUSecondsSinceMarked != 1740 || o.Share == nil || math.Abs(*o.Share-58.0/60) > 1e-9 {
		t.Errorf("orphan = %+v, want 1740 CPU seconds since marked and a share of 58/60", o)
	}
	r.feed(3, 0, &cpu)
	if th := r.vital(); th.State != "under" {
		t.Errorf("three idle minutes later, state = %q, want under", th.State)
	}
	if got := r.rec.kinds(); len(got) != 2 || got[1] != events.KindHostVitalsCleared {
		t.Errorf("events = %v, want crossed then one cleared", got)
	}
}

// Exactly 90% over the window is under: the line is strict.
func TestOrphanCPUAtExactlyNinetyPercentIsUnder(t *testing.T) {
	r := newRig(t)
	r.markers[300] = procargs.Marker{Session: "s"}
	cpu := 1000.0
	r.el = 90000
	r.pass(op(300, 1, "tmux", cpu, r.el))
	r.feed(31, 54, &cpu)
	if th := r.vital(); th.State != "under" {
		t.Errorf("state = %q at exactly 90%%, want under (%+v)", th.State, th)
	}
	if o := r.orphans(); len(o) != 1 || o[0].Share == nil {
		t.Errorf("orphans = %+v, want one with a share (watched 30 minutes)", o)
	}
	if len(r.rec.got) != 0 {
		t.Errorf("events = %v, want none at exactly 90%%", r.rec.kinds())
	}
}

// A table that could not be read is a gap: the orphans stay listed, the state
// stays over, and no event is emitted across it.
func TestAMissedTableKeepsTheOrphansAndTheState(t *testing.T) {
	r := newRig(t)
	r.markers[300] = procargs.Marker{Session: "s"}
	cpu := 1000.0
	r.el = 90000
	r.pass(op(300, 1, "tmux", cpu, r.el))
	r.feed(30, 58, &cpu)
	if r.vital().State != "over" {
		t.Fatalf("setup: state = %q, want over", r.vital().State)
	}
	n := len(r.rec.got)
	r.now = r.now.Add(time.Minute)
	r.s.SampleProcs(nil, false)
	if th := r.vital(); th.State != "gap" {
		t.Errorf("a missed table, state = %q, want gap", th.State)
	}
	if len(r.orphans()) != 1 {
		t.Errorf("a missed table, orphans = %+v, want the one still listed", r.orphans())
	}
	cpu += 58
	r.el += 120
	r.pass(op(300, 1, "tmux", cpu, r.el))
	if th := r.vital(); th.State != "over" {
		t.Errorf("the pass after the gap, state = %q, want over", th.State)
	}
	if len(r.rec.got) != n {
		t.Errorf("events = %v, want no new event across the gap", r.rec.kinds())
	}
}

// With no sources a sampler marks nothing and says so; with none it never reads.
func TestWithoutOrphanSourcesThereAreNoOrphans(t *testing.T) {
	s := New(true, func() Reading { return swapReading(1, 100) }, &recorder{})
	s.SampleProcs([]Proc{op(300, 1, "tmux", 5, 9000)}, true)
	if got := s.Snapshot().Orphans; len(got) != 0 {
		t.Errorf("orphans = %+v, want none without sources", got)
	}
	th := vital(t, s, "orphan CPU")
	if th.State != "under" || !strings.Contains(th.Detail, "no orphans") {
		t.Errorf("threshold = %+v, want under with no orphans", th)
	}
}

// The snapshot lists at most OrphansShown orphans, most CPU since marking first.
func TestOrphansAreCappedAndOrderedByCPU(t *testing.T) {
	r := newRig(t)
	var first, second []Proc
	for i := 0; i < OrphansShown+5; i++ {
		pid := 1000 + i
		r.markers[pid] = procargs.Marker{Session: "s"}
		first = append(first, op(pid, 1, "p", 10, 9000))
		second = append(second, op(pid, 1, "p", 10+float64(i), 9060))
	}
	r.pass(first...)
	r.pass(second...)
	got := r.orphans()
	if len(got) != OrphansShown {
		t.Fatalf("%d orphans listed, want %d", len(got), OrphansShown)
	}
	if got[0].PID != 1000+OrphansShown+4 || got[1].PID != 1000+OrphansShown+3 {
		t.Errorf("order = %d, %d, want the most CPU first", got[0].PID, got[1].PID)
	}
}
