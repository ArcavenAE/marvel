//go:build darwin

package session

import (
	"context"
	"os/exec"
	"time"
)

// readFreeMemoryPercent asks the kernel for its free-memory level. The sysctl
// needs no privilege and returns at once; the bound is for a host in trouble.
func readFreeMemoryPercent() (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", "kern.memorystatus_level").Output()
	if err != nil {
		return 0, false
	}
	return parseMemoryStatusLevel(string(out))
}
