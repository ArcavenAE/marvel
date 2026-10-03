package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
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
	// CommitUnconfirmed is set when Commit came from the toolchain and nothing
	// in the version confirms it (a dev build, a stable tag).
	CommitUnconfirmed bool `json:"commit_unconfirmed,omitempty"`
	// Dirty is set when the build had uncommitted changes (vcs.modified).
	Dirty bool `json:"dirty,omitempty"`
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

// VerifiedCommit returns revision only when the version confirms it. Go's VCS
// stamping skips a linked worktree's .git file and walks up to an enclosing
// repository, so a build made in a worktree can carry the wrong repository's
// commit; a wrong commit is worse than none. An alpha version ends in the
// short sha it was built from, and the revision must start with it. A version
// that names no sha (a dev build, a stable tag) confirms nothing, so no commit
// is reported for it.
func VerifiedCommit(version, revision string) string {
	sha := trailingSHA(version)
	if sha == "" || revision == "" || !strings.HasPrefix(strings.ToLower(revision), sha) {
		return ""
	}
	return revision
}

// trailingSHA returns the last dot- or dash-separated part of version when it
// looks like a short git sha (7 to 40 lower-case hex digits), else "".
func trailingSHA(version string) string {
	parts := strings.FieldsFunc(strings.ToLower(version), func(r rune) bool { return r == '.' || r == '-' })
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if len(last) < 7 || len(last) > 40 {
		return ""
	}
	for _, r := range last {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return last
}

// BuildFor assembles a Build from the stamped version and channel and what the
// toolchain recorded. Scaffold.
func BuildFor(version, channel, revision string, modified bool) Build {
	_, _ = revision, modified
	return Build{Version: version, Channel: channel}
}

// VCSInfo returns the toolchain's recorded revision and whether the tree was
// modified. Scaffold.
func VCSInfo() (revision string, modified bool) { return VCSRevision(), false }
