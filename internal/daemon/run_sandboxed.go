package daemon

import (
	"context"
	"time"

	"github.com/arcavenae/marvel/internal/api"
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
// at maxOut bytes.
func runChild(ctx context.Context, argv []string, dir string, env []string, stdin []byte, timeout time.Duration, maxOut int) (api.RunSandboxedResult, error) {
	return api.RunSandboxedResult{}, nil // red: not built
}
