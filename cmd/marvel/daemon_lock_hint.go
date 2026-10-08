package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/pidfile"
)

// liveSignal is the liveness probe: nil means the process exists and the caller
// may signal it. A seam so tests do not probe processes they do not own.
var liveSignal = func(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.Signal(0))
}

// lockedStateHint explains a daemon that could not take the state file lock.
// The lock is the one thing that stops a second daemon, so a held lock usually
// means a daemon is already running, which reads as the opposite when the error
// is a bare timeout (marvel#606). A holder is named only once it is identified:
// the pidfile's pid must be live and callable, and its argv must say it is
// `marvel daemon`, read by the reader the reexec pre-flight uses. Otherwise the
// pid is offered as a lead that may not be the holder, and nothing is claimed,
// because a stale pidfile can name a reused pid. The cause stays in the chain.
func lockedStateHint(err error, pidFile string, root *cobra.Command) error {
	if err == nil || !errors.Is(err, api.ErrBoltLocked) {
		return err
	}
	const pointer = "`marvel describe daemon` shows a running one"
	pid := pidFromFile(pidFile)
	if pid == 0 {
		return fmt.Errorf("the state lock is held by another process, and the pidfile does not name one; %s: %w", pointer, err)
	}
	if argv, ok := identifiedHolder(pid, root); ok {
		return fmt.Errorf("%s: %w", holderMessage(pid, argv), err)
	}
	return fmt.Errorf("the state lock is held; the pidfile names pid %d, which may not be the holder; %s: %w", pid, pointer, err)
}

// isMarvelDaemon reports whether argv is `marvel daemon ...`: the program is
// named marvel and the command tree resolves the rest to the daemon command.
// The tree knows which root flags take a value (`--cluster prod daemon`), so
// no flag list is kept here.
func isMarvelDaemon(root *cobra.Command, argv []string) bool {
	if len(argv) == 0 || filepath.Base(argv[0]) != "marvel" {
		return false
	}
	c, _, err := root.Find(argv[1:])
	return err == nil && c != root && c.Name() == "daemon" && c.Parent() == root
}

// pidFromFile returns the pid the pidfile holds, and zero when the file is off,
// missing, unreadable, or holds no valid pid. The one parser is shared with the
// daemon's own start guard (internal/pidfile).
func pidFromFile(path string) int { return pidfile.Read(path) }

// daemonPositionalArgs refuses a positional argument, because `marvel daemon` starts the
// daemon. `status` is the one people reach for, so it is pointed at the read
// that exists.
func daemonPositionalArgs(cmd *cobra.Command, args []string) error {
	err := cobra.NoArgs(cmd, args)
	if err != nil && len(args) > 0 && args[0] == "status" {
		return fmt.Errorf("%w; did you mean `marvel describe daemon`?", err)
	}
	return err
}

// identifiedHolder reports the argv of the process the pid names when it is a
// marvel daemon this one may take as the holder: not this process (a reexec
// keeps its pid, and in a container a restarted daemon can be pid 1 again), a
// process the caller can signal, and one whose argv says `marvel daemon`. Only a
// process the caller can signal can be the holder: the state file is 0600, so
// another user's process, which signal 0 answers with EPERM, cannot have opened
// it.
func identifiedHolder(pid int, root *cobra.Command) ([]string, bool) {
	if pid == os.Getpid() || liveSignal(pid) != nil {
		return nil, false
	}
	argv, err := daemonArgs(pid)
	if err != nil || !isMarvelDaemon(root, argv) {
		return nil, false
	}
	return argv, true
}

// holderMessage names an identified holder and how it was started.
func holderMessage(pid int, argv []string) string {
	return fmt.Sprintf("another marvel daemon holds the state lock (pid %d, started as `%s`); it is already running, and `marvel describe daemon` shows it",
		pid, strings.Join(argv, " "))
}

// runningMessage is what the fast path knows: the pidfile names a marvel daemon
// that is running. It says nothing of any state file's lock, which the fast path
// never opened, and which a daemon with its own --state-bolt does not hold.
func runningMessage(pidFile string, pid int, argv []string) string {
	return fmt.Sprintf("another marvel daemon is running against pidfile %s (pid %d, started as `%s`)",
		pidFile, pid, strings.Join(argv, " "))
}

// refuseLiveDaemon is the fast path in front of the state lock (marvel#744). A
// second daemon would otherwise wait five seconds on the lock before the
// pidfile guard in Start could say a daemon is running. It refuses at once only
// when the pidfile names an identified marvel daemon, and says what the pidfile
// shows, not what the lock would. A dead pid, another user's process, a process that is not a
// daemon, an argv that cannot be read and a pidfile that names nothing all
// return nil: the lock stays the authority and decides.
func refuseLiveDaemon(pidFile string, root *cobra.Command) error {
	pid := pidFromFile(pidFile)
	if pid == 0 {
		return nil
	}
	if argv, ok := identifiedHolder(pid, root); ok {
		return errors.New(runningMessage(pidFile, pid, argv))
	}
	return nil
}
