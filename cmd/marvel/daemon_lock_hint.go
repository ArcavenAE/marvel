package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
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
func lockedStateHint(err error, pidFile string) error {
	if err == nil || !errors.Is(err, api.ErrBoltLocked) {
		return err
	}
	const pointer = "`marvel describe daemon` shows a running one"
	pid := pidFromFile(pidFile)
	if pid == 0 {
		return fmt.Errorf("the state lock is held by another process, and the pidfile does not name one; %s: %w", pointer, err)
	}
	// Only a process the caller can signal can be the holder: the state file is
	// 0600, so another user's process, which signal 0 answers with EPERM, cannot
	// have opened it.
	if liveSignal(pid) == nil {
		if argv, aerr := daemonArgs(pid); aerr == nil && isMarvelDaemon(argv) {
			return fmt.Errorf("another marvel daemon holds the state lock (pid %d, started as `%s`); it is already running, and `marvel describe daemon` shows it: %w",
				pid, strings.Join(argv, " "), err)
		}
	}
	return fmt.Errorf("the state lock is held; the pidfile names pid %d, which may not be the holder; %s: %w", pid, pointer, err)
}

// isMarvelDaemon reports whether argv is `marvel daemon ...`: the program is
// named marvel and the first argument that is not a flag is daemon.
func isMarvelDaemon(argv []string) bool {
	if len(argv) == 0 || filepath.Base(argv[0]) != "marvel" {
		return false
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a == "daemon"
	}
	return false
}

// pidFromFile returns the pid the pidfile holds, and zero when the file is
// off, missing, unreadable, or holds anything but a positive decimal number
// (surrounding whitespace aside). A pid at or below zero is never returned:
// kill(-5, 0) would signal a process group.
func pidFromFile(path string) int {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

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
