package gittest

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestNoCommitPlumbing: a test may not write an unsigned commit (operator
// ruling, relayed by director 2026-10-27), and these fixtures test where a
// repository resolves, never its history, so they make no commit at all.
// The patterns are bracketed so this file cannot match itself.
func TestNoCommitPlumbing(t *testing.T) {
	t.Parallel()
	checks := []struct {
		path string
		re   *regexp.Regexp
	}{
		{"gittest.go", regexp.MustCompile(`"[c]ommit"|"[c]ommit-tree"|"[m]ktag"|"[f]ast-import"|"[u]pdate-ref"|-t [c]ommit`)},
		{
			"../../.github/workflows/probe-ubuntu-26.yml",
			regexp.MustCompile(`\bgit\b.*\s([c]ommit|[c]ommit-tree|[m]ktag|[f]ast-import|[u]pdate-ref)(\s|$)|-t [c]ommit`),
		},
	}
	for _, c := range checks {
		data, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if c.re.MatchString(line) {
				t.Errorf("%s:%d makes a commit: %s", c.path, i+1, strings.TrimSpace(line))
			}
		}
	}
}
