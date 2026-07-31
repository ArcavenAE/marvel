package procstat

import (
	"fmt"
	"strconv"
	"strings"
)

// procStat is the subset of /proc/<pid>/stat marvel reads.
type procStat struct {
	ppid     int
	cpuTicks uint64
	rssPages int64
}

// parseProcStat reads /proc/<pid>/stat. Field 2 is the executable name
// in parentheses and may itself contain spaces and parentheses, so the
// scan starts after the LAST ')' and counts from field 3 (state).
// Offsets used, in the proc(5) numbering: 4 ppid, 14 utime, 15 stime,
// 24 rss (in pages). Field 24 is why this reads no statm: rss there is
// the same number.
func parseProcStat(data string) (procStat, error) {
	end := strings.LastIndex(data, ")")
	if end < 0 {
		return procStat{}, fmt.Errorf("procstat: no comm field")
	}
	f := strings.Fields(data[end+1:])
	// Highest index touched is rss at field 24, which sits at index 21
	// once state (field 3) is index 0.
	const rssIndex = 21
	if len(f) <= rssIndex {
		return procStat{}, fmt.Errorf("procstat: short stat line (%d fields after comm)", len(f))
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return procStat{}, fmt.Errorf("procstat: ppid: %w", err)
	}
	utime, err := strconv.ParseUint(f[11], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("procstat: utime: %w", err)
	}
	stime, err := strconv.ParseUint(f[12], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("procstat: stime: %w", err)
	}
	rss, err := strconv.ParseInt(f[rssIndex], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("procstat: rss: %w", err)
	}
	return procStat{ppid: ppid, cpuTicks: utime + stime, rssPages: rss}, nil
}

// parseProcIO reads /proc/<pid>/io and returns the block-layer byte
// counters. read_bytes and write_bytes are actual storage traffic;
// rchar and wchar (deliberately ignored) also count pipe and tty
// traffic, which for an agent process is dominated by its own terminal
// and says nothing about disk pressure.
func parseProcIO(data string) (readBytes, writeBytes int64, err error) {
	var haveRead, haveWrite bool
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, perr := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if perr != nil {
			continue
		}
		switch key {
		case "read_bytes":
			readBytes, haveRead = n, true
		case "write_bytes":
			writeBytes, haveWrite = n, true
		}
	}
	if !haveRead || !haveWrite {
		return 0, 0, fmt.Errorf("procstat: io counters missing")
	}
	return readBytes, writeBytes, nil
}
