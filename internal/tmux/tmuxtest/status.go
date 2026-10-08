// Package tmuxtest holds helpers that tests in several packages share about
// the tmux they run against. It is imported by tests only.
package tmuxtest

import (
	"regexp"
	"strconv"
)

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// StatusExpected reports whether `tmux -V` output names a tmux that keeps a
// dead pane's exit status (3.5 and later). An unparseable version is treated
// as expected, so a surprise fails loudly instead of degrading quietly.
func StatusExpected(version string) bool {
	m := versionRE.FindStringSubmatch(version)
	if m == nil {
		return true
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 3 || (major == 3 && minor >= 5)
}
