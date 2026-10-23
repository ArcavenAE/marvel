//go:build darwin

package childproof

import (
	"os"
	"strings"
	"testing"
)

// A process of another user shows its start time but not its executable or
// arguments (EPERM). The failed read still returns the start time, which is
// what lets a caller tell a reused pid from the recorded one.
func TestReadIdentityKeepsTheStartWhenTheArgumentsAreRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read pid 1")
	}
	id, err := ReadIdentityForTest(1)
	if err == nil {
		t.Skip("pid 1 is readable here, so there is no failed read to check")
	}
	if !strings.HasPrefix(id.Start, "darwin:") {
		t.Errorf("start after a failed read = %q, want a darwin start time", id.Start)
	}
	if id.Exe != "" || len(id.Argv) != 0 {
		t.Errorf("failed read returned exe %q argv %v, want only the start", id.Exe, id.Argv)
	}
}
