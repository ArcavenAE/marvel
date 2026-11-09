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
	SwapOK    bool
	SwapUsed  uint64 // bytes
	SwapTotal uint64 // bytes

	// KernelPressureOK is true on macOS, where the kernel reports a pressure
	// level (1 normal, 2 warning, 4 critical) and the free share of memory
	// behind the figure `memory_pressure` prints.
	KernelPressureOK bool
	PressureLevel    int
	MemoryFreePct    int

	// PSIOK is true on Linux when /proc/pressure/memory is present. PSISomeAvg10
	// is the share of the last 10 seconds in which some task stalled on memory.
	PSIOK        bool
	PSISomeAvg10 float64
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

	mu       sync.Mutex
	latest   Reading
	have     bool
	swapOver bool
}

// New returns a sampler. read is the host reader (Read on a real host). A
// sampler built with enabled false never calls read and never emits.
func New(enabled bool, read func() Reading, emit events.Emitter) *Sampler {
	return &Sampler{enabled: enabled, read: read, emit: emit}
}

// Enabled reports whether the sampler was turned on.
func (s *Sampler) Enabled() bool { return s != nil && s.enabled }

// Sample takes one reading, keeps it, and reports a swap crossing or clear if
// the reading moved to the other side of the line since the last one that had
// a swap figure. A reading with no swap figure is a gap: it changes neither
// the standing state nor the events.
func (s *Sampler) Sample() {
	if !s.Enabled() {
		return
	}
	r := s.read()

	s.mu.Lock()
	s.latest, s.have = r, true
	var kind events.Kind
	var msg string
	if r.SwapOK {
		over := r.swapOver()
		switch {
		case over && !s.swapOver:
			kind, msg = events.KindHostVitalsCrossed, "host swap over its line"
		case !over && s.swapOver:
			kind, msg = events.KindHostVitalsCleared, "host swap back under its line"
		}
		s.swapOver = over
	}
	s.mu.Unlock()

	if kind == "" {
		return
	}
	sev := events.SeverityInfo
	if kind == events.KindHostVitalsCrossed {
		sev = events.SeverityWarning
	}
	events.Emit(s.emit, events.Event{
		Kind:     kind,
		Severity: sev,
		Message:  msg + ": " + swapDetail(r),
	})
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
