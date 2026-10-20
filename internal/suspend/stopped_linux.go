//go:build linux

package suspend

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// readStopped reads field 3 of /proc/<pid>/stat. T is a stop by a signal; t, a
// tracing stop, is not one a suspend makes. The command name is field 2 and may
// hold spaces and parentheses, so the state is read after the last ')'.
func readStopped(pid int) (bool, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false, err
	}
	return parseStopped(string(b))
}

func parseStopped(stat string) (bool, error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return false, errors.New("suspend: stat has no command field")
	}
	f := strings.Fields(stat[i+1:])
	if len(f) == 0 {
		return false, errors.New("suspend: stat has no state field")
	}
	return f[0] == "T", nil
}
