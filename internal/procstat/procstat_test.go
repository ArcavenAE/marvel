package procstat

import (
	"math"
	"os"
	"testing"
	"time"
)

// paneTree models what tmux actually leaves behind: pane_pid 100 is the
// shell, 200 is the agent binary it exec'd, 300 is a tool the agent
// spawned. 999 is an unrelated process that must not be counted.
func paneTree() []entry {
	return []entry{
		{pid: 1, ppid: 0, cpuPercent: 0.1, rssBytes: 1 << 20},
		{pid: 100, ppid: 1, cpuPercent: 0.5, rssBytes: 2 << 20},
		{pid: 200, ppid: 100, cpuPercent: 12.0, rssBytes: 400 << 20},
		{pid: 300, ppid: 200, cpuPercent: 3.5, rssBytes: 30 << 20},
		{pid: 999, ppid: 1, cpuPercent: 90.0, rssBytes: 900 << 20},
	}
}

func TestRollupSumsSubtree(t *testing.T) {
	s := NewSampler()
	got := s.rollup([]int{100}, paneTree(), time.Now())

	sample, ok := got[100]
	if !ok {
		t.Fatal("no sample for root pid 100")
	}
	if sample.Procs != 3 {
		t.Errorf("Procs = %d, want 3 (shell + agent + tool)", sample.Procs)
	}
	if math.Abs(sample.CPUPercent-16.0) > 0.001 {
		t.Errorf("CPUPercent = %v, want 16.0", sample.CPUPercent)
	}
	if want := int64(432 << 20); sample.RSSBytes != want {
		t.Errorf("RSSBytes = %d, want %d", sample.RSSBytes, want)
	}
	if sample.IOAvailable {
		t.Error("IOAvailable is true with no IO-carrying entries")
	}
}

// A single-pid read of pane_pid is what marvel did before this sampler
// existed; this pins the difference the rollup buys.
func TestRollupRootAloneIsNotTheWholeStory(t *testing.T) {
	s := NewSampler()
	got := s.rollup([]int{100}, paneTree(), time.Now())
	if got[100].CPUPercent <= 0.5 {
		t.Errorf("subtree CPU %v did not exceed the shell's own 0.5", got[100].CPUPercent)
	}
}

func TestRollupMissingRootReportsZero(t *testing.T) {
	s := NewSampler()
	got := s.rollup([]int{4242}, paneTree(), time.Now())
	sample, ok := got[4242]
	if !ok {
		t.Fatal("absent root omitted from result; callers cannot tell it was measured")
	}
	if sample.Procs != 0 || sample.CPUPercent != 0 || sample.RSSBytes != 0 {
		t.Errorf("got %+v, want zero-valued sample", sample)
	}
}

