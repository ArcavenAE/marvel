package tmux

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// BearerPresence is what a look at one environment variable found.
type BearerPresence int

const (
	// BearerUnknown is the zero value: the look failed or tmux said something
	// this reader does not read. It never means absent.
	BearerUnknown BearerPresence = iota
	// BearerAbsent means tmux said the name is not set to a non-empty value.
	BearerAbsent
	// BearerPresent means the name is set to a non-empty value.
	BearerPresent
)

// unknownVariablePrefix is how tmux begins its stderr when a name is not in the
// environment it was asked about. An unset name exits 1 with this text, so exit
// 1 alone does not tell a missing name from a failed call.
const unknownVariablePrefix = "unknown variable:"

// BearerPresence reports whether name holds a non-empty value in the tmux
// environment a new pane of session would inherit: the session's own
// environment first, then the server's global one. It reads one name per call,
// and reads only the shape of the answer. The value is never returned, logged
// or put into an error: the output of show-environment carries it.
func (d *Driver) BearerPresence(session, name string) BearerPresence {
	state, fallThrough := d.showEnvironment(name, "-t", sessionTarget(session))
	if !fallThrough {
		return state
	}
	state, fallThrough = d.showEnvironment(name, "-g")
	if fallThrough {
		return BearerAbsent
	}
	return state
}

// showEnvironment runs show-environment for one name in one scope. fallThrough
// is true when tmux said the name is unknown to that scope, which is the cue to
// ask the next one (or, for the last scope, to read it as absent).
func (d *Driver) showEnvironment(name string, scope ...string) (state BearerPresence, fallThrough bool) {
	args := append([]string{"show-environment"}, scope...)
	args = append(args, name)
	out, err := d.cmd(args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && bytes.HasPrefix(bytes.TrimSpace(ee.Stderr), []byte(unknownVariablePrefix)) {
			return BearerAbsent, true
		}
		return BearerUnknown, false
	}
	line := strings.TrimSpace(string(out))
	if value, ok := strings.CutPrefix(line, name+"="); ok && value != "" {
		return BearerPresent, false
	}
	// "-NAME" (removed from the environment) and "NAME=" (set empty) are both
	// final: the scope answered, and the answer is no value.
	return BearerAbsent, false
}
