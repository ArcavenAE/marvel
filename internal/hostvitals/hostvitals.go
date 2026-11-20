// Package hostvitals samples the host's own swap and memory pressure and
// reports them, nothing more (docs/design/host-vitals.md). It is off unless
// the cluster config turns it on.
//
// The sampler reads and reports. It holds no way to signal, kill, restart or
// refuse anything, so a reading cannot gate a spawn or touch a process
// (docs/design/host-health-guard.md HH-1, host-vitals.md section 5).
package hostvitals

import (
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// SwapCrossPercent is the line over which swap use is reported as a crossing:
// more than this share of the host's total swap. It is a declared constant and
// a diagnostic, never a gate (host-vitals.md section 4); the first two weeks
// of samples re-check it on 2026-11-16.
const SwapCrossPercent = 50

// Reading is one look at the host. A field group is meaningful only when its
// OK flag is true; a host that cannot say leaves the group zero.
type Reading struct {
	// SwapOK is true when the host reported its swap. SwapTotal is 0 on a
	// host with no swap configured.
	SwapOK    bool   `json:"swap_ok"`
	SwapUsed  uint64 `json:"swap_used"`  // bytes
	SwapTotal uint64 `json:"swap_total"` // bytes

	// KernelPressureOK is true on macOS, where the kernel reports a pressure
	// level (1 normal, 2 warning, 4 critical) and the free share of memory
	// behind the figure `memory_pressure` prints.
	KernelPressureOK bool `json:"kernel_pressure_ok"`
	PressureLevel    int  `json:"pressure_level"`
	MemoryFreePct    int  `json:"memory_free_pct"`

	// PSIOK is true on Linux when /proc/pressure/memory is present. PSISomeAvg10
	// is the share of the last 10 seconds in which some task stalled on memory.
	PSIOK        bool    `json:"psi_ok"`
	PSISomeAvg10 float64 `json:"psi_some_avg10"`
}

// swapOver reports whether the reading is over the swap line. A reading with
// no swap figure, or a host with no swap, is never over it.
func (r Reading) swapOver() bool {
	return r.SwapOK && r.SwapTotal > 0 && r.SwapUsed*100 > r.SwapTotal*SwapCrossPercent
}

// Sampler holds the latest reading in memory and reports crossings. The zero
// value is not usable; build one with New.
type Sampler struct {
	enabled bool
	read    func() Reading
	emit    events.Emitter
	// now is the clock the sample time comes from; a test sets it.
	now func() time.Time

	mu       sync.Mutex
	latest   Reading
	latestAt time.Time
	have     bool

	// The process table of the latest sample: whether it could be read, the
	// top processes by RSS and the summed RSS of the fseventsd processes.
	procsOK       bool
	top           []Proc
	fseventsdRSS  uint64
	fseventsdSeen bool

	// over holds, per vital, whether its last reading was over its line.
	over map[string]bool
}

// New returns a sampler. read is the host reader (Read on a real host). A
// sampler built with enabled false never calls read and never emits.
func New(enabled bool, read func() Reading, emit events.Emitter) *Sampler {
	return &Sampler{enabled: enabled, read: read, emit: emit, now: time.Now, over: map[string]bool{}}
}

// Enabled reports whether the sampler was turned on.
func (s *Sampler) Enabled() bool { return s != nil && s.enabled }

// Sample takes one reading with no process table: the process vitals read as a
// gap. It is SampleProcs(nil, false).
func (s *Sampler) Sample() { s.SampleProcs(nil, false) }

// SampleProcs takes one reading, keeps it with the process table the caller
// read, and reports a crossing or a clear for each vital that moved to the
// other side of its line since the last reading that had a figure for it. A
// vital with no figure (no swap figure, no process table) is a gap: it changes
// neither the standing state nor the events. ok is false when the table could
// not be read.
func (s *Sampler) SampleProcs(procs []Proc, ok bool) {
	if !s.Enabled() {
		return
	}
	r := s.read()
	top, fsev, fseen := summarize(procs)

	s.mu.Lock()
	s.latest, s.have = r, true
	s.latestAt = s.now()
	s.procsOK, s.top, s.fseventsdRSS, s.fseventsdSeen = ok, nil, 0, false
	if ok {
		s.top, s.fseventsdRSS, s.fseventsdSeen = top, fsev, fseen
	}
	var out []events.Event
	move := func(vital string, over bool, crossed, cleared, detail string) {
		was := s.over[vital]
		s.over[vital] = over
		switch {
		case over && !was:
			out = append(out, events.Event{Kind: events.KindHostVitalsCrossed, Severity: events.SeverityWarning, Message: crossed + ": " + detail})
		case !over && was:
			out = append(out, events.Event{Kind: events.KindHostVitalsCleared, Severity: events.SeverityInfo, Message: cleared + ": " + detail})
		}
	}
	if r.SwapOK {
		move("swap", r.swapOver(), "host swap over its line", "host swap back under its line", swapDetail(r))
	}
	if ok && hostOS == "darwin" {
		move("fseventsd", fsev > FseventsdCrossBytes, "fseventsd RSS over its line", "fseventsd RSS back under its line", gib(fsev))
	}
	if ok && len(top) > 0 {
		move("process", top[0].RSSBytes > ProcessRSSCrossBytes, "process RSS over its line", "process RSS back under its line", procDetail(top[0]))
	}
	s.mu.Unlock()

	for _, e := range out {
		events.Emit(s.emit, e)
	}
}

// Latest returns the most recent reading, and false before the first sample.
func (s *Sampler) Latest() (Reading, bool) {
	if s == nil {
		return Reading{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest, s.have
}
