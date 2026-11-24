package hostvitals

import (
	"fmt"
	"sort"
	"strings"
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
	// Parents are the command names of the orphan's ancestors, nearest first,
	// as last seen before it lost its parent; empty when it was never seen
	// with one.
	Parents []string `json:"parents,omitempty"`
	// StartedAt is when the process started: the pass time less its elapsed time.
	StartedAt time.Time `json:"started_at"`
	// RSSBytes is the orphan's resident memory at the latest pass.
	RSSBytes uint64 `json:"rss_bytes"`
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
	// Exclude lists bundle or executable paths whose processes are never
	// counted as orphans. Empty excludes nothing.
	Exclude []string
}

// SetOrphanSources gives the sampler its orphan sources. Without them no
// process is marked and the vital reads under with no orphans.
func (s *Sampler) SetOrphanSources(src OrphanSources) {
	s.mu.Lock()
	s.orphanSrc = src
	s.mu.Unlock()
}

// SetOrphanExclude sets the bundle or executable paths whose processes are
// never counted as orphans. Empty, the default, excludes nothing.
func (s *Sampler) SetOrphanExclude(paths []string) {
	s.mu.Lock()
	s.orphanSrc.Exclude = append([]string(nil), paths...)
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
	// detailed is set once the arguments and directory of an unattributed
	// process have been read for good; tries counts the reads made.
	detailed bool
	tries    int
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
	// excluded holds, by pid, the elapsed seconds when a process was read as
	// under an excluded path, so it is read once until its pid is reused.
	excluded map[int]float64
	// anc holds, by pid, the command names of its ancestors on the last pass,
	// nearest first, so an orphan can show the chain it lost.
	anc map[int][]string
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
		t.excluded = map[int]float64{}
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
	for pid, at := range t.excluded {
		if p, ok := byPID[pid]; !ok || p.ElapsedSeconds+reuseSlack < at {
			delete(t.excluded, pid)
		}
	}

	for _, p := range procs {
		if p.PPID != 1 || t.marked[p.PID] != nil {
			continue
		}
		if t.isExcluded(p, src) {
			continue
		}
		if at, ok := t.tree[p.PID]; ok && p.ElapsedSeconds+reuseSlack >= at {
			t.mark(now, p, ReasonSeatTree, t.key[p.PID], "")
			continue
		}
		if src.Marker != nil {
			if _, done := t.probed[p.PID]; !done {
				t.probed[p.PID] = p.ElapsedSeconds
				if m, err := src.Marker(p.PID); err == nil && m.Marvel() {
					t.mark(now, p, ReasonMarker, "", m.Session)
					continue
				}
			}
		}
		// No seat tree and no marker: tracked, and listed once it is over the line.
		t.mark(now, p, ReasonUnattributed, "", "")
	}

	t.anc = ancestorNames(byPID)
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
	unattributed := 0
	for pid, e := range t.marked {
		p := byPID[pid]
		e.cpu, e.elapsed = p.CPUSeconds, p.ElapsedSeconds
		e.orphan.RSSBytes = p.RSSBytes
		e.orphan.Name = p.Name
		e.hist = append(e.hist, cpuPoint{at: now, cpu: p.CPUSeconds})
		share := e.share(now)
		over := share != nil && *share > OrphanShare
		if e.orphan.Reason == ReasonUnattributed {
			// A parentless process nobody vouches for is a report, so it is listed
			// only over the line, and its arguments are read once it is a shown row.
			if !over {
				continue
			}
			unattributed++
			e.orphan.AgeSeconds = e.elapsed
		}
		o := e.orphan
		o.CPUSecondsSinceMarked = e.cpu - e.cpuAtMark
		o.Share = share
		view.list = append(view.list, o)
		if over && (worst == nil || *share > *worst.Share) {
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
		if unattributed > 0 {
			view.detail += fmt.Sprintf("; %d unattributed over the line", unattributed)
		}
	case len(view.list) == 0:
		view.detail = "no orphans"
	default:
		view.detail = fmt.Sprintf("%d orphans, none over the line", len(view.list))
	}
	if len(view.list) > OrphansShown {
		view.list = view.list[:OrphansShown]
	}
	t.readDetails(view.list, src)
	return view
}

// readDetails reads the arguments and directory of the unattributed rows that
// will be shown, once each, at most MaxDetailReadsPerPass on one pass, in list
// order. A shown row not yet read says "pending" and is read on a later pass.
func (t *orphanTracker) readDetails(list []Orphan, src OrphanSources) {
	if src.Detail == nil {
		return
	}
	reads := 0
	for i := range list {
		o := &list[i]
		if o.Reason != ReasonUnattributed {
			continue
		}
		e := t.marked[o.PID]
		if !e.detailed && reads < MaxDetailReadsPerPass {
			reads++
			e.tries++
			e.orphan.Args, e.orphan.Cwd = detailText(src.Detail(o.PID))
			// A read that failed for a passing reason (an lsof timeout, a process
			// that just exited) says unknown and is tried again on a later pass, up
			// to MaxDetailTries. A read that worked, or one refused for permission,
			// is final.
			unknown := e.orphan.Args == textUnknown || e.orphan.Cwd == textUnknown
			e.detailed = !unknown || e.tries >= MaxDetailTries
		}
		if e.tries > 0 {
			o.Args, o.Cwd = e.orphan.Args, e.orphan.Cwd
		} else {
			o.Args, o.Cwd = "pending", "pending"
		}
	}
}

// mark records a new orphan with the CPU it has now.
func (t *orphanTracker) mark(now time.Time, p Proc, reason, session, claimed string) {
	t.marked[p.PID] = &orphanEntry{
		orphan: Orphan{
			PID: p.PID, Name: p.Name, Reason: reason, Session: session, ClaimedSession: claimed, MarkedAt: now,
			Parents: t.anc[p.PID], StartedAt: now.Add(-time.Duration(p.ElapsedSeconds * float64(time.Second))),
		},
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

// ancestorNames maps each process with a parent in the table to the command
// names of its ancestors, nearest first. The walk stops at pid 1, at a parent
// missing from the table and at a cycle.
func ancestorNames(byPID map[int]Proc) map[int][]string {
	out := make(map[int][]string, len(byPID))
	for pid, p := range byPID {
		if p.PPID == 1 {
			continue
		}
		var names []string
		seen := map[int]bool{pid: true}
		for at := p.PPID; at != 1 && !seen[at]; {
			a, ok := byPID[at]
			if !ok {
				break
			}
			seen[at] = true
			names = append(names, a.Name)
			at = a.PPID
		}
		if len(names) > 0 {
			out[pid] = names
		}
	}
	return out
}

// isExcluded reports whether the process runs from a path on the exclude list.
// It reads the arguments once per process, and only when a list is set.
func (t *orphanTracker) isExcluded(p Proc, src OrphanSources) bool {
	if len(src.Exclude) == 0 || src.Detail == nil {
		return false
	}
	if _, done := t.excluded[p.PID]; done {
		return true
	}
	d := src.Detail(p.PID)
	if d.ArgsErr != nil || len(d.Args) == 0 {
		return false
	}
	exe := d.Args[0]
	for _, e := range src.Exclude {
		e = strings.TrimRight(e, "/")
		if e != "" && (exe == e || strings.HasPrefix(exe, e+"/")) {
			t.excluded[p.PID] = p.ElapsedSeconds
			return true
		}
	}
	return false
}
