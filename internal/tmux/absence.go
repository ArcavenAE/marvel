package tmux

import (
	"context"
	"strings"
)

// absence is what tmux's stderr says about the server it was asked to reach.
type absence int

const (
	// absenceNone: the text does not say the server is absent. Anything
	// unrecognised lands here, which a caller reads as an outage.
	absenceNone absence = iota
	// absenceNoServer: "no server running on <path>", the socket exists and
	// nothing answers it. The server is gone.
	absenceNoServer
	// absenceNoSocket: "error connecting to <path> (No such file or
	// directory)" or "(Connection refused)". The socket is missing or dead,
	// which is also what a live server behind an unreachable path looks like,
	// so it counts as absence only when no server process is alive.
	absenceNoSocket
)

// classifyAbsence reads tmux's stderr. A timeout is never passed here: a
// caller checks ErrTmuxTimeout first.
func classifyAbsence(text string) absence { return absenceNone }

// procList lists processes as `ps -axo pid,args` does; a test replaces it.
type procList func(ctx context.Context) ([]byte, error)

// serverAbsent reports whether tmux's stderr means there is no server to ask.
func (d *Driver) serverAbsent(text string) bool {
	return strings.Contains(text, "no server running") || strings.Contains(text, "No such file or directory")
}
