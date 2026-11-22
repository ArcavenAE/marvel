//go:build linux

package procargs

import (
	"fmt"
	"os"
)

// Cwd reads a process's working directory from /proc.
func Cwd(pid int) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
}
