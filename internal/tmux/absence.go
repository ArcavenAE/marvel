package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// absence is what tmux's stderr says about the server it was asked to reach.
type absence int

const (
	// absenceNone: the text does not say the server is absent. Anything
	// unrecognised lands here, which a caller reads as an outage.
	absenceNone absence = iota
	// absenceNoServer: "no server running on <path>". tmux prints it for a
	// socket nothing serves, and also for a live server whose listen backlog
	// is full (a stopped server), so it is absence only when no server
	// process is alive.
	absenceNoServer
	// absenceNoSocket: "error connecting to <path> (No such file or
	// directory)" or "(Connection refused)". The socket is missing or dead,
	// which is also what a live server behind an unreachable path looks like,
	// so it too counts as absence only when no server process is alive.
	absenceNoSocket
)

// connectingRE matches the connecting prefix together with the errno text. A
// bare errno is not matched: other failures print it too.
var connectingRE = regexp.MustCompile(`error connecting to .+ \((?:No such file or directory|Connection refused)\)`)

// classifyAbsence reads tmux's stderr. A timeout is never passed here: a
// caller checks ErrTmuxTimeout first.
//
// The wording was measured on tmux 3.7b on macOS: "no server running on
// <path>" for a socket whose server exited or was killed, and "error
// connecting to <path> (No such file or directory)" for a socket file that is
// not there. "(Connection refused)" is the wording the design ruling names and
// was not produced on that build. The wording on Linux is unmeasured.
func classifyAbsence(text string) absence {
	switch {
	case strings.Contains(text, "no server running"):
		return absenceNoServer
	case connectingRE.MatchString(text):
		return absenceNoSocket
	}
	return absenceNone
}

// procList lists processes as `ps -axo pid,args` does; a test replaces it.
type procList func(ctx context.Context) ([]byte, error)

func psAll(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "ps", "-axo", "pid,args").Output()
}

// serverAbsent reports whether tmux's stderr means there is no server to ask.
// It is the one rule every caller that reads tmux's stderr applies: a server
// answer that says "no server", or that the socket is missing or refused, is
// absence only when no tmux server for this socket name is alive. After a host
// reboot the socket directory is gone with the server, so the process list is
// empty and the answer is absence. A live server behind a socket that cannot
// be reached, or one stopped with a full listen backlog, is an outage, and
// reading it as absence would reap every session it still runs or start a
// second server. When the process list cannot be read the answer is "not
// absent": the safe direction is the outage.
//
// "can't find pane/window/session" is not here: a live server is the one
// answering.
func (d *Driver) serverAbsent(text string) bool {
	if classifyAbsence(text) == absenceNone {
		return false
	}
	alive, err := d.serverProcessAlive()
	return err == nil && !alive
}

// serverProcessAlive reports whether a tmux server for this driver's socket
// name is running, read from one process listing. The anchor is the program
// (base name tmux) and a -L argument in its global options, ahead of the
// command: `-L <name>` after the command is some other flag's value, and
// another program or another socket's server does not count. A client for the
// same socket also matches, which can only make the answer "alive".
//
// This reads the server's own argv, which on tmux 3.7b on macOS carries
// -L <name> (measured). A build that retitles its server process would not
// match here and would read as absent; that case is unmeasured and the
// fallback to the recorded session pids is not implemented.
func (d *Driver) serverProcessAlive() (bool, error) {
	if d.socket == "" {
		// The shared default server has no -L to anchor on.
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout())
	defer cancel()
	list := d.procs
	if list == nil {
		list = psAll
	}
	out, err := list(ctx)
	if err != nil {
		return false, fmt.Errorf("list processes: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if carriesSocket(strings.Fields(line), d.socket) {
			return true, nil
		}
	}
	return false, nil
}

// tmuxFlagsWithArg are tmux's global options that take a value.
var tmuxFlagsWithArg = map[string]bool{"-c": true, "-f": true, "-L": true, "-S": true, "-T": true}

// carriesSocket reports whether a `pid args...` process line is a tmux with
// -L name among its global options.
func carriesSocket(fields []string, name string) bool {
	if len(fields) < 2 || filepath.Base(fields[1]) != "tmux" {
		return false
	}
	args := fields[2:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return false // the command starts here; the options are behind us
		}
		if a == "-L" {
			return i+1 < len(args) && args[i+1] == name
		}
		if tmuxFlagsWithArg[a] {
			i++
		}
	}
	return false
}
