//go:build linux

package childproof

import (
	"os"
	"strings"
	"testing"
)

// A process of another user shows its start time but not its executable
// (/proc/<pid>/exe is EACCES). The failed read still returns the start time,
// which is what lets a caller tell a reused pid from the recorded one.
func TestReadIdentityKeepsTheStartWhenTheExecutableIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read /proc/1/exe")
	}
	// With /proc mounted hidepid, the stat of another user's process is refused
	// too, so there is no start time to keep and nothing here to check.
	if _, err := os.ReadFile("/proc/1/stat"); err != nil {
		t.Skipf("/proc/1/stat is not readable here (hidepid on /proc?): %v", err)
	}
	id, err := ReadIdentityForTest(1)
	if err == nil {
		t.Skip("pid 1 is readable here, so there is no failed read to check")
	}
	if !strings.HasPrefix(id.Start, "linux:") {
		t.Errorf("start after a failed read = %q, want a linux start time", id.Start)
	}
	if id.Exe != "" || len(id.Argv) != 0 {
		t.Errorf("failed read returned exe %q argv %v, want only the start", id.Exe, id.Argv)
	}
}
