package session

import (
	"bufio"
	"strconv"
	"strings"
)

// The host's free-memory reading, recorded in the session.created event of
// each spawn (docs/design/host-health-guard.md section 4.2, "Recommended now").
// It is a diagnostic and nothing reads it back to decide anything: no cap, no
// floor, no gate. A host that cannot give a reading gets the event it always
// got.

// readFreeMemoryPercent is the platform reader, one file per OS. It returns
// the free share of physical memory as a whole percentage, and false when the
// host cannot say.
//
// hostMemoryNote is the clause the spawn event appends to its message, empty
// when there is no reading.
func (m *Manager) hostMemoryNote() string {
	read := m.freeMemory
	if read == nil {
		read = readFreeMemoryPercent
	}
	pct, ok := read()
	if !ok {
		return ""
	}
	return ", host free memory " + strconv.Itoa(pct) + "%"
}

// parseMemoryStatusLevel reads the output of `sysctl -n kern.memorystatus_level`,
// which macOS reports as the free percentage of memory.
func parseMemoryStatusLevel(out string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	return n, true
}

// parseMeminfoFree reads /proc/meminfo and returns MemAvailable as a whole
// percentage of MemTotal. MemAvailable is the kernel's own estimate of what a
// new workload can use, so cache that can be dropped counts as free.
func parseMeminfoFree(s string) (int, bool) {
	var total, avail int64 = -1, -1
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || (name != "MemTotal" && name != "MemAvailable") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return 0, false
		}
		v, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || v < 0 {
			return 0, false
		}
		if name == "MemTotal" {
			total = v
		} else {
			avail = v
		}
	}
	if total <= 0 || avail < 0 || avail > total {
		return 0, false
	}
	return int(avail * 100 / total), true
}
