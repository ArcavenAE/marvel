//go:build darwin

package procargs

import "golang.org/x/sys/unix"

// Read reads a process's exact argument list from the kernel.
func Read(pid int) ([]string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	return ParseProcargs2(b)
}

// ExecPath reads the path a process was exec'd from, as the kernel recorded it.
func ExecPath(pid int) (string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	return ParseProcargs2ExecPath(b)
}
