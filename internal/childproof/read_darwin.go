//go:build darwin

package childproof

import (
	"fmt"

	"golang.org/x/sys/unix"

	"github.com/arcavenae/marvel/internal/procargs"
)

// readIdentity reads what the kernel says about pid now. Any error, including
// "no such process", is returned; the caller treats an error as unproven. When
// the start time was read before the error, the returned Identity holds it
// alone, which is all a caller may trust from a failed read.
// kern.proc.pid gives p_starttime and the process group; kern.procargs2 gives
// the executable path and the arguments. No cgo.
func readIdentity(pid int) (Identity, error) {
	ki, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Identity{}, err
	}
	t := ki.Proc.P_starttime
	start := fmt.Sprintf("darwin:%d.%06d", t.Sec, t.Usec)
	exe, err := procargs.ExecPath(pid)
	if err != nil {
		return Identity{Start: start}, err
	}
	argv, err := procargs.Read(pid)
	if err != nil {
		return Identity{Start: start}, err
	}
	return Identity{
		Start: start,
		Exe:   exe,
		Argv:  argv,
		Pgid:  int(ki.Eproc.Pgid),
		Ppid:  int(ki.Eproc.Ppid),
	}, nil
}
