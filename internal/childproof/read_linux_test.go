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
