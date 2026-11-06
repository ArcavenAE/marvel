package hostvitals

import (
	"bufio"
	"strconv"
	"strings"
)

// parseMeminfoSwap reads /proc/meminfo and returns swap used and total in
// bytes. Used is SwapTotal less SwapFree. A file with either line missing is
// not a reading.
func parseMeminfoSwap(s string) (used, total uint64, ok bool) {
	var tot, free int64 = -1, -1
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		name, rest, found := strings.Cut(sc.Text(), ":")
		if !found || (name != "SwapTotal" && name != "SwapFree") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return 0, 0, false
		}
		v, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || v < 0 {
			return 0, 0, false
		}
		if name == "SwapTotal" {
			tot = v
		} else {
			free = v
		}
	}
	if tot < 0 || free < 0 || free > tot {
		return 0, 0, false
	}
	return uint64(tot-free) << 10, uint64(tot) << 10, true
}

// parsePSISomeAvg10 reads /proc/pressure/memory and returns the avg10 figure
// of its "some" line.
func parsePSISomeAvg10(s string) (float64, bool) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "some" {
			continue
		}
		for _, kv := range f[1:] {
			k, v, found := strings.Cut(kv, "=")
			if !found || k != "avg10" {
				continue
			}
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n < 0 {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}
