package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// authorizeRun resolves the seat a run.sandboxed token belongs to. The request
// names no session, so the token is the identity; only a bound token of a
// running seat is admitted.
func (d *Daemon) authorizeRun(token string) (api.Session, error) {
	return api.Session{}, nil // red: not built
}

// runLogRecord is the one log line for a run: who, what program, which profile
// by digest, and how it ended. It carries no environment value, no stdin, no
// argument past the program name and no profile text.
func runLogRecord(sess api.Session, req api.RunSandboxedRequest, composed string, rc int, took time.Duration) string {
	return "" // red: not built
}

// runChild runs argv in its own process group and returns when it exits. The
// group is killed at the timeout and when ctx ends, and each stream is capped
// at maxOut bytes. A command that cannot be started is an error, never a
// result with an exit status.
func runChild(ctx context.Context, argv []string, dir string, env []string, timeout time.Duration, maxOut int) (api.RunSandboxedResult, error) {
	return api.RunSandboxedResult{}, nil // red: not built
}

// runCarveOut is the path the deny block lets back through a denied folder: the
// seat's recorded working directory, never the request's Dir.
func runCarveOut(sess api.Session, req api.RunSandboxedRequest) []string {
	return nil // red: not built
}

// handleRunSandboxed serves run.sandboxed. ctx is the connection's: it ends
// when the seat hangs up.
func (d *Daemon) handleRunSandboxed(ctx context.Context, params json.RawMessage) daemonrpc.Response {
	return daemonrpc.Response{} // red: not built
}
