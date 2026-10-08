// Package pidfile reads the pid a daemon writes to its pidfile. One parser
// serves every reader, so none can disagree with another about what a pidfile
// names.
package pidfile

import (
	"fmt"
	"os"
	"strings"
)

// Parse is a red stub with the behavior the readers had before they shared it:
// it takes the first number and ignores what follows, and holds 64 bits.
func Parse(text string) (int, bool) {
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(text), "%d", &pid); err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// Read returns the pid the file at path holds, and zero when the path is empty,
// the file is missing or unreadable, or it holds no valid pid.
func Read(path string) int {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := Parse(string(data))
	return pid
}
