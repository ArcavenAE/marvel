package hostvitals

import (
	"fmt"
	"sort"
	"time"

	"github.com/arcavenae/marvel/internal/procargs"
)

// The orphan CPU vital (docs/design/host-vitals.md sections 2.1 and 4): a
// process that lost its marvel ancestry and burns CPU. Over 90% of a core for
// 30 minutes is the line.
const (
	// OrphanWindow is how long an orphan must have burned CPU for the vital to cross.
	OrphanWindow = 30 * time.Minute
	// OrphanShare is the share of one core over the window that crosses the line, strict.
	OrphanShare = 0.9
	// OrphansShown caps the orphans a snapshot lists, most CPU since marking first.
	OrphansShown = 20
)

// Why an orphan was marked.
const (
	// ReasonSeatTree: it was under a seat's pane tree on the pass before and has parent pid 1 now.
	ReasonSeatTree = "seat tree"
	// ReasonMarker: it already had parent pid 1 when first seen, and its environment carries a marvel marker.
	ReasonMarker = "marker"
	// ReasonUnattributed: it has parent pid 1, no seat-tree history and no marker.
	ReasonUnattributed = "unattributed"
)

// Orphan is a process that lost its marvel ancestry, and what it has burned since.
type Orphan struct {
	PID    int    `json:"pid"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Session is the seat the orphan was under, from the daemon's own record.
	// It is set only for the seat tree reason.
	Session string `json:"session,omitempty"`
	// ClaimedSession is the MARVEL_SESSION the process says it has. It is
	// untrusted text from the process, shown to clients, and nothing keys on it.
	// It is set only for the marker reason.
	ClaimedSession string    `json:"claimed_session,omitempty"`
	MarkedAt       time.Time `json:"marked_at"`
	// CPUSecondsSinceMarked is its CPU time now minus its CPU time when marked.
	CPUSecondsSinceMarked float64 `json:"cpu_seconds_since_marked"`
	// Share is its share of one core over the last OrphanWindow, nil until it
	// has been watched that long.
	Share *float64 `json:"share_30m,omitempty"`
	// Args, Cwd and AgeSeconds are set only for the unattributed reason.
	Args       string  `json:"args,omitempty"`
	Cwd        string  `json:"cwd,omitempty"`
	AgeSeconds float64 `json:"age_seconds,omitempty"`
}

// ProcDetail is what a source returns about one process: its exact arguments
// and working directory, or the error that kept it from reading each.
type ProcDetail struct {
	Args    []string
	ArgsErr error
	Cwd     string
	CwdErr  error
}

// OrphanSources tells the sampler what it cannot read from the process table:
// which pids are seats' roots, and a process's marvel marker.
type OrphanSources struct {
	// Roots returns the pane pid of every live seat.
	Roots func() []int
	// Marker reads a process's marvel marker (procargs.ReadMarker on a host).
	Marker func(pid int) (procargs.Marker, error)
	// RootSession names the seat that owns a root pid, from the daemon's own
	// record. The sampler asks it once per root on each pass that finds the root
	// in the table, and keeps the answer with the processes under the root; it
	// never asks again for a seat that has died. Nil, or an empty answer, leaves
	// the session unnamed.
	RootSession func(pid int) string
	// Detail reads the arguments and working directory of a process.
	Detail func(pid int) ProcDetail
}

// SetOrphanSources gives the sampler its orphan sources. Without them no
// process is marked and the vital reads under with no orphans.
func (s *Sampler) SetOrphanSources(src OrphanSources) {
	s.mu.Lock()
	s.orphanSrc = src
	s.mu.Unlock()
}

// cpuPoint is a CPU time reading of one orphan.
type cpuPoint struct {
	at  time.Time
	cpu float64
}

// orphanEntry is one marked process and the CPU readings kept for its window.
type orphanEntry struct {
	orphan    Orphan
	cpuAtMark float64
	cpu       float64
	elapsed   float64
	hist      []cpuPoint
}

// orphanTracker is the sampler's memory across vitals passes. It is touched
// only by SampleProcs, one goroutine; what Snapshot reads is a copy published
// under the sampler's lock.
type orphanTracker struct {
	// tree holds, by pid, the elapsed seconds of every process that was a seat
	// root or below one on the last pass, and key the seat's key as the daemon
	// named it on that pass. The key is recorded then, while the seat is alive:
	// by the pass that marks the orphan the seat is usually gone and may have
	// left the store, so it is never looked up again.
	tree map[int]float64
	key  map[int]string
	// marked holds the orphans.
	marked map[int]*orphanEntry
	// probed holds, by pid, the elapsed seconds when its marker was last read,
	// so a parentless process is read once and not again until its pid is reused.
	probed map[int]float64
}

// reuseSlack is how far a process's elapsed time may fall between passes
// before its pid counts as reused by another process.
const reuseSlack = 1.0

// orphanView is what one pass publishes for the snapshot.
type orphanView struct {
	list   []Orphan
	over   bool
	detail string
}

// update takes one readable table. It marks new orphans, drops the ones that
// exited or whose pid was reused, appends each survivor's CPU reading, and
// returns the view. A pass whose table was not readable never calls it, so the
// tracker keeps its state across the gap.
func (t *orphanTracker) update(now time.Time, procs []Proc, src OrphanSources) orphanView {
	if t.tree == nil {
		t.tree, t.key, t.marked, t.probed = map[int]float64{}, map[int]string{}, map[int]*orphanEntry{}, map[int]float64{}
	}
	byPID := make(map[int]Proc, len(procs))
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}

	for pid, e := range t.marked {
		if p, ok := byPID[pid]; !ok || p.ElapsedSeconds+reuseSlack < e.elapsed {
			delete(t.marked, pid)
		}
	}
	for pid, at := range t.probed {
		if p, ok := byPID[pid]; !ok || p.ElapsedSeconds+reuseSlack < at {
			delete(t.probed, pid)
		}
	}

	for _, p := range procs {
		if p.PPID != 1 || t.marked[p.PID] != nil {
			continue
		}
		if at, ok := t.tree[p.PID]; ok && p.ElapsedSeconds+reuseSlack >= at {
			t.mark(now, p, ReasonSeatTree, t.key[p.PID], "")
			continue
		}
		if src.Marker == nil {
			continue
		}
		if _, done := t.probed[p.PID]; done {
			continue
		}
		t.probed[p.PID] = p.ElapsedSeconds
		if m, err := src.Marker(p.PID); err == nil && m.Marvel() {
			t.mark(now, p, ReasonMarker, "", m.Session)
		}
	}

	t.tree, t.key = map[int]float64{}, map[int]string{}
	if src.Roots != nil {
		var walk func(pid int, key string)
		walk = func(pid int, key string) {
			p, ok := byPID[pid]
			if !ok {
				return
			}
			if _, seen := t.tree[pid]; seen {
				return
			}
			t.tree[pid], t.key[pid] = p.ElapsedSeconds, key
			for _, c := range children[pid] {
				walk(c, key)
			}
		}
		for _, root := range src.Roots() {
			key := ""
			if src.RootSession != nil {
				key = src.RootSession(root)
			}
			walk(root, key)
		}
	}

	view := orphanView{}
	var worst *Orphan
	for pid, e := range t.marked {
		p := byPID[pid]
		e.cpu, e.elapsed = p.CPUSeconds, p.ElapsedSeconds
		e.orphan.Name = p.Name
		e.hist = append(e.hist, cpuPoint{at: now, cpu: p.CPUSeconds})
		share := e.share(now)
		o := e.orphan
		o.CPUSecondsSinceMarked = e.cpu - e.cpuAtMark
		o.Share = share
		view.list = append(view.list, o)
		if share != nil && *share > OrphanShare && (worst == nil || *share > *worst.Share) {
			w := o
			worst = &w
		}
	}
	sort.Slice(view.list, func(i, j int) bool {
		if view.list[i].CPUSecondsSinceMarked != view.list[j].CPUSecondsSinceMarked {
			return view.list[i].CPUSecondsSinceMarked > view.list[j].CPUSecondsSinceMarked
		}
		return view.list[i].PID < view.list[j].PID
	})
	switch {
	case worst != nil:
		view.over = true
		view.detail = fmt.Sprintf("%s (pid %d) %.0f%% of a core over %d minutes, %s of CPU since marked",
			worst.Name, worst.PID, *worst.Share*100, int(OrphanWindow/time.Minute), hours(worst.CPUSecondsSinceMarked))
	case len(view.list) == 0:
		view.detail = "no orphans"
	default:
		view.detail = fmt.Sprintf("%d orphans, none over the line", len(view.list))
	}
	if len(view.list) > OrphansShown {
		view.list = view.list[:OrphansShown]
	}
	return view
}

// mark records a new orphan with the CPU it has now.
func (t *orphanTracker) mark(now time.Time, p Proc, reason, session, claimed string) {
	t.marked[p.PID] = &orphanEntry{
		orphan:    Orphan{PID: p.PID, Name: p.Name, Reason: reason, Session: session, ClaimedSession: claimed, MarkedAt: now},
		cpuAtMark: p.CPUSeconds, cpu: p.CPUSeconds, elapsed: p.ElapsedSeconds,
	}
}

// share is the orphan's share of one core over the span from its latest reading
// at or before now minus the window, to now. It is nil until the orphan has
// been watched that long. Readings older than that one are dropped.
func (e *orphanEntry) share(now time.Time) *float64 {
	cut := now.Add(-OrphanWindow)
	base := -1
	for i, pt := range e.hist {
		if !pt.at.After(cut) {
			base = i
		}
	}
	if base < 0 {
		return nil
	}
	e.hist = e.hist[base:]
	span := now.Sub(e.hist[0].at).Seconds()
	if span <= 0 {
		return nil
	}
	v := (e.cpu - e.hist[0].cpu) / span
	return &v
}

// hours renders CPU seconds as hours with one decimal.
func hours(sec float64) string { return fmt.Sprintf("%.1f h", sec/3600) }
