// Package procstat samples CPU and memory for a process subtree.
//
// marvel rolls up a subtree rather than reading one pid because tmux
// execs the runtime command through a shell: pane_pid is the shell, and
// the agent binary is its child (often with children of its own). A
// single-pid read of pane_pid reports a shell sitting idle at a few
// kilobytes while the agent underneath it burns a core.
//
// The package shells out to ps on darwin and reads /proc on linux. No
// cgo, no third-party dependency: marvel's dependency list is short on
// purpose.
//
// Platform honesty, per marvel B13: the darwin path is verified against
// binaries built by go build and go test. It has not been run from a
// signed and notarized release build under the hardened runtime.
// Shelling out to ps rather than calling proc_pidinfo means no
// entitlement should be implicated, but that is an argument, not a
// measurement, and the release build is where it gets settled.
package procstat

import (
	"errors"
	"time"
)

// ErrUnsupported is returned by Sample on platforms with no process
// table reader. Callers should treat it as "no metrics here", not as a
// failure worth retrying.
var ErrUnsupported = errors.New("procstat: unsupported platform")

// Sample is one rollup over a session's process subtree.
type Sample struct {
	// Procs counts the processes rolled up, root included. Zero means
	// the root pid was absent from the process table, which for a
	// marvel session means the agent has exited.
	Procs int

	CPUPercent float64
	RSSBytes   int64

	// IOReadBytes and IOWriteBytes are cumulative block-layer bytes
	// since process start, summed over the subtree. Only meaningful
	// when IOAvailable is true, which today means linux: darwin has no
	// per-process byte counter reachable without a privileged
	// interface, and reporting zero there would read as "idle" rather
	// than "not measured".
	IOReadBytes  int64
	IOWriteBytes int64
	IOAvailable  bool
}

// entry is one process as read from the platform's process table.
type entry struct {
	pid  int
	ppid int

	// cpuPercent is a ready-made utilization estimate (darwin's ps
	// reports the kernel's own decaying average). Used when cumulative
	// is false.
	cpuPercent float64
	// cpuTicks is cumulative user+system time in clock ticks, used when
	// cumulative is true. A percentage comes from the delta between two
	// passes, so the first pass after daemon start reports zero.
	cpuTicks   uint64
	cumulative bool

	rssBytes     int64
	ioReadBytes  int64
	ioWriteBytes int64
	ioOK         bool
}

// clockTicksPerSecond is the divisor for cumulative CPU ticks. POSIX
// exposes this as sysconf(_SC_CLK_TCK); reading it needs cgo, and the
// value has been 100 on every Linux port since the kernel started
// reporting USER_HZ-scaled times regardless of the internal HZ setting.
const clockTicksPerSecond = 100.0

// Sampler holds the state one platform needs across passes: the
// previous cumulative CPU reading per pid. Not safe for concurrent use;
// marvel drives it from a single goroutine.
type Sampler struct {
	prev   map[int]uint64
	prevAt time.Time
}

// NewSampler returns a sampler with no history. Its first Sample call
// reports zero CPU on platforms that only expose cumulative CPU time.
func NewSampler() *Sampler {
	return &Sampler{prev: make(map[int]uint64)}
}

// Sample reads the process table once and returns a rollup per root
// pid. Roots absent from the table get a zero-valued Sample rather than
// being omitted, so callers can tell "measured, nothing there" from
// "not measured".
func (s *Sampler) Sample(roots []int) (map[int]Sample, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	entries, err := readProcesses()
	if err != nil {
		return nil, err
	}
	return s.rollup(roots, entries, time.Now()), nil
}

// rollup is the platform-independent half of a pass: index the process
// table, walk each root's descendants, sum. Split out from Sample so
// tests can drive it with a fixture process table on any platform.
func (s *Sampler) rollup(roots []int, entries []entry, now time.Time) map[int]Sample {
	elapsed := now.Sub(s.prevAt).Seconds()

	byPID := make(map[int]entry, len(entries))
	children := make(map[int][]int, len(entries))
	next := make(map[int]uint64, len(entries))
	for _, e := range entries {
		byPID[e.pid] = e
		if e.ppid != e.pid {
			children[e.ppid] = append(children[e.ppid], e.pid)
		}
		if e.cumulative {
			next[e.pid] = e.cpuTicks
		}
	}

	out := make(map[int]Sample, len(roots))
	for _, root := range roots {
		out[root] = s.walk(root, byPID, children, elapsed)
	}

	// Replace rather than merge: pids die, and a sampler that
	// remembered every pid it ever saw would grow for the life of the
	// daemon.
	s.prev = next
	s.prevAt = now
	return out
}

// walk sums a root and its descendants. seen guards against a parent
// cycle, which a /proc scan can synthesize when pids are reused
// mid-walk.
func (s *Sampler) walk(root int, byPID map[int]entry, children map[int][]int, elapsed float64) Sample {
	var sum Sample
	seen := make(map[int]bool)
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true

		e, ok := byPID[pid]
		if !ok {
			continue
		}
		sum.Procs++
		sum.CPUPercent += s.percentFor(e, elapsed)
		sum.RSSBytes += e.rssBytes
		if e.ioOK {
			sum.IOAvailable = true
			sum.IOReadBytes += e.ioReadBytes
			sum.IOWriteBytes += e.ioWriteBytes
		}
		stack = append(stack, children[pid]...)
	}
	return sum
}

// percentFor converts one process's CPU reading into a percentage of a
// single core. Cumulative sources need two passes; a counter that went
// backwards means the pid was reused, so the reading starts over.
func (s *Sampler) percentFor(e entry, elapsed float64) float64 {
	if !e.cumulative {
		return e.cpuPercent
	}
	if elapsed <= 0 {
		return 0
	}
	prev, ok := s.prev[e.pid]
	if !ok || e.cpuTicks < prev {
		return 0
	}
	return 100 * (float64(e.cpuTicks-prev) / clockTicksPerSecond) / elapsed
}
