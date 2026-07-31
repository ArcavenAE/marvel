//go:build linux

package procstat

import (
	"fmt"
	"os"
	"strconv"
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
