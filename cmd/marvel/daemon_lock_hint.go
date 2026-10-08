package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
)

// lockedStateHint explains a daemon that could not take the state file lock.
// The lock is the one thing that stops a second daemon, so a held lock usually
// means a daemon is already running, which reads as the opposite when the
// error is a bare timeout (marvel#606). The holder is named only when the
// pidfile names a live process; otherwise the message says the lock is held
// and does not claim a daemon it cannot see. The cause stays in the chain.
func lockedStateHint(err error, pidFile string) error {
	if err == nil || !errors.Is(err, api.ErrBoltLocked) {
		return err
	}
	if pid := livePidFromFile(pidFile); pid > 0 {
		return fmt.Errorf("another marvel daemon holds the state lock (pid %d, from %s); it is already running, and `marvel describe daemon` shows it: %w",
			pid, pidFile, err)
	}
	return fmt.Errorf("the state lock is held by another process, and the pidfile does not name a live daemon; if one is running, `marvel describe daemon` shows it: %w", err)
}

// livePidFromFile returns the pid in the pidfile when it names a running
// process, and zero when the file is off, missing, unreadable, not a pid, or
// names a process that is gone.
func livePidFromFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		return 0
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0
	}
	// Signal 0 is the "is it alive" check on Unix; EPERM still means alive.
	if err := proc.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
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
