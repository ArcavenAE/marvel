//go:build linux

package procstat

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// procRoot is a variable so tests can point the walk at a fixture tree.
var procRoot = "/proc"

// readProcesses walks /proc. Processes that vanish mid-walk are skipped
// rather than failing the pass, which is the normal case for a scan of a
// live process table.
//
// /proc/<pid>/io is owner-readable only; a session running under
// another uid contributes no IO counters and the rollup reports IO as
// unavailable rather than as zero.
func readProcesses() ([]entry, error) {
	dir, err := os.Open(procRoot)
	if err != nil {
		return nil, fmt.Errorf("procstat: open %s: %w", procRoot, err)
	}
	defer func() { _ = dir.Close() }()

	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("procstat: read %s: %w", procRoot, err)
	}

	pageSize := int64(os.Getpagesize())
	entries := make([]entry, 0, len(names))
	for _, name := range names {
		pid, cerr := strconv.Atoi(name)
		if cerr != nil {
			continue
		}
		raw, rerr := os.ReadFile(procRoot + "/" + name + "/stat")
		if rerr != nil {
			continue
		}
		st, perr := parseProcStat(string(raw))
		if perr != nil {
			continue
		}
		e := entry{
			pid:        pid,
			ppid:       st.ppid,
			cpuTicks:   st.cpuTicks,
			cumulative: true,
			rssBytes:   st.rssPages * pageSize,
		}
		if ioRaw, ierr := os.ReadFile(procRoot + "/" + name + "/io"); ierr == nil {
			if r, w, perr := parseProcIO(string(ioRaw)); perr == nil {
				e.ioReadBytes, e.ioWriteBytes, e.ioOK = r, w, true
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// readProcessesWide is readProcesses plus the command name, elapsed time and
// CPU time per process, from the same /proc files (docs/design/host-vitals.md
// section 2). Elapsed is the uptime minus the process's start time.
func readProcessesWide() ([]entry, []Proc, error) {
	entries, err := readProcesses()
	if err != nil {
		return nil, nil, err
	}
	up, uerr := os.ReadFile(procRoot + "/uptime")
	uptime, perr := parseUptime(string(up))
	if uerr != nil || perr != nil {
		return nil, nil, fmt.Errorf("procstat: read %s/uptime", procRoot)
	}
	procs := make([]Proc, 0, len(entries))
	for _, e := range entries {
		raw, rerr := os.ReadFile(procRoot + "/" + strconv.Itoa(e.pid) + "/stat")
		if rerr != nil {
			continue
		}
		name, start, serr := parseProcStatWide(string(raw))
		if serr != nil {
			continue
		}
		elapsed := uptime - float64(start)/clockTicksPerSecond
		if elapsed < 0 {
			elapsed = 0
		}
		procs = append(procs, Proc{
			PID: e.pid, PPID: e.ppid, RSSBytes: e.rssBytes,
			Elapsed: time.Duration(elapsed * float64(time.Second)),
			CPUTime: time.Duration(float64(e.cpuTicks) / clockTicksPerSecond * float64(time.Second)),
			Name:    name,
		})
	}
	return entries, procs, nil
}
