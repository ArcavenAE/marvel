package hostvitals

import (
	"fmt"
	"strings"
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
// that adds the vital removes the line. The design lists none of that kind
// today (weekly SSD wear is out of scope), so nothing fills Snapshot.Gaps.
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

// Snapshot returns the sampler's state. It does not read the host. A sampler
// that is off, or a nil one, answers the zero snapshot: enabled false and
// nothing else.
func (s *Sampler) Snapshot() Snapshot {
	if !s.Enabled() {
		return Snapshot{}
	}
	snap := Snapshot{Enabled: true}
	s.mu.Lock()
	have, r, at := s.have, s.latest, s.latestAt.UTC()
	procsOK, top, fsev, fseen := s.procsOK, append([]Proc(nil), s.top...), s.fseventsdRSS, s.fseventsdSeen
	orphans := s.orphans
	seats, seatRSS := s.seatCount, s.seatRSS
	disk := append([]diskLine(nil), s.disk...)
	var delta *uint64
	if s.haveIns {
		d := s.deltaIns
		delta = &d
	}
	s.mu.Unlock()
	if !have {
		return snap
	}
	snap.Sampled = true
	snap.At = &at
	snap.Reading = r
	snap.Thresholds = []Threshold{
		swapThresholdIns(r, delta), pressureThreshold(r), seatMemoryThreshold(procsOK, seats, seatRSS, r.MemTotal), diskThreshold(disk),
		fseventsdThreshold(procsOK, fsev, fseen), processThreshold(procsOK, top), orphanThreshold(procsOK, orphans), orphanMemoryThreshold(procsOK, orphans),
	}
	if len(top) > 0 {
		snap.Top = top
	}
	if len(orphans.list) > 0 {
		snap.Orphans = append([]Orphan(nil), orphans.list...)
	}
	return snap
}

// swapThreshold reports the swap line against the reading, with no earlier
// sample to take swap-ins against.
func swapThreshold(r Reading) Threshold { return swapThresholdIns(r, nil) }

// swapThresholdIns is swapThreshold with the swap-ins since the last sample,
// when two samples carried the counter. Swap is shown as GiB used and swap-ins
// as a count between samples; the line itself still reads a share of the total.
func swapThresholdIns(r Reading, delta *uint64) Threshold {
	t := Threshold{Name: "swap used", Line: fmt.Sprintf("over %d%% of total", SwapCrossPercent)}
	switch {
	case !r.SwapOK:
		t.State, t.Detail = "gap", "the host gave no swap figure"
	case r.SwapTotal == 0 && hostOS != "darwin":
		t.State, t.Detail = "under", "no swap configured"
	default:
		t.State = "under"
		if r.swapOver() {
			t.State = "over"
		}
		t.Detail = usedGiB(r.SwapUsed)
		switch {
		case !r.SwapInsOK:
			t.Detail += "; swap-ins not available on this host"
		case delta == nil:
			t.Detail += "; swap-ins: first sample"
		default:
			t.Detail += fmt.Sprintf("; %d swap-ins since the last sample", *delta)
		}
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

// seatMemoryThreshold sums the seats' RSS against the host's physical memory.
// The design names no line for it, so a figure shows with state none.
func seatMemoryThreshold(ok bool, seats int, rss, total uint64) Threshold {
	t := Threshold{Name: "seat memory", Line: "no line", State: "none"}
	switch {
	case !ok:
		t.State, t.Detail = "gap", noTable
	case total == 0:
		t.Detail = fmt.Sprintf("%d seats, %s", seats, gib(rss))
	default:
		t.Detail = fmt.Sprintf("%d seats, %s of %s (%d%%)", seats, gib(rss), gib(total), rss*100/total)
	}
	return t
}

// orphanMemoryThreshold sums the RSS of the listed orphans, beside orphan CPU.
func orphanMemoryThreshold(ok bool, v orphanView) Threshold {
	t := Threshold{Name: "orphan memory", Line: "no line", State: "none"}
	if !ok {
		t.State, t.Detail = "gap", noTable
		return t
	}
	if len(v.list) == 0 {
		t.Detail = "no orphans"
		return t
	}
	var rss uint64
	for _, o := range v.list {
		rss += o.RSSBytes
	}
	t.Detail = fmt.Sprintf("%d orphans, %s", len(v.list), gib(rss))
	return t
}

// diskLine is one path's reading: its volume's size and free space, or the
// reason it could not be read.
type diskLine struct {
	path        string
	total, free uint64
	err         error
}

// DiskStat reports the total and free bytes of the volume that holds path.
type DiskStat func(path string) (total, free uint64, err error)

// SetDiskPaths names the paths whose volumes the disk free vital reports, and
// the reader for them.
func (s *Sampler) SetDiskPaths(paths []string, stat DiskStat) {
	fixed := append([]string(nil), paths...)
	s.SetDiskPathsFunc(func() []string { return fixed }, stat)
}

// SetDiskPathsFunc is SetDiskPaths with the paths asked for on each sample, for
// a set that changes while the daemon runs.
func (s *Sampler) SetDiskPathsFunc(paths func() []string, stat DiskStat) {
	s.mu.Lock()
	s.diskPaths, s.diskStat = paths, stat
	s.mu.Unlock()
}

// readDisks stats each named path once.
func readDisks(paths func() []string, stat DiskStat) []diskLine {
	if paths == nil || stat == nil {
		return nil
	}
	var out []diskLine
	for _, p := range paths() {
		l := diskLine{path: p}
		l.total, l.free, l.err = stat(p)
		out = append(out, l)
	}
	return out
}

// diskThreshold reports each path's volume. The design names no line for it.
func diskThreshold(lines []diskLine) Threshold {
	t := Threshold{Name: "disk free", Line: "no line", State: "none"}
	if len(lines) == 0 {
		t.State, t.Detail = "gap", "no path named to read"
		return t
	}
	var parts []string
	for _, l := range lines {
		switch {
		case l.err != nil || l.total == 0:
			parts = append(parts, l.path+": unreadable")
		default:
			parts = append(parts, fmt.Sprintf("%s: %s free, %d%% used", l.path, gib(l.free), (l.total-min(l.free, l.total))*100/l.total))
		}
	}
	t.Detail = strings.Join(parts, "; ")
	return t
}
