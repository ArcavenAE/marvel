package procstat

import (
	"errors"
	"time"
)

// Proc is one process as the host vitals read needs it
// (docs/design/host-vitals.md section 2): the top processes by RSS with their
// pid, parent, elapsed time, CPU time and command name. Name is the command
// name only, the last element of the executable path, never its arguments.
type Proc struct {
	PID      int
	PPID     int
	RSSBytes int64
	Elapsed  time.Duration
	CPUTime  time.Duration
	Name     string
}

// readNarrow and readWide are the two process table reads. They are
// variables so a test can count them.
var (
	readNarrow = readProcesses
	readWide   = readProcessesWide
)

// readProcessesWide reads the process table with the three extra columns.
func readProcessesWide() ([]entry, []Proc, error) {
	return nil, nil, errors.New("procstat: wide read is not built")
}

// ReadTable reads the process table once, with the extra columns, and returns
// it without any rollup. It is the vitals read's own ps, used only when no
// seat has a pid and the metrics pass ran none.
func ReadTable() ([]Proc, error) {
	return nil, errors.New("procstat: ReadTable is not built")
}

// WantTable asks the next Sample to read the wide table and keep it.
func (s *Sampler) WantTable() {}

// Table returns the wide table the last Sample kept, and false when that
// Sample kept none. It hands the table over once.
func (s *Sampler) Table() ([]Proc, bool) { return nil, false }

// parsePSWide reads `ps -A -o pid=,ppid=,pcpu=,rss=,etime=,time=,comm=`.
func parsePSWide(out string) ([]entry, []Proc) { return nil, nil }

// parseElapsed reads ps's etime: [[dd-]hh:]mm:ss.
func parseElapsed(s string) (time.Duration, bool) { return 0, false }

// parseCPUTime reads ps's time: [[h:]m:]s with a fractional seconds part.
func parseCPUTime(s string) (time.Duration, bool) { return 0, false }

// commandName is the last element of an executable path.
func commandName(comm string) string { return comm }