func TestRollupMultipleRoots(t *testing.T) {
	s := NewSampler()
	got := s.rollup([]int{100, 999}, paneTree(), time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	if got[999].Procs != 1 {
		t.Errorf("pid 999 Procs = %d, want 1", got[999].Procs)
	}
	if math.Abs(got[999].CPUPercent-90.0) > 0.001 {
		t.Errorf("pid 999 CPUPercent = %v, want 90.0", got[999].CPUPercent)
	}
}

// A /proc scan is not atomic: pid reuse mid-walk can produce a parent
// edge that points back into the subtree. The walk must terminate.
func TestRollupSurvivesParentCycle(t *testing.T) {
	entries := []entry{
		{pid: 100, ppid: 300, rssBytes: 1},
		{pid: 200, ppid: 100, rssBytes: 1},
		{pid: 300, ppid: 200, rssBytes: 1},
	}
	done := make(chan Sample, 1)
	go func() {
		s := NewSampler()
		done <- s.rollup([]int{100}, entries, time.Now())[100]
	}()
	select {
	case sample := <-done:
		if sample.Procs != 3 {
			t.Errorf("Procs = %d, want 3 (each process counted once)", sample.Procs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rollup did not terminate on a cyclic parent chain")
	}
}

// The linux reader reports cumulative ticks, so a percentage only exists
// once two passes have been taken. 300 ticks over 3s at 100 ticks/s is
// one full core.
func TestRollupCumulativeCPUNeedsTwoPasses(t *testing.T) {
	tree := func(ticks uint64) []entry {
		return []entry{
			{pid: 100, ppid: 1, cpuTicks: 10, cumulative: true, rssBytes: 1 << 20},
			{pid: 200, ppid: 100, cpuTicks: ticks, cumulative: true, rssBytes: 1 << 20},
		}
	}
	s := NewSampler()
	t0 := time.Now()

	first := s.rollup([]int{100}, tree(1000), t0)
	if first[100].CPUPercent != 0 {
		t.Errorf("first pass CPUPercent = %v, want 0 (no prior reading)", first[100].CPUPercent)
	}

	second := s.rollup([]int{100}, tree(1300), t0.Add(3*time.Second))
	if math.Abs(second[100].CPUPercent-100.0) > 0.001 {
		t.Errorf("second pass CPUPercent = %v, want 100.0", second[100].CPUPercent)
	}
}

// pid reuse hands the sampler a counter lower than the one it stored.
// Reporting a negative or wrapped percentage would be worse than
// reporting nothing for one interval.
func TestRollupCumulativeCPUHandlesPIDReuse(t *testing.T) {
	s := NewSampler()
	t0 := time.Now()
	high := []entry{{pid: 100, ppid: 1, cpuTicks: 50000, cumulative: true}}
	s.rollup([]int{100}, high, t0)

	low := []entry{{pid: 100, ppid: 1, cpuTicks: 3, cumulative: true}}
	got := s.rollup([]int{100}, low, t0.Add(3*time.Second))
	if got[100].CPUPercent != 0 {
		t.Errorf("CPUPercent = %v after counter went backwards, want 0", got[100].CPUPercent)
	}
}

// prev must not accumulate dead pids for the life of the daemon.
func TestRollupForgetsVanishedPIDs(t *testing.T) {
	s := NewSampler()
	t0 := time.Now()
	s.rollup([]int{100}, []entry{
		{pid: 100, ppid: 1, cpuTicks: 1, cumulative: true},
		{pid: 200, ppid: 100, cpuTicks: 1, cumulative: true},
	}, t0)
	s.rollup([]int{100}, []entry{
		{pid: 100, ppid: 1, cpuTicks: 2, cumulative: true},
	}, t0.Add(time.Second))
	if len(s.prev) != 1 {
		t.Errorf("prev holds %d pids, want 1", len(s.prev))
	}
	if _, ok := s.prev[200]; ok {
		t.Error("prev still holds pid 200 after it left the process table")
	}
}

func TestRollupIO(t *testing.T) {
	s := NewSampler()
	entries := []entry{
		{pid: 100, ppid: 1, ioReadBytes: 1000, ioWriteBytes: 10, ioOK: true},
		{pid: 200, ppid: 100, ioReadBytes: 2000, ioWriteBytes: 20, ioOK: true},
		// Owned by another uid: /proc/<pid>/io was unreadable.
		{pid: 300, ppid: 200, ioReadBytes: 0, ioWriteBytes: 0, ioOK: false},
	}
	got := s.rollup([]int{100}, entries, time.Now())[100]
	if !got.IOAvailable {
		t.Fatal("IOAvailable is false with two readable counters")
	}
	if got.IOReadBytes != 3000 || got.IOWriteBytes != 30 {
		t.Errorf("IO = %d read / %d write, want 3000 / 30", got.IOReadBytes, got.IOWriteBytes)
	}
}

func TestSampleNoRoots(t *testing.T) {
	s := NewSampler()
	got, err := s.Sample(nil)
	if err != nil {
		t.Fatalf("Sample(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Sample(nil) returned %d samples, want 0", len(got))
	}
}

// End-to-end against the live process table: the test's own process must
// show up with a nonzero RSS. Skipped where there is no reader.
func TestSampleSelf(t *testing.T) {
	s := NewSampler()
	got, err := s.Sample([]int{os.Getpid()})
	if err != nil {
		t.Skipf("no process table reader on this platform: %v", err)
	}
	sample := got[os.Getpid()]
	if sample.Procs < 1 {
		t.Fatalf("Procs = %d for the running test process, want at least 1", sample.Procs)
	}
	if sample.RSSBytes <= 0 {
		t.Errorf("RSSBytes = %d for the running test process, want > 0", sample.RSSBytes)
	}
}
