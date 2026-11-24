package session

import "errors"

// ErrRunProfileRefused is wrapped by every refusal of a requested run profile.
var ErrRunProfileRefused = errors.New("run profile refused")

// ComposeRunProfile joins the parts of a sandboxed run's profile into one text:
// the role's own profile, then the requested run profile, then the deny block.
// The request is read into forms and re-serialized, never copied as text, and
// refused unless every form is a deny or one of the three localhost network
// allows the run conditions use (seat-tcc-deny.md, sections 5 and 14; marvel#1596
// rulings Q1 and Q3). A role with no profile gets (version 1)(allow default).
func ComposeRunProfile(role, requested, block string) (string, error) {
	return "", nil // red: not built
}

// RunSandboxParams returns the -D arguments a composed run needs: one KEY=VALUE
// for each (param "KEY") the role's code names, taken from vals. A named
// parameter with no value is an error.
func RunSandboxParams(role string, vals map[string]string) ([]string, error) {
	return nil, nil // red: not built
}

// CheckRunHost refuses a host that cannot run a composed profile: not darwin,
// or no sandbox-exec at the path. A run is never started unwrapped.
func CheckRunHost(goos, sandboxExecPath string) error {
	return nil // red: not built
}
