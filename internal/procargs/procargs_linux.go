//go:build linux

package procargs

import (
	"fmt"
	"os"
)

// Read reads a process's exact argument list from /proc.
func Read(pid int) ([]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	return ParseCmdline(b)
}
