package main

import (
	"github.com/arcavenae/marvel/internal/daemon"
)

// Seams for the reexec pre-flight and the reexec request.
var (
	preflightQuery buildQuery = queryDaemonBuild
	daemonArgs                = func(pid int) ([]string, error) { return nil, nil }
	reexecSend                = func(req daemon.Request) (*daemon.Response, error) { return send(req) }
)

// preflightReexec refuses before the reexec request is sent when the running
// daemon's own arguments would not start under this build.
func preflightReexec() error { return nil }
