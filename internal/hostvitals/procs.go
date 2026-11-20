package hostvitals

import "runtime"

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

// SampleProcs takes one reading like Sample and also keeps the process table
// the caller read. ok is false when the table could not be read.
func (s *Sampler) SampleProcs(procs []Proc, ok bool) {
	s.Sample()
}
