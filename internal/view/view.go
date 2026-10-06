// Package view builds and swaps a per-seat read-only view of a repository's
// default branch (docs/design/readonly-view.md, marvel#609). It has no
// controller wiring: a caller hands it a directory, a remote and a ref, and
// it keeps a bare mirror, one extracted tree per commit, and a cur symlink
// that moves with one rename.
package view

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// New returns a Builder for the view directory dir.
func New(dir, remote, ref string, git Git) *Builder {
	return &Builder{Dir: dir, Remote: remote, Ref: ref, Git: git}
}

const (
	mirrorName  = "mirror.git"
	treesName   = "trees"
	curName     = "cur"
	curNewName  = "cur.new"
	partSuffix  = ".part"
	viewSHAName = "VIEW_SHA"
)

// Refresh follows Ref once: fetch into the mirror, resolve the ref, and when
// the commit differs from the one cur names, extract it, make it read-only
// and swap cur onto it with one rename. Any failure leaves cur on the tree it
// named before and removes what this call began. The superseded tree is left
// in place; retaining and sealing it is the caller's concern.
func (b *Builder) Refresh(ctx context.Context) (Result, error) {
	mirror := filepath.Join(b.Dir, mirrorName)
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return Result{}, &RefreshError{Step: StepFetch, Err: err}
	}
	if err := b.Git.Fetch(ctx, mirror, b.Remote, b.Ref); err != nil {
		return Result{}, &RefreshError{Step: StepFetch, Err: err}
	}
	sha, err := b.Git.Resolve(ctx, mirror, b.Ref)
	if err != nil {
		return Result{}, &RefreshError{Step: StepResolve, Err: err}
	}
	if !validCommitID(sha) {
		return Result{}, &RefreshError{Step: StepResolve, Err: fmt.Errorf("ref %q resolved to %q, not a full commit id", b.Ref, sha)}
	}

	prev := b.current()
	if prev == sha {
		return Result{Commit: sha, Previous: prev}, nil
	}

	trees := filepath.Join(b.Dir, treesName)
	final := filepath.Join(trees, sha)
	built := false
	if _, err := os.Lstat(final); err != nil {
		if err := b.extract(ctx, mirror, sha, trees, final); err != nil {
			return Result{}, &RefreshError{Step: StepExtract, Err: err}
		}
		built = true
	}
	if err := b.swap(sha); err != nil {
		if built {
			_ = forceRemove(final)
		}
		return Result{}, &RefreshError{Step: StepSwap, Err: err}
	}
	return Result{Commit: sha, Previous: prev, Changed: true}, nil
}

// current returns the commit cur names, or "" when cur is absent or not a
// link into trees.
func (b *Builder) current() string {
	target, err := os.Readlink(filepath.Join(b.Dir, curName))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// extract builds trees/<sha> through a .part directory renamed into place
// last, so a crash or failure never leaves a half-built tree under its final
// name.
func (b *Builder) extract(ctx context.Context, mirror, sha, trees, final string) (err error) {
	if err := os.MkdirAll(trees, 0o700); err != nil {
		return err
	}
	part := final + partSuffix
	if err := forceRemove(part); err != nil {
		return err
	}
	if err := os.Mkdir(part, 0o700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = forceRemove(part)
		}
	}()
	if err := b.Git.Archive(ctx, mirror, sha, part); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(part, viewSHAName), []byte(sha+"\n"), 0o600); err != nil {
		return err
	}
	if err := makeReadOnly(part); err != nil {
		return err
	}
	return os.Rename(part, final)
}

// swap points cur at trees/<sha> by writing cur.new and renaming it over cur.
func (b *Builder) swap(sha string) error {
	curNew := filepath.Join(b.Dir, curNewName)
	if err := os.Remove(curNew); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(filepath.Join(treesName, sha), curNew); err != nil {
		return err
	}
	rename := b.rename
	if rename == nil {
		rename = os.Rename
	}
	if err := rename(curNew, filepath.Join(b.Dir, curName)); err != nil {
		_ = os.Remove(curNew)
		return err
	}
	return nil
}

// validCommitID reports whether s is a full 40 or 64 hex digit object id.
func validCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// makeReadOnly removes every write bit under root, files first and each
// directory after its contents. A read-only file in a writable directory can
// be replaced, so the directories lose write too.
func makeReadOnly(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			dirs = append(dirs, path)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()&^0o222)
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		info, err := os.Stat(dirs[i])
		if err != nil {
			return err
		}
		if err := os.Chmod(dirs[i], info.Mode().Perm()&^0o222); err != nil {
			return err
		}
	}
	return nil
}

// forceRemove deletes path, restoring owner write on directories first
// because a view tree is read-only. A missing path is not an error.
func forceRemove(path string) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// ExecGit runs the git binary.
type ExecGit struct{}

// viewRef is where the mirror keeps the followed branch.
const viewRef = "refs/marvel/view"

func (ExecGit) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = cleanGitEnv()
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(ee.Stderr)))
		}
		return out, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

// Fetch creates the bare mirror on first use and fetches the branch ref into
// it. The branch is forced, so a rewritten remote branch still follows.
func (g ExecGit) Fetch(ctx context.Context, mirror, remote, ref string) error {
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err != nil {
		if _, err := g.run(ctx, "init", "--bare", "-q", mirror); err != nil {
			return err
		}
	}
	_, err := g.run(ctx, "--git-dir", mirror, "fetch", "-q", "--no-tags", remote, "+refs/heads/"+ref+":"+viewRef)
	return err
}

// Resolve returns the full commit id the mirror holds for the followed branch.
func (g ExecGit) Resolve(ctx context.Context, mirror, ref string) (string, error) {
	out, err := g.run(ctx, "--git-dir", mirror, "rev-parse", "--verify", viewRef+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Archive streams `git archive` of the commit and extracts it into dest.
func (ExecGit) Archive(ctx context.Context, mirror, sha, dest string) error {
	cmd := exec.CommandContext(ctx, "git", "--git-dir", mirror, "archive", "--format=tar", sha)
	cmd.Env = cleanGitEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	extractErr := extractTar(stdout, dest)
	if extractErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return extractErr
}

// extractTar writes a tar stream under dest. Entries that would land outside
// dest are refused.
func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("archive entry %q is outside the tree", hdr.Name)
		}
		path := filepath.Join(dest, name)
		mode := os.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700|mode); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600|mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, path); err != nil {
				return err
			}
		}
	}
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
