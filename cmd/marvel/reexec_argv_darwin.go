//go:build darwin

package main

import "golang.org/x/sys/unix"

// readProcessArgs reads a process's exact argument list from the kernel.
func readProcessArgs(pid int) ([]string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	return parseProcargs2(b)
}
