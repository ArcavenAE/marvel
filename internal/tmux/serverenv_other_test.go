//go:build !linux

package tmux

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// serverEnvNames returns the NAMES of the variables a process was started with,
// read with `ps -E`. Where that read is refused (a sandbox, a setuid ps), the
// real-server proof did not run, and the skip says so.
func serverEnvNames(t *testing.T, pid int) map[string]bool {
	t.Helper()
	out, err := exec.Command("ps", "-E", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Skipf("the real-server proof did not run: ps -E cannot read the server here: %v", err)
	}
	names := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		if k, _, ok := strings.Cut(f, "="); ok && k != "" {
			names[k] = true
		}
	}
	return names
}
