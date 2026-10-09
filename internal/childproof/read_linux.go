//go:build linux

package childproof

import (
	"fmt"
	"os"
	"strings"

	"github.com/arcavenae/marvel/internal/procargs"
)

// readIdentity reads what the kernel says about pid now. Any error, including
// "no such process", is returned; the caller treats an error as unproven.
// /proc/<pid>/stat gives the start time (field 22), the process group
// (field 5) and the parent (field 4); boot_id names the boot, so ticks from two boots never compare
// equal; /proc/<pid>/exe is the executable.
func readIdentity(pid int) (Identity, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return Identity{}, err
	}
	ppid, pgrp, ticks, err := parseStat(stat)
	if err != nil {
		return Identity{}, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return Identity{}, err
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return Identity{}, err
	}
	argv, err := procargs.Read(pid)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Start: fmt.Sprintf("linux:%s:%d", strings.TrimSpace(string(boot)), ticks),
		Exe:   strings.TrimSuffix(exe, " (deleted)"),
		Argv:  argv,
		Pgid:  pgrp,
		Ppid:  ppid,
	}, nil
}
