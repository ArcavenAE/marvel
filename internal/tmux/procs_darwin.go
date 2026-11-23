//go:build darwin

package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arcavenae/marvel/internal/procargs"
)

// The three kernel reads, behind variables so a test can make each one fail.
var (
	kinfoProcs = unix.SysctlKinfoProcSlice
	procArgs   = procargs.Read
)

// defaultProcList lists the tmux processes as `ps -axo pid,args` would, read
// from the kernel: the process table through the kern.proc.all sysctl and each
// tmux process's argument list through kern.procargs2, which is how
// internal/procargs reads one. It needs no `ps`, which a sandbox can refuse
// (ps is setuid, and sandbox-exec cannot run it even under a profile that
// allows everything).
//
// Only processes of this user whose executable name (p_comm) is tmux are listed, since the
// one reader of the list, carriesSocket, ignores every other program. That is
// narrower than the ps it replaces, which matched on argv[0] alone: a tmux
// binary file named something else but run with argv[0] set to tmux is not
// listed. marvel starts the binary it resolves as tmux, and p_comm is cut at 16
// bytes, which does not touch a 4-byte name. A process that exits between the
// table read and its argument read is skipped. Any other failure is returned:
// a table that cannot be read, or the arguments of a tmux process that is still
// in the table. The caller reads an error as "not absent", the safe direction.
func defaultProcList(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	procs, err := kinfoProcs("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("kern.proc.all: %w", err)
	}
	uid := uint32(os.Geteuid())
	var b bytes.Buffer
	for _, p := range procs {
		if commName(p.Proc.P_comm[:]) != "tmux" {
			continue
		}
		// Another account's tmux is not this driver's server, and the kernel
		// refuses its argument list.
		if p.Eproc.Ucred.Uid != uid {
			continue
		}
		pid := int(p.Proc.P_pid)
		args, err := readArgsSettled(ctx, pid)
		if err != nil {
			if processGone(pid) {
				continue
			}
			// Still in the table, so a live tmux that cannot be read is
			// unknown, not gone: say so, and the caller reads "not absent".
			return nil, fmt.Errorf("arguments of tmux pid %d: %w", pid, err)
		}
		if len(args) == 0 {
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

// readArgsSettled reads a process's arguments, trying a few times while the
// process is still coming or going: a process that has just forked, or has
// exited and not yet been reaped, has no readable argument list for a few
// milliseconds, and the retry keeps that from reading as an outage.
func readArgsSettled(ctx context.Context, pid int) ([]string, error) {
	var (
		args []string
		err  error
	)
	for i := 0; i < 5; i++ {
		if args, err = procArgs(pid); err == nil {
			return args, nil
		}
		if processGone(pid) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil, err
}

// processGone says the kernel no longer has pid, or holds it only as an exited
// process waiting to be reaped. The sysctl answers a pid it does not have with
// ESRCH, or with EIO when the answer is empty (measured on darwin with the
// x/sys in go.mod). Any other answer, including a refusal, counts as still
// there, so an unreadable process is never taken for a gone one.
func processGone(pid int) bool {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EIO)
	}
	return k.Proc.P_stat == sZOMB
}

// sZOMB is the kernel's process state for an exited process not yet reaped.
const sZOMB = 5
