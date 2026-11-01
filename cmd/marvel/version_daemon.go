package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/daemonrpc"
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
	resp, err := send(daemonrpc.Request{Method: daemonrpc.MethodVersion})
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
	simulatorReport(w, client.Version, client.Channel, exec.LookPath, runSimulatorVersion)

	info, err := q()
	switch {
	case errors.Is(err, errDaemonPredates):
		say("daemon  does not report its build (it predates this report); " +
			adoptsText)
	case err != nil:
		// No daemon, or it did not answer: nothing to compare.
	default:
		say("daemon  %s pid %d started %s", info.Label(), info.PID, info.StartedAt.UTC().Format(time.RFC3339))
		warning, note := compareBuilds(client, info.Build)
		if warning != "" {
			say("warning: %s", warning)
		}
		if note != "" {
			say("note: %s", note)
		}
	}
}

// buildWarned is set once a build-mismatch warning has been printed, so a
// command that calls the daemon many times (get -w polls) prints it once.
var buildWarned atomic.Bool

// buildMismatchWarning is the warning for a daemon whose stamped build differs
// from client, or "" when they match, when the stamp is absent (a daemon that
// predates it) or cannot be read.
func buildMismatchWarning(client daemon.Build, stamp json.RawMessage) string {
	if len(stamp) == 0 {
		return ""
	}
	var d daemon.Build
	if err := json.Unmarshal(stamp, &d); err != nil {
		return ""
	}
	// Only a warning, never the note: a note is a difference that cannot be
	// ruled out, and printing it on every verb of a dev build would be noise.
	// marvel version still prints it.
	warning, _ := compareBuilds(client, d)
	return warning
}

// noteBuildMismatch prints the build-mismatch warning to w at most once per
// process. marvel version prints its own comparison, so its call is skipped.
func noteBuildMismatch(w io.Writer, method string, stamp json.RawMessage) {
	if method == daemonrpc.MethodVersion || buildWarned.Load() {
		return
	}
	warning := buildMismatchWarning(thisBuild(), stamp)
	if warning == "" || !buildWarned.CompareAndSwap(false, true) {
		return
	}
	_, _ = fmt.Fprintf(w, "warning: %s\n", drawn(warning))
}

// thisBuild is the build of the running binary: its stamped version and channel
// and what the toolchain recorded about the commit it was built from.
func thisBuild() daemon.Build {
	revision, modified := daemon.VCSInfo()
	return daemon.BuildFor(version, channel, revision, modified)
}

// compareBuilds says whether the daemon is a different build than the client.
// A warning is a difference that was shown: the versions or channels differ, or
// the versions match and both sides recorded revisions that differ. A note is a
// difference that cannot be ruled out: a changed tree at the same revision, or
// dev builds with no revision recorded, which the version string cannot tell
// apart. A revision with no sha in the version to confirm it can be the wrong
// repository's, so it is printed marked unconfirmed.
func compareBuilds(client, d daemon.Build) (warning, note string) {
	const adopt = adoptsText
	rev := func(b daemon.Build) string { return cmp.Or(b.Commit, b.Revision) }
	cr, dr := rev(client), rev(d)
	switch {
	case d.Version != client.Version || d.Channel != client.Channel:
		return fmt.Sprintf("the running daemon is a different build (%s, %s) than this client; %s",
			d.Version, d.Channel, adopt), ""
	case cr != "" && dr != "" && cr != dr:
		return fmt.Sprintf("the running daemon is a different build: the same version %s, but commit %s, "+
			"and this client is commit %s; %s", d.Version, d.CommitLabel(), client.CommitLabel(), adopt), ""
	case cr != "" && dr != "" && (client.Dirty || d.Dirty):
		return "", "the client or the daemon was built with uncommitted or untracked changes, " +
			"so builds at the same commit can still differ"
	case client.Version == "dev" && (cr == "" || dr == ""):
		return "", "dev builds with no commit recorded cannot be told apart; build from a git checkout, or stamp a version"
	}
	return "", ""
}

// simulatorVersionTimeout bounds the simulator's answer to -version, so a
// program that hangs cannot hold marvel version.
const simulatorVersionTimeout = 2 * time.Second

// runSimulatorVersion runs `<path> -version` and returns what it printed.
func runSimulatorVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), simulatorVersionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-version").Output()
	return string(out), err
}

// simulatorReport prints the build of the marvel-simulator on PATH, and a note
// when it is not the build of this marvel. A tour seat runs that program, and
// the two ship together, so a difference is worth a line. Nothing is printed
// when no simulator is on PATH, because marvel does not need one. look and run
// are injected so a test needs no program on PATH.
func simulatorReport(w io.Writer, version, channel string, look func(string) (string, error), run func(string) (string, error)) {
	path, err := look("marvel-simulator")
	if err != nil {
		return
	}
	out, err := run(path)
	fields := strings.Fields(out)
	// "marvel-simulator <version> (<channel>)"
	if err != nil || len(fields) != 3 || fields[0] != "marvel-simulator" || !strings.HasPrefix(fields[2], "(") || !strings.HasSuffix(fields[2], ")") {
		_, _ = fmt.Fprintf(w, "simulator %s does not report its version\n", path)
		return
	}
	simVersion, simChannel := fields[1], strings.Trim(fields[2], "()")
	_, _ = fmt.Fprintf(w, "simulator %s (%s)\n", simVersion, simChannel)
	if simVersion != version || simChannel != channel {
		_, _ = fmt.Fprintf(w, "note: the simulator on PATH is %s (%s) and marvel is %s (%s); they ship together, so install both from one release\n",
			simVersion, simChannel, version, channel)
	}
}
