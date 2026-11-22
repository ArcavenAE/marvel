//go:build darwin

package procargs

import "golang.org/x/sys/unix"

// OwnerUID reads the effective uid of a process from the kernel's process table.
func OwnerUID(pid int) (int, error) {
	ki, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(ki.Eproc.Ucred.Uid), nil
}
