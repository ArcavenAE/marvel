//go:build darwin

package procstat

import (
	"fmt"
	"os/exec"
)

// readProcesses shells out to ps once per pass. One exec for the whole
// table beats proc_pidinfo per pid: the syscall route needs cgo, and
// marvel samples every managed session on the same tick anyway.
//
// pcpu here is the kernel's own decaying-average utilization estimate,
// not a delta the sampler computes, so darwin readings are usable on
// the first pass.
func readProcesses() ([]entry, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pcpu=,rss=").Output()
	if err != nil {
		return nil, fmt.Errorf("procstat: ps: %w", err)
	}
	return parsePS(string(out)), nil
}
