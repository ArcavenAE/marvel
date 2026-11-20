package procstat

import (
	"math"
	"strconv"
	"strings"
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
// variables so a test can count them. readWide is the same read with three
// more columns (elapsed time, CPU time and command name), one exec on darwin.
var (
	readNarrow = readProcesses
	readWide   = readProcessesWide
)

// ReadTable reads the process table once, with the extra columns, and returns
// it without any rollup. It is the vitals read's own read, used only when no
// seat has a pid and the metrics pass ran none (docs/design/host-vitals.md
// section 2.2).
func ReadTable() ([]Proc, error) {
	_, procs, err := readWide()
	return procs, err
}

// WantTable asks the next Sample to read the wide table and keep it. The
// request is spent by that Sample, or by the next Table call, so a pass that
// reads nothing does not leave it pending for the pass after.
func (s *Sampler) WantTable() { s.wantTable = true }

// Table returns the wide table the last Sample kept, and false when that
// Sample kept none. It hands the table over once and drops any request that
// was never served.
func (s *Sampler) Table() ([]Proc, bool) {
	t, ok := s.table, s.haveTable
	s.table, s.haveTable, s.wantTable = nil, false, false
	return t, ok
}

// parsePSWide reads `ps -A -o pid=,ppid=,pcpu=,rss=,etime=,time=,comm=`. The
// first six columns have no spaces; comm is the rest of the line, because an
// executable path can hold spaces. A row that does not parse is skipped, as in
// parsePS.
func parsePSWide(out string) ([]entry, []Proc) {
	lines := strings.Split(out, "\n")
	entries := make([]entry, 0, len(lines))
	procs := make([]Proc, 0, len(lines))
	for _, line := range lines {
		var f [6]string
		rest := line
		ok := true
		for i := range f {
			rest = strings.TrimLeft(rest, " \t")
			end := strings.IndexAny(rest, " \t")
			if end <= 0 {
				ok = false
				break
			}
			f[i], rest = rest[:end], rest[end:]
		}
		comm := strings.TrimSpace(rest)
		if !ok || comm == "" {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		pcpu, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			continue
		}
		rssKiB, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil {
			continue
		}
		elapsed, eok := parseElapsed(f[4])
		cpu, cok := parseCPUTime(f[5])
		if !eok || !cok {
			continue
		}
		entries = append(entries, entry{pid: pid, ppid: ppid, cpuPercent: pcpu, rssBytes: rssKiB * 1024})
		procs = append(procs, Proc{PID: pid, PPID: ppid, RSSBytes: rssKiB * 1024, Elapsed: elapsed, CPUTime: cpu, Name: commandName(comm)})
	}
	return entries, procs
}

// parseElapsed reads ps's etime: [[dd-]hh:]mm:ss.
func parseElapsed(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	var days int
	if i := strings.Index(s, "-"); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil || d < 0 {
			return 0, false
		}
		days, s = d, s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, false
		}
		v[3-len(parts)+i] = n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(v[0])*time.Hour +
		time.Duration(v[1])*time.Minute + time.Duration(v[2])*time.Second, true
}

// parseCPUTime reads ps's time: [[h:]m:]s with a fractional seconds part. The
// leading field may pass 59 (macOS prints minutes past 999).
func parseCPUTime(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || sec < 0 {
		return 0, false
	}
	var d time.Duration
	mult := []time.Duration{time.Minute, time.Hour}
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return 0, false
		}
		d += time.Duration(n) * mult[len(parts)-2-i]
	}
	return d + time.Duration(math.Round(sec*1000))*time.Millisecond, true
}

// commandName is the last element of an executable path: the command name and
// nothing else.
func commandName(comm string) string {
	comm = strings.TrimSpace(comm)
	if i := strings.LastIndex(comm, "/"); i >= 0 && i < len(comm)-1 {
		return comm[i+1:]
	}
	return comm
}
