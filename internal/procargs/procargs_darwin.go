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
