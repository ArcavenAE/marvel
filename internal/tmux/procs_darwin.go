//go:build darwin

package tmux

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/arcavenae/marvel/internal/procargs"
)

// defaultProcList lists the tmux processes as `ps -axo pid,args` would, read
// from the kernel: the process table through the kern.proc.all sysctl and each
// tmux process's argument list through kern.procargs2, which is how
// internal/procargs reads one. It needs no `ps`, which a sandbox can refuse
// (ps is setuid, and sandbox-exec cannot run it even under a profile that
// allows everything).
//
// Only processes whose executable name is tmux are listed, since the one
// reader of the list, carriesSocket, ignores every other program. A process
// that exits between the table read and its argument read is skipped. If the
// table itself cannot be read the error is returned, and the caller reads the
// answer as "not absent", the safe direction.
func defaultProcList(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("kern.proc.all: %w", err)
	}
	var b bytes.Buffer
	for _, p := range procs {
		if commName(p.Proc.P_comm[:]) != "tmux" {
			continue
		}
		pid := int(p.Proc.P_pid)
		args, err := procargs.Read(pid)
		if err != nil || len(args) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%d %s\n", pid, strings.Join(args, " "))
	}
	return b.Bytes(), nil
}

// commName is the NUL-terminated executable name in a kinfo_proc.
func commName(c []byte) string {
	if i := bytes.IndexByte(c, 0); i >= 0 {
		c = c[:i]
	}
	return string(c)
}
