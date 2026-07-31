package procstat

import (
	"strconv"
	"strings"
)

// parsePS reads the output of `ps -A -o pid=,ppid=,pcpu=,rss=`. The
// trailing `=` on each column suppresses the header, so every line is
// data. Columns are whitespace-padded to a width that depends on the
// widest value on the machine, so fields are split on runs of space
// rather than at fixed offsets.
//
// Lines that don't parse are skipped: a process can exit between ps
// writing its header and its row, and one malformed row should not cost
// the whole pass.
func parsePS(out string) []entry {
	lines := strings.Split(out, "\n")
	entries := make([]entry, 0, len(lines))
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 4 {
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
		entries = append(entries, entry{
			pid:        pid,
			ppid:       ppid,
			cpuPercent: pcpu,
			rssBytes:   rssKiB * 1024,
		})
	}
	return entries
}
