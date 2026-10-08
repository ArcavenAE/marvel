package main

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/procargs"
)

// Seams for the reexec pre-flight and the reexec request.
var (
	preflightQuery buildQuery = queryDaemonBuild
	daemonArgs                = procargs.Read
	reexecSend                = func(req daemon.Request) (*daemon.Response, error) { return send(req) }
)

// preflightReexec refuses, before the reexec request goes out, a daemon whose
// own arguments this build's `marvel daemon` would reject. A reexec executes
// the new binary with the old argument list, so a daemon that was started with
// a word the new build refuses (marvel#606) would die in the exec while the
// client reports success. Only code that runs before the exec can stop that,
// and that code is this client.
//
// The arguments go through the real daemon command's flag parse and Args
// check, not a copy of them. What cannot be read is refused, never guessed. A
// remote daemon is skipped: its pid is on another host, so no local process
// table can answer for it.
func preflightReexec(w io.Writer) error {
	addr, _, err := resolveDaemonAddr()
	if err != nil {
		return nil
	}
	if !isLocalDaemonAddr(addr) {
		_, _ = fmt.Fprintf(w, "pre-flight skipped: %s is remote, so its startup arguments cannot be read from here; "+
			"if it was started with arguments this build's `marvel daemon` rejects, it will not come back. "+
			"Check them on that host first.\n", cmp.Or(clusterName, addr))
		return nil
	}
	info, err := boundedQuery(versionQueryTimeout, preflightQuery)
	if err != nil {
		return unreadable(fmt.Sprintf("the daemon's pid could not be read (%v)", err))
	}
	argv, err := daemonArgs(info.PID)
	if err != nil {
		return unreadable(fmt.Sprintf("the arguments of pid %d could not be read (%v)", info.PID, err))
	}
	i := slices.Index(argv, "daemon")
	if i < 1 {
		return unreadable(fmt.Sprintf("pid %d was not started as `marvel daemon` (its arguments are %q)", info.PID, strings.Join(argv, " ")))
	}
	if err := parseDaemonArgs(argv[i+1:]); err != nil {
		return fmt.Errorf("the running daemon was started as `%s`, which this build's `marvel daemon` rejects (%v); "+
			"%s", strings.Join(argv, " "), err, restartPointer)
	}
	return nil
}

const restartPointer = "stop it with `marvel stop --keep-bus` and start it again with `marvel daemon <flags>`; agents survive the restart with adopt"

func unreadable(what string) error {
	return fmt.Errorf("cannot check that the running daemon would start under this build: %s; %s", what, restartPointer)
}

// parseDaemonArgs runs args through the real daemon command: its flag set and
// its Args check, the two things that decide whether `marvel daemon` starts.
func parseDaemonArgs(args []string) error {
	c := daemonCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if err := c.ParseFlags(args); err != nil {
		return err
	}
	return c.ValidateArgs(c.Flags().Args())
}
