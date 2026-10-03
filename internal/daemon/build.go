package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"time"
)

// MethodVersion is the read-only RPC that reports the running daemon's build.
// It answers from the daemon's own memory and never touches the state store,
// which this process holds under an exclusive bbolt lock.
const MethodVersion = "version"

// Build names a marvel build.
type Build struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
	Commit  string `json:"commit,omitempty"`
}

// Describe renders the build for a log line or a version report.
func (b Build) Describe(pid int) string {
	s := fmt.Sprintf("%s (%s)", b.Version, b.Channel)
	if b.Commit != "" {
		s += " commit " + b.Commit
	}
	return fmt.Sprintf("%s pid %d", s, pid)
}

// BuildInfo is what a running daemon reports about itself. StartedAt is when
// this process image started: a reexec keeps the pid and changes it.
type BuildInfo struct {
	Build
	StartedAt time.Time `json:"started_at"`
	PID       int       `json:"pid"`
}

// VCSRevision returns the commit this binary was built from, or "" when the
// toolchain did not record one (a build outside a git tree, or -buildvcs=false).
func VCSRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, kv := range info.Settings {
		if kv.Key == "vcs.revision" {
			return kv.Value
		}
	}
	return ""
}

func (d *Daemon) handleVersion() Response {
	result, err := json.Marshal(BuildInfo{Build: d.build, StartedAt: d.startedAt, PID: os.Getpid()})
	if err != nil {
		return Response{Error: fmt.Sprintf("encode build: %v", err)}
	}
	return Response{Result: result}
}
