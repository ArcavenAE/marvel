// Package requiretool lets a test say which external program it needs, and
// lets CI say which of those programs it promised to provide.
//
// A test that needs nats-server or tmux skips when the program is missing, so a
// laptop without it stays green. On CI that skip hides a broken install: the
// job reads as a pass and the test never ran (marvel#646). CI names the tools
// it installs in MARVEL_REQUIRED_TOOLS, a comma-separated list. A test whose
// tool is on that list fails when the tool is missing, and every other test
// still skips, so a local run, or a job that never promised the tool, does not
// start failing.
package requiretool

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// EnvVar names the list of tools the environment promises to provide.
const EnvVar = "MARVEL_REQUIRED_TOOLS"

// Listed reports whether name is an entry of the comma-separated list. Entries
// are trimmed and matched whole, and an empty name is never listed.
func Listed(list, name string) bool {
	if name == "" {
		return false
	}
	for _, entry := range strings.Split(list, ",") {
		if strings.TrimSpace(entry) == name {
			return true
		}
	}
	return false
}

// captured holds the list as it stood before a test binary scrubbed its
// environment; nil means Preserve has not run.
var captured *string

// Preserve records the list before a test binary scrubs its environment.
// homeguard calls it first, because it removes every MARVEL_* variable.
func Preserve() {
	v := os.Getenv(EnvVar)
	captured = &v
}

// current is the list the tests act on: the preserved one, else the live one.
func current() string {
	if captured != nil {
		return *captured
	}
	return os.Getenv(EnvVar)
}

// IsRequired reports whether the environment promises name.
func IsRequired(name string) bool { return Listed(current(), name) }

// Path returns the path of the named program. When it is missing, the test
// fails if the environment promises it and skips otherwise.
func Path(t testing.TB, name string) string {
	t.Helper()
	return pathFor(t, exec.LookPath, current(), name)
}

// reporter is the part of testing.TB that Path uses.
type reporter interface {
	Helper()
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
}

func pathFor(r reporter, lookPath func(string) (string, error), list, name string) string {
	r.Helper()
	p, err := lookPath(name)
	if err == nil {
		return p
	}
	if Listed(list, name) {
		r.Fatalf("%s is not on PATH, but %s lists it as installed: the install step failed or put it elsewhere (%v)", name, EnvVar, err)
		return ""
	}
	r.Skipf("%s not on PATH", name)
	return ""
}
