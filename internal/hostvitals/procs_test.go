package hostvitals

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

const oneGiB = 1 << 30

// pr builds a process with the given RSS in bytes.
func pr(pid int, name string, rss uint64) Proc {
	return Proc{PID: pid, PPID: 1, RSSBytes: rss, ElapsedSeconds: 60, CPUSeconds: 1.5, Name: name}
}

// sampleProcs builds an on sampler with a clean swap reading and samples it once
// with the given table.
func sampleProcs(t *testing.T, procs []Proc, ok bool) *Sampler {
	t.Helper()
	s := New(true, func() Reading { return swapReading(1, 100) }, &recorder{})
	s.SampleProcs(procs, ok)
	return s
}

// vital returns the named threshold of the latest snapshot.
func vital(t *testing.T, s *Sampler, name string) Threshold {
	t.Helper()
	th, ok := find(s.Snapshot().Thresholds, name)
	if !ok {
		t.Fatalf("no %q threshold in %+v", name, s.Snapshot().Thresholds)
	}
	return th
}

// The top list holds the largest processes by RSS, biggest first, at most
// ten, and a tie goes to the lower pid so the order is the same every time.
func TestTopProcessesAreOrderedAndCapped(t *testing.T) {
	var procs []Proc
	for i := 1; i <= 14; i++ {
		procs = append(procs, pr(100+i, "p", uint64(i)*oneGiB/4))
	}
	procs = append(procs, pr(5, "tie-low", 14*oneGiB/4), pr(900, "tie-high", 14*oneGiB/4))
	s := sampleProcs(t, procs, true)

	top := s.Snapshot().Top
	if len(top) != TopProcesses {
		t.Fatalf("top has %d entries, want %d", len(top), TopProcesses)
	}
	if top[0].PID != 5 || top[1].PID != 114 || top[2].PID != 900 {
		t.Errorf("first three pids = %d, %d, %d; want 5, 114, 900 (a tie goes to the lower pid)", top[0].PID, top[1].PID, top[2].PID)
	}
	for i := 1; i < len(top); i++ {
		if top[i].RSSBytes > top[i-1].RSSBytes {
			t.Errorf("top[%d] is larger than top[%d]", i, i-1)
		}
	}
}

// fseventsd RSS is the sum over processes of that name, found by command
// name; a host without one reads under, with the reason.
func TestFseventsdRSSIsFoundByName(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "darwin"

	s := sampleProcs(t, []Proc{pr(10, "fseventsd", 3*oneGiB), pr(11, "fseventsd", 2*oneGiB), pr(12, "other", 30*oneGiB)}, true)
	th := vital(t, s, "fseventsd RSS")
	if th.State != "over" || !strings.Contains(th.Detail, "5.0 GiB") {
		t.Errorf("fseventsd RSS = %+v, want over at 5.0 GiB", th)
	}
	if th.Line != "over 4 GiB" {
		t.Errorf("line = %q, want %q", th.Line, "over 4 GiB")
	}

	s = sampleProcs(t, []Proc{pr(12, "other", 30*oneGiB)}, true)
	th = vital(t, s, "fseventsd RSS")
	if th.State != "under" || !strings.Contains(th.Detail, "no fseventsd") {
		t.Errorf("no fseventsd process = %+v, want under with the reason", th)
	}
}

// The fseventsd line is strict like the swap line: exactly 4 GiB is under.
func TestFseventsdLineIsStrict(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "darwin"
	for _, tc := range []struct {
		rss  uint64
		want string
	}{{4 * oneGiB, "under"}, {4*oneGiB + 1, "over"}} {
		s := sampleProcs(t, []Proc{pr(10, "fseventsd", tc.rss)}, true)
		if got := vital(t, s, "fseventsd RSS").State; got != tc.want {
			t.Errorf("rss %d: state %q, want %q", tc.rss, got, tc.want)
		}
	}
}

// fseventsd is darwin only: elsewhere the vital is a gap that says so, and the
// process RSS vital still reads.
func TestFseventsdIsAGapOffDarwin(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "linux"
	s := sampleProcs(t, []Proc{pr(10, "fseventsd", 30*oneGiB), pr(11, "x", oneGiB)}, true)
	th := vital(t, s, "fseventsd RSS")
	if th.State != "gap" || !strings.Contains(th.Detail, "darwin") {
		t.Errorf("fseventsd RSS off darwin = %+v, want a gap naming darwin", th)
	}
	if got := vital(t, s, "process RSS").State; got != "over" {
		t.Errorf("process RSS = %q, want over (a 30 GiB process is over 20 GiB)", got)
	}
}

