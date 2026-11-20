package hostvitals

import (
	"fmt"
	"time"
)

// Threshold is one line the sampler holds, with where the latest reading
// stands against it. State is "under", "over", "gap" (the host gave no figure)
// or "none" (a vital the design names no line for).
type Threshold struct {
	Name   string `json:"name"`
	Line   string `json:"line"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Gap is a vital the design lists and the sampler does not hold yet. Section
// is the section of docs/design/host-vitals.md that names it, so the build
// that adds the vital removes the line.
type Gap struct {
	Vital   string `json:"vital"`
	Section string `json:"section"`
	Reason  string `json:"reason"`
}

// Snapshot is what `marvel get vitals` shows: the latest reading, its time,
// each threshold with its state and each gap. It is built from memory only.
type Snapshot struct {
	Enabled    bool        `json:"enabled"`
	Sampled    bool        `json:"sampled"`
	At         *time.Time  `json:"at,omitempty"`
	Reading    Reading     `json:"reading"`
	Thresholds []Threshold `json:"thresholds,omitempty"`
	Top        []Proc      `json:"top_processes,omitempty"`
	Orphans    []Orphan    `json:"orphans,omitempty"`
	Gaps       []Gap       `json:"gaps,omitempty"`
}

// notSampled is the reason every gap line carries today.
const notSampled = "not sampled yet"

// gaps are the vitals the design lists (section 2) and the sampler does not
// hold yet, in the design's table order. A build that samples one removes its
// line here.
func gaps() []Gap {
	return []Gap{
		{Vital: "SSD wear", Section: "2", Reason: notSampled},
	}
}

// Snapshot returns the sampler's state. It does not read the host. A sampler
// that is off, or a nil one, answers the zero snapshot: enabled false and
// nothing else.
func (s *Sampler) Snapshot() Snapshot {
	if !s.Enabled() {
		return Snapshot{}
	}
	snap := Snapshot{Enabled: true, Gaps: gaps()}
	s.mu.Lock()
	have, r, at := s.have, s.latest, s.latestAt.UTC()
	procsOK, top, fsev, fseen := s.procsOK, append([]Proc(nil), s.top...), s.fseventsdRSS, s.fseventsdSeen
	orphans := s.orphans
	s.mu.Unlock()
	if !have {
		return snap
	}
	snap.Sampled = true
	snap.At = &at
	snap.Reading = r
	snap.Thresholds = []Threshold{swapThreshold(r), pressureThreshold(r), fseventsdThreshold(procsOK, fsev, fseen), processThreshold(procsOK, top), orphanThreshold(procsOK, orphans)}
	if len(top) > 0 {
		snap.Top = top
	}
	if len(orphans.list) > 0 {
		snap.Orphans = append([]Orphan(nil), orphans.list...)
	}
	return snap
}

// swapThreshold reports the swap line against the reading.
func swapThreshold(r Reading) Threshold {
	t := Threshold{Name: "swap used", Line: fmt.Sprintf("over %d%% of total", SwapCrossPercent)}
	switch {
	case !r.SwapOK:
		t.State, t.Detail = "gap", "the host gave no swap figure"
	case r.SwapTotal == 0:
		t.State, t.Detail = "under", "no swap configured"
	default:
		t.State = "under"
		if r.swapOver() {
			t.State = "over"
		}
		t.Detail = fmt.Sprintf("%s of %s (%d%%)", gib(r.SwapUsed), gib(r.SwapTotal), r.SwapUsed*100/r.SwapTotal)
	}
	return t
}

// pressureThreshold reports memory pressure. The design names no line for it
// (section 4), so a figure shows with state none and never a made-up line.
func pressureThreshold(r Reading) Threshold {
	t := Threshold{Name: "memory pressure", Line: "no line", State: "none"}
	switch {
	case r.KernelPressureOK:
		t.Detail = fmt.Sprintf("level %d (%s), %d%% free", r.PressureLevel, levelName(r.PressureLevel), r.MemoryFreePct)
	case r.PSIOK:
		t.Detail = fmt.Sprintf("some tasks stalled on memory %.2f%% of the last 10 s", r.PSISomeAvg10)
	default:
		t.State, t.Detail = "gap", "the host gave no pressure figure"
	}
	return t
}

// levelName names a kernel pressure level (1 normal, 2 warning, 4 critical).
func levelName(l int) string {
	switch l {
	case 1:
		return "normal"
	case 2:
		return "warning"
	case 4:
		return "critical"
	}
	return "unknown"
}

// gib formats bytes as GiB with one decimal.
func gib(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

// noTable is the detail of a process vital with no process table to read.
const noTable = "the process table was not read"

// fseventsdThreshold reports the summed fseventsd RSS against its line. The
// vital is darwin only, so elsewhere it is a gap that says so.
func fseventsdThreshold(ok bool, rss uint64, seen bool) Threshold {
	t := Threshold{Name: "fseventsd RSS", Line: fmt.Sprintf("over %d GiB", FseventsdCrossBytes>>30)}
	switch {
	case hostOS != "darwin":
		t.State, t.Detail = "gap", "darwin only: fseventsd is a macOS process"
	case !ok:
		t.State, t.Detail = "gap", noTable
	case !seen:
		t.State, t.Detail = "under", "no fseventsd process"
	default:
		t.State, t.Detail = "under", gib(rss)
		if rss > FseventsdCrossBytes {
			t.State = "over"
		}
	}
	return t
}

// processThreshold reports the largest process's RSS against its line, naming
// it by command name and pid.
func processThreshold(ok bool, top []Proc) Threshold {
	t := Threshold{Name: "process RSS", Line: fmt.Sprintf("over %d GiB", ProcessRSSCrossBytes>>30)}
	switch {
	case !ok:
		t.State, t.Detail = "gap", noTable
	case len(top) == 0:
		t.State, t.Detail = "under", "no processes read"
	default:
		t.State, t.Detail = "under", procDetail(top[0])
		if top[0].RSSBytes > ProcessRSSCrossBytes {
			t.State = "over"
		}
	}
	return t
}

// orphanThreshold reports the orphan CPU vital: under with no orphans or none
// over the line, over naming the worst one, or a gap when no table was read.
func orphanThreshold(ok bool, v orphanView) Threshold {
	t := Threshold{Name: "orphan CPU", Line: fmt.Sprintf("over %d%% of a core for %d minutes", int(OrphanShare*100), int(OrphanWindow/time.Minute))}
	switch {
	case !ok:
		t.State, t.Detail = "gap", noTable
	case v.over:
		t.State, t.Detail = "over", v.detail
	default:
		t.State, t.Detail = "under", v.detail
		if t.Detail == "" {
			t.Detail = "no orphans"
		}
	}
	return t
}
