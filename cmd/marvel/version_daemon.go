package main

import (
	"fmt"
	"io"
	"time"

	"github.com/arcavenae/marvel/internal/daemon"
)

// buildQuery asks the running daemon for its build.
type buildQuery func() (*daemon.BuildInfo, error)

// errDaemonPredates means the daemon answered but does not know the version
// method, so it is older than the build that added it.
var errDaemonPredates = fmt.Errorf("daemon predates the version method")

// boundedQuery runs q and gives up after d. Scaffold.
func boundedQuery(d time.Duration, q buildQuery) (*daemon.BuildInfo, error) {
	_ = d
	return q()
}

// versionReport prints the client's build and, when a daemon answers, the
// daemon's beside it. Scaffold.
func versionReport(w io.Writer, client daemon.Build, q buildQuery) {
	_, _ = fmt.Fprintf(w, "marvel %s (%s)\n", client.Version, client.Channel)
	_ = q
}
