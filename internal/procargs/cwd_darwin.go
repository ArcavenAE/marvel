//go:build darwin

package procargs

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// lsofPath is the system lsof, named in full so no PATH entry can replace it.
const lsofPath = "/usr/sbin/lsof"

// lsofTimeout bounds one lsof call.
const lsofTimeout = 3 * time.Second

// Cwd reads a process's working directory with lsof. The build has no cgo, so
// proc_pidinfo is out of reach and no sysctl returns the directory. The caller
// asks once per process, when the process first crosses a CPU line, never on a
// timer. The argument vector is fixed, the environment is only LC_ALL=C and stdin is
// closed. The call fails for a process of another user, which lsof cannot open.
func Cwd(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, lsofPath, "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn")
	cmd.Env = []string{"LC_ALL=C"}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return ParseLsofCwd(out)
}
