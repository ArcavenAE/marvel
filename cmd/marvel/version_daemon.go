package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/daemon"
)

// versionQueryTimeout bounds how long `marvel version` waits on a daemon. The
// socket has no read deadline, so a wedged daemon would otherwise hang it.
const versionQueryTimeout = 2 * time.Second

// buildQuery asks the running daemon for its build.
type buildQuery func() (*daemon.BuildInfo, error)

// errDaemonPredates means the daemon answered but does not know the version
// method, so it is older than the build that added it.
var errDaemonPredates = errors.New("daemon predates the version method")

// boundedQuery runs q and gives up after d. The abandoned call finishes or
// dies with the process; marvel version exits right after.
func boundedQuery(d time.Duration, q buildQuery) (*daemon.BuildInfo, error) {
	type result struct {
		info *daemon.BuildInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := q()
		done <- result{info, err}
	}()
	select {
	case r := <-done:
		return r.info, r.err
	case <-time.After(d):
		return nil, fmt.Errorf("daemon did not answer within %s", d)
	}
}

// queryDaemonBuild asks the running daemon, through the same path every other
// command uses, which build it is.
func queryDaemonBuild() (*daemon.BuildInfo, error) {
	resp, err := send(daemon.Request{Method: daemon.MethodVersion})
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(resp.Error, "unknown method") {
		return nil, errDaemonPredates
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	var info daemon.BuildInfo
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		return nil, fmt.Errorf("decode daemon build: %w", err)
	}
	return &info, nil
}

// versionReport prints the client's build and, when a daemon answers, the
// daemon's beside it. The client line stays first and unchanged: scripts read
// it. With no daemon running, nothing more is printed, because marvel version
// works without one.
func versionReport(w io.Writer, client daemon.Build, q buildQuery) {
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	say("marvel %s (%s)", client.Version, client.Channel)

	info, err := q()
	switch {
	case errors.Is(err, errDaemonPredates):
		say("daemon  does not report its build (it predates this report); " +
			"'marvel daemon reexec' adopts the installed binary")
	case err != nil:
		// No daemon, or it did not answer: nothing to compare.
	default:
		line := fmt.Sprintf("daemon  %s (%s)", info.Version, info.Channel)
		if info.Commit != "" {
			line += " commit " + info.Commit
		}
		say("%s pid %d started %s", line, info.PID, info.StartedAt.UTC().Format(time.RFC3339))
		if info.Version != client.Version || info.Channel != client.Channel {
			say("warning: the running daemon is a different build (%s, %s) than this client; "+
				"'marvel daemon reexec' adopts the installed binary", info.Version, info.Channel)
		}
	}
}
