// Package view builds and swaps a per-seat read-only view of a repository's
// default branch (docs/design/readonly-view.md, marvel#609). It has no
// controller wiring: a caller hands it a directory, a remote and a ref, and
// it keeps a bare mirror, one extracted tree per commit, and a cur symlink
// that moves with one rename.
package view

import (
	"context"
	"errors"
	"os"
	"strings"
)

// Step names the part of a refresh that failed.
type Step string

const (
	StepFetch   Step = "fetch"
	StepResolve Step = "resolve"
	StepExtract Step = "extract"
	StepSwap    Step = "swap"
)

// RefreshError is the typed error a refresh returns. The current tree is
// kept on every failure; the controller turns this into view.refresh-failed.
type RefreshError struct {
	Step Step
	Err  error
}

func (e *RefreshError) Error() string { return "view refresh " + string(e.Step) + ": " + e.Err.Error() }
func (e *RefreshError) Unwrap() error { return e.Err }

// Git is the part of git a view needs, so a test can stand in for it.
type Git interface {
	// Fetch brings the remote's ref into the bare mirror, creating it first.
	Fetch(ctx context.Context, mirror, remote, ref string) error
	// Resolve returns the full commit id the mirror holds for ref.
	Resolve(ctx context.Context, mirror, ref string) (string, error)
	// Archive extracts the tree of commit sha into the existing directory dest.
	Archive(ctx context.Context, mirror, sha, dest string) error
}

// Result reports what a refresh did.
type Result struct {
	// Commit is the commit cur now names.
	Commit string
	// Previous is the commit cur named before, empty for the first build.
	Previous string
	// Changed is false when the ref had not moved.
	Changed bool
}

// Builder owns one view's directory: <Dir>/mirror.git, <Dir>/trees/<sha>
// and <Dir>/cur.
type Builder struct {
	Dir    string
	Remote string
	Ref    string
	Git    Git

	// rename is the swap seam; nil means os.Rename.
	rename func(oldpath, newpath string) error
}

var errNotImplemented = errors.New("not implemented")

// New returns a Builder for the view directory dir.
func New(dir, remote, ref string, git Git) *Builder {
	return &Builder{Dir: dir, Remote: remote, Ref: ref, Git: git}
}

// Refresh follows Ref once.
func (b *Builder) Refresh(ctx context.Context) (Result, error) {
	return Result{}, &RefreshError{Step: StepFetch, Err: errNotImplemented}
}

// ExecGit runs the git binary.
type ExecGit struct{}

func (ExecGit) Fetch(ctx context.Context, mirror, remote, ref string) error {
	return errNotImplemented
}

func (ExecGit) Resolve(ctx context.Context, mirror, ref string) (string, error) {
	return "", errNotImplemented
}

func (ExecGit) Archive(ctx context.Context, mirror, sha, dest string) error {
	return errNotImplemented
}

// cleanGitEnv is the process environment without the variables that point git
// at a repository other than the one it is told to use, and with prompting
// off so a missing credential fails instead of waiting.
func cleanGitEnv() []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GIT_DIR="),
			strings.HasPrefix(kv, "GIT_WORK_TREE="),
			strings.HasPrefix(kv, "GIT_INDEX_FILE="),
			strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY="),
			strings.HasPrefix(kv, "GIT_COMMON_DIR="):
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0")
}
