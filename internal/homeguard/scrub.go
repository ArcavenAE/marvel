package homeguard

import (
	"os"
	"slices"
	"strings"
)

// keepEnv is the list of variables a guarded test binary keeps from the
// environment it was started with; every other variable is removed before any
// test runs, so a child a test starts inherits only these and the scratch HOME.
// It is a list of what is kept, not of what is removed, so a credential variable
// that nobody has named yet is gone too. HOME is not on it: Run sets it to the
// scratch home separately.
var keepEnv = []string{"PATH", "LANG", "TMPDIR"}

// scrubbedEnv returns the entries of environ whose names are in keep, in their
// original order. An entry with no name or no equals sign is dropped.
func scrubbedEnv(environ, keep []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if ok && name != "" && slices.Contains(keep, name) {
			out = append(out, kv)
		}
	}
	return out
}

// scrubProcessEnv replaces the process environment with scrubbedEnv(environ,
// keepEnv).
func scrubProcessEnv() error {
	kept := scrubbedEnv(os.Environ(), keepEnv)
	os.Clearenv()
	for _, kv := range kept {
		name, value, _ := strings.Cut(kv, "=")
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}
