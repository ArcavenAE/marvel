// Package pidfile reads the pid a daemon writes to its pidfile. One parser
// serves every reader, so none can disagree with another about what a pidfile
// names.
package pidfile

import (
	"os"
	"strconv"
	"strings"
)

// Parse returns the pid in the text of a pidfile, and false when the text is not
// a pid. A pid is a plain decimal number, surrounding whitespace aside, that is
// positive and fits 32 bits: kill(2) and kern.procargs2 take 32 bits, so a
// wider number would wrap to another process (this one, -1 for every process,
// -5 for a process group), and text after the number means the file is not the
// one a daemon wrote.
func Parse(text string) (int, bool) {
	digits := strings.TrimSpace(text)
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	pid, err := strconv.ParseInt(digits, 10, 32)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return int(pid), true
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
