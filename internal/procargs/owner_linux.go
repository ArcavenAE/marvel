//go:build linux

package procargs

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// OwnerUID reads the uid that owns a process from /proc/<pid>.
func OwnerUID(pid int) (int, error) {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no owner in the /proc entry")
	}
	return int(st.Uid), nil
}
