package tmux

import (
	"os"
	"testing"
)

// writeExecutable writes a fake tmux (or any script the test will exec) and
// fails the test if it cannot. Every test in this package that writes a script
// and then execs it goes through here (marvel#1554).
func writeExecutable(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
}
