//go:build linux

package session

import "os"

func readFreeMemoryPercent() (int, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	return parseMeminfoFree(string(b))
}
