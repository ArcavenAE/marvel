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

import "testing"

// EnvVar names the list of tools the environment promises to provide.
const EnvVar = "MARVEL_REQUIRED_TOOLS"

// Listed reports whether name is an entry of the comma-separated list.
func Listed(list, name string) bool {
	return false
}

// Preserve records the list before a test binary scrubs its environment.
// homeguard calls it first, because it removes every MARVEL_* variable.
func Preserve() {}

// reporter is the part of testing.TB that Path uses.
type reporter interface {
	Helper()
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
}

var captured *string

func current() string { _ = captured; return "" }

func pathFor(r reporter, lookPath func(string) (string, error), list, name string) string {
	return ""
}

// IsRequired reports whether the environment promises name.
func IsRequired(name string) bool { return false }

// Path returns the path of the named program. When it is missing, the test
// fails if the environment promises it and skips otherwise.
func Path(t testing.TB, name string) string { return "" }
