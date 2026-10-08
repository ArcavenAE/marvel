package tmux

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
// -L name among its global options. For the name "default", which is tmux's
// own default server and what MARVEL_TMUX_SOCKET=default selects, a tmux with
// neither -L nor -S also counts: a user's default server is usually started
// as plain `tmux`. A client of that server matches too, which can only make
// the answer "alive".
func carriesSocket(fields []string, name string) bool {
	if len(fields) < 2 || filepath.Base(fields[1]) != "tmux" {
		return false
	}
	args := fields[2:]
	named := false // -L or -S seen among the global options
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break // the command starts here; the options are behind us
		}
		if a == "-L" {
			if i+1 < len(args) && args[i+1] == name {
				return true
			}
			named = true
		}
		if a == "-S" {
			named = true
		}
		if tmuxFlagsWithArg[a] {
			i++
		}
	}
	return name == "default" && !named
}

// startupBound is how long a query waits for a server that is alive but has
// not begun to listen. 590ms is max(500ms, 10 x 59ms), where 59ms is the
// longest wait measured on tmux 3.4 on Linux (200 runs, one container). A
// variable so a test can shorten it.
var startupBound = 590 * time.Millisecond

// tmuxRun is one tmux query: its stdout, the text tmux printed on stderr (or
// the combined output) when it failed, and the error.
type tmuxRun func() (out []byte, text string, err error)

// whileStarting runs a query and, when tmux says the server is absent while a
// server process for this name is alive, runs it again until the server
// answers or startupBound passes. A server process exists a moment before it
// listens, and a bare absence answer in that moment is not an outage.
//
// It returns the last answer and one verdict, absent, which the caller uses
// as it stands: the process list is read once per answer, and a caller that
// read it again could see a server another caller started in between and turn
// a settled absence into an outage. absent is true only for an absence answer
// with no live server process. A timeout, any text that is not an absence
// answer, an unreadable process list, and a server still silent after the
// bound are all not absent.
//
// The waits are 10, 20, 40 and 80ms, then 100ms steps. Each attempt keeps the
// driver's exec timeout.
func (d *Driver) whileStarting(run tmuxRun) (out []byte, text string, absent bool, err error) {
	out, text, err = run()
	retry, absent := d.startingVerdict(text, err)
	logged := false
	logHit := func() {
		if retry && !logged && retryOnly(text) {
			logged = true
			log.Printf("debug: tmux retry-only answer while a server process is alive: %q", text)
		}
	}
	logHit()
	if !retry {
		return out, text, absent, err
	}
	deadline := time.Now().Add(startupBound)
	wait := 10 * time.Millisecond
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return out, text, false, err
		}
		time.Sleep(min(wait, remaining))
		if wait < 80*time.Millisecond {
			wait *= 2
		} else {
			wait = 100 * time.Millisecond
		}
		out, text, err = run()
		retry, absent = d.startingVerdict(text, err)
		logHit()
		if !retry {
			return out, text, absent, err
		}
	}
}

// retryOnlyTexts are tmux answers that mean "try again while a server process
// is alive" and never mean absence. With no live process they stay an error,
// and after the bound they are an outage.
//
//   - "server exited unexpectedly": a tmux 3.4 client reached a server that
//     was starting or shutting down and it closed the connection.
//   - "no current target": PROVISIONAL. Measured once on tmux 3.4 under load;
//     the mechanism is unconfirmed (it may mean a live server with zero
//     sessions). It is not reclassified on inference.
var retryOnlyTexts = []string{"server exited unexpectedly", "no current target"}

func retryOnly(text string) bool {
	for _, t := range retryOnlyTexts {
		if strings.Contains(text, t) {
			return true
		}
	}
	return false
}

// startingVerdict reads one answer. retry is true for an absence answer or a
// retry-only answer while a server process is alive. absent is true only for
// an absence answer with no live process; a retry-only answer is never absent.
func (d *Driver) startingVerdict(text string, err error) (retry, absent bool) {
	if err == nil || errors.Is(err, ErrTmuxTimeout) {
		return false, false
	}
	only := retryOnly(text)
	if !only && classifyAbsence(text) == absenceNone {
		return false, false
	}
	alive, perr := d.serverProcessAlive()
	if perr != nil {
		return false, false
	}
	if only {
		return alive, false
	}
	return alive, !alive
}
