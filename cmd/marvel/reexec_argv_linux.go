//go:build linux

package main

import (
	"fmt"
	"os"
)

// readProcessArgs reads a process's exact argument list from /proc.
func readProcessArgs(pid int) ([]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	return parseCmdline(b)
}
