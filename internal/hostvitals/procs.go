package hostvitals

import (
	"fmt"
	"runtime"
	"sort"
)

// Proc is one process in the top-by-RSS list. Name is the command name only,
// never its arguments.
type Proc struct {
	PID            int     `json:"pid"`
	PPID           int     `json:"ppid"`
	RSSBytes       uint64  `json:"rss_bytes"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	CPUSeconds     float64 `json:"cpu_seconds"`
	Name           string  `json:"name"`
	// Seat is the key of the seat whose pane tree holds the process, from the
	// daemon's own record; empty for a process outside every seat.
	Seat string `json:"seat,omitempty"`
}

// TopProcesses is how many processes the top list keeps.
const TopProcesses = 10

// The lines for the process vitals (docs/design/host-vitals.md section 4):
// declared constants and diagnostics, never gates.
const (
	FseventsdCrossBytes  = 4 << 30
	ProcessRSSCrossBytes = 20 << 30
)

// hostOS is the platform the fseventsd vital applies to; a test sets it.
var hostOS = runtime.GOOS

// summarize returns the top processes by RSS, biggest first and a tie to the
// lower pid, capped at TopProcesses, and the summed RSS of the processes named
// fseventsd with whether any was seen.
func summarize(procs []Proc) (top []Proc, fseventsd uint64, seen bool) {
	for _, p := range procs {
		if p.Name == "fseventsd" {
			fseventsd += p.RSSBytes
			seen = true
		}
	}
	top = append([]Proc(nil), procs...)
	sort.SliceStable(top, func(i, j int) bool {
		if top[i].RSSBytes != top[j].RSSBytes {
			return top[i].RSSBytes > top[j].RSSBytes
		}
		return top[i].PID < top[j].PID
	})
	if len(top) > TopProcesses {
		top = top[:TopProcesses]
	}
	return top, fseventsd, seen
}

// procDetail names a process by command name and pid, with its RSS. Never its
// arguments.
func procDetail(p Proc) string {
	return fmt.Sprintf("%s (pid %d) %s", p.Name, p.PID, gib(p.RSSBytes))
}

// SeatRSS sums the RSS of every process in the pane tree of each root, and
// counts the roots found in the table. A process under two roots counts once.
func SeatRSS(procs []Proc, roots []int) (seats int, rss uint64) {
	byPID, kids := indexProcs(procs)
	seen := map[int]bool{}
	done := map[int]bool{}
	for _, root := range roots {
		if done[root] {
			continue
		}
		done[root] = true
		if _, ok := byPID[root]; !ok {
			continue
		}
		seats++
		walkTree(root, kids, func(pid int) {
			if !seen[pid] {
				seen[pid] = true
				rss += byPID[pid].RSSBytes
			}
		})
	}
	return seats, rss
}

// indexProcs maps pids to processes and parents to their children.
func indexProcs(procs []Proc) (map[int]Proc, map[int][]int) {
	byPID := make(map[int]Proc, len(procs))
	kids := make(map[int][]int, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
		kids[p.PPID] = append(kids[p.PPID], p.PID)
	}
	return byPID, kids
}

// walkTree calls fn for pid and every process below it, each once.
func walkTree(pid int, kids map[int][]int, fn func(int)) {
	seen := map[int]bool{}
	var walk func(int)
	walk = func(n int) {
		if seen[n] {
			return
		}
		seen[n] = true
		fn(n)
		for _, c := range kids[n] {
			walk(c)
		}
	}
	walk(pid)
}

// nameSeats sets Seat on each process in the pane tree of a root the daemon
// can name. The first root to reach a process names it.
func nameSeats(procs []Proc, roots []int, session func(pid int) string) {
	if session == nil {
		return
	}
	_, kids := indexProcs(procs)
	idx := make(map[int]int, len(procs))
	for i, p := range procs {
		idx[p.PID] = i
	}
	for _, root := range roots {
		key := session(root)
		if key == "" {
			continue
		}
		walkTree(root, kids, func(pid int) {
			if i, ok := idx[pid]; ok && procs[i].Seat == "" {
				procs[i].Seat = key
			}
		})
	}
}
