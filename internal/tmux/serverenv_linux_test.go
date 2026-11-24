//go:build linux

package tmux

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// serverEnvNames returns the NAMES of the variables a process was started with,
// read from /proc/<pid>/environ. The caller's own tmux server is always readable
// by the caller, so a read error here is a failure, never a skip: a skip would let
// the real-server proof pass in CI without running.
func serverEnvNames(t *testing.T, pid int) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		t.Fatalf("cannot read the server environment: %v", err)
	}
	names := map[string]bool{}
	for _, kv := range strings.Split(string(b), "\x00") {
		if k, _, ok := strings.Cut(kv, "="); ok && k != "" {
			names[k] = true
		}
	}
	return names
}
