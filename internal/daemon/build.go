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

// Build names a marvel build. Commit is a revision the version confirms, and it
// keeps the meaning it had when the version report first shipped: a client that
// predates Revision and Dirty reads Commit and nothing else, so an unconfirmed
// revision must never be put there.
type Build struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
	Commit  string `json:"commit,omitempty"`
	// Revision is the revision the toolchain recorded when nothing in the
	// version confirms it (a dev build, a stable tag). It is what tells two dev
	// builds apart, and it can be the wrong repository's when the build was made
	// in a linked worktree, so it is always shown as unconfirmed.
	Revision string `json:"revision,omitempty"`
	// Dirty is set when the build's tree had changes vcs.modified counts:
	// uncommitted edits and also untracked files.
	Dirty bool `json:"dirty,omitempty"`
}

// CommitLabel renders the commit alone, or the unconfirmed revision marked as
// such, or "" when neither is known.
func (b Build) CommitLabel() string {
	switch {
	case b.Commit != "":
		return b.Commit
	case b.Revision != "":
		return b.Revision + " (unconfirmed)"
	}
	return ""
}

// Label renders the build for a log line or a version report: version and
// channel, then the commit when one is known, and "(dirty)" when the tree had
// changes vcs.modified counts.
func (b Build) Label() string {
	s := fmt.Sprintf("%s (%s)", b.Version, b.Channel)
	if c := b.CommitLabel(); c != "" {
		s += " commit " + c
	}
	if b.Dirty {
		s += " (dirty)"
	}
	return s
}

// Describe is Label with the process id, for the startup log line.
func (b Build) Describe(pid int) string {
	return fmt.Sprintf("%s pid %d", b.Label(), pid)
}

// BuildInfo is what a running daemon reports about itself. StartedAt is when
// this process image started: a reexec keeps the pid and changes it.
type BuildInfo struct {
	Build
	StartedAt time.Time `json:"started_at"`
	PID       int       `json:"pid"`
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
// toolchain recorded. A version that names its sha confirms the revision or
// contradicts it, and a contradicted revision is dropped, because Go can stamp
// an enclosing repository's commit on a build made in a linked worktree and a
// wrong commit is worse than none. A version that names no sha (a dev build, a
// stable tag) confirms nothing, so the revision goes in Revision, never in
// Commit. The dirty flag follows whatever revision survives: with it dropped
// the flag goes too.
func BuildFor(version, channel, revision string, modified bool) Build {
	b := Build{Version: version, Channel: channel}
	switch {
	case revision == "":
	case trailingSHA(version) != "":
		b.Commit = VerifiedCommit(version, revision)
	default:
		b.Revision = revision
	}
	b.Dirty = modified && (b.Commit != "" || b.Revision != "")
	return b
}

// VCSInfo returns the revision the toolchain recorded and whether the tree had
// uncommitted changes (vcs.modified). Both are empty or false when the build
// carries no VCS stamp.
func VCSInfo() (revision string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, kv := range info.Settings {
		switch kv.Key {
		case "vcs.revision":
			revision = kv.Value
		case "vcs.modified":
			modified = kv.Value == "true"
		}
	}
	return revision, modified
}