// The process RSS vital is the largest process against 20 GiB, strict, and its
// detail names the process by command name and pid only.
func TestProcessRSSNamesTheLargestProcess(t *testing.T) {
	s := sampleProcs(t, []Proc{pr(41, "job", 21*oneGiB), pr(42, "small", oneGiB)}, true)
	th := vital(t, s, "process RSS")
	if th.State != "over" || th.Line != "over 20 GiB" {
		t.Errorf("process RSS = %+v, want over, line %q", th, "over 20 GiB")
	}
	if !strings.Contains(th.Detail, "job") || !strings.Contains(th.Detail, "41") || !strings.Contains(th.Detail, "21.0 GiB") {
		t.Errorf("detail = %q, want the name, pid and size of the largest process", th.Detail)
	}
	s = sampleProcs(t, []Proc{pr(41, "job", 20*oneGiB)}, true)
	if got := vital(t, s, "process RSS").State; got != "under" {
		t.Errorf("exactly 20 GiB: state %q, want under", got)
	}
}

// A table that could not be read is a gap on both vitals, never a zero.
func TestNoTableIsAGapNotAZero(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "darwin"
	s := sampleProcs(t, nil, false)
	for _, name := range []string{"fseventsd RSS", "process RSS"} {
		th := vital(t, s, name)
		if th.State != "gap" || !strings.Contains(th.Detail, "process table") {
			t.Errorf("%s = %+v, want a gap naming the process table", name, th)
		}
	}
	if len(s.Snapshot().Top) != 0 {
		t.Errorf("top = %+v, want none without a table", s.Snapshot().Top)
	}
}

// A crossing is reported once when the line is passed and once when it is no
// longer passed, for each vital on its own, and the swap events are as before.
func TestProcessCrossingsAndClears(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "darwin"

	rec := &recorder{}
	s := New(true, func() Reading { return swapReading(1, 100) }, rec)
	s.SampleProcs([]Proc{pr(10, "fseventsd", oneGiB)}, true)
	if len(rec.got) != 0 {
		t.Fatalf("under every line, events = %v, want none", rec.kinds())
	}
	s.SampleProcs([]Proc{pr(10, "fseventsd", 5*oneGiB), pr(41, "job", 21*oneGiB)}, true)
	if got := rec.kinds(); len(got) != 2 || got[0] != events.KindHostVitalsCrossed || got[1] != events.KindHostVitalsCrossed {
		t.Fatalf("two lines passed, events = %v, want two crossed", got)
	}
	var msgs string
	for _, e := range rec.got {
		msgs += e.Message + "\n"
		if e.Severity != events.SeverityWarning {
			t.Errorf("crossing severity = %v, want warning", e.Severity)
		}
	}
	for _, want := range []string{"fseventsd RSS", "process RSS", "job", "41"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("crossing messages lack %q:\n%s", want, msgs)
		}
	}
	n := len(rec.got)
	s.SampleProcs([]Proc{pr(10, "fseventsd", 6*oneGiB), pr(41, "job", 22*oneGiB)}, true)
	if len(rec.got) != n {
		t.Errorf("still over, events grew to %d, want %d", len(rec.got), n)
	}
	s.SampleProcs([]Proc{pr(10, "fseventsd", oneGiB)}, true)
	if got := rec.kinds()[n:]; len(got) != 2 || got[0] != events.KindHostVitalsCleared || got[1] != events.KindHostVitalsCleared {
		t.Errorf("both back under, new events = %v, want two cleared", got)
	}
}

// A sample whose table could not be read is a gap: it neither crosses nor
// clears, as a reading with no swap figure does not.
func TestAGapChangesNeitherTheStateNorTheEvents(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "darwin"
	rec := &recorder{}
	s := New(true, func() Reading { return swapReading(1, 100) }, rec)
	s.SampleProcs([]Proc{pr(41, "job", 21*oneGiB)}, true)
	n := len(rec.got)
	if n != 1 {
		t.Fatalf("crossing the process line gave %d events, want 1", n)
	}
	s.SampleProcs(nil, false)
	s.SampleProcs([]Proc{pr(41, "job", 22*oneGiB)}, true)
	if len(rec.got) != n {
		t.Errorf("events = %v, want no new event across a gap while still over", rec.kinds())
	}
}

// The two vitals this build samples are no longer gap lines; the two it does
// not sample still are, each with its design section.
func TestOnlyUnsampledVitalsRemainGapLines(t *testing.T) {
	s := sampleProcs(t, []Proc{pr(1, "x", oneGiB)}, true)
	var got []string
	for _, g := range s.Snapshot().Gaps {
		got = append(got, g.Vital+" "+g.Section)
	}
	want := []string{"orphan CPU 2.1", "SSD wear 2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("gaps = %v, want %v", got, want)
	}
}
