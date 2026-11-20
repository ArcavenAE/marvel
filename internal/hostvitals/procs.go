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
