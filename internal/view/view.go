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
	info, err := os.Lstat(final)
	if err == nil && treeSealed(info) {
		// The ref came back to a commit whose tree was sealed. A sealed tree is
		// a skeleton, so it is built again rather than pointed at.
		if rerr := forceRemove(final); rerr != nil {
			return Result{}, &RefreshError{Step: StepExtract, Err: rerr}
		}
		err = os.ErrNotExist
	}
	if err != nil {
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
	if err := writeViewSHA(part, sha); err != nil {
		return err
	}
	if err := makeReadOnly(part); err != nil {
		return err
	}
	return os.Rename(part, final)
}

// writeViewSHA creates VIEW_SHA at the root of the tree, through an os.Root and
// with O_EXCL. The file is marvel's: an archive that carries one of its own, as
// a file or as a link, is refused, and a link can never carry the write out of
// the tree.
func writeViewSHA(tree, sha string) error {
	root, err := os.OpenRoot(tree)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(viewSHAName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write %s (an archive that carries its own is refused): %w", viewSHAName, err)
	}
	if _, err := f.WriteString(sha + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
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

// extractTar writes a tar stream under dest. Every write goes through an
// os.Root on dest, so no path, and no symlink the archive itself created, can
// lead a write out of the tree. An entry that cannot be written inside, a
// symlink whose target leaves the tree, and an entry type this code does not
// handle are errors: the archive is refused, never partly skipped.
func extractTar(r io.Reader, dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	// verified holds directories already shown to be real, not links.
	verified := map[string]bool{}
	// links holds every symlink written, by tree-relative name, for the
	// resolution pass once the whole archive is down.
	links := map[string]string{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return checkLinks(root, links)
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
		// A directory entry ends in a slash, which os.Root does not take.
		name = filepath.Clean(name)
		// git never stores an entry under a symlink, so an archive that does
		// is hostile: a later link judged by its name would resolve somewhere
		// else once a parent is a link (a case-fold collision leaves one).
		if err := refuseLinkedParent(root, name, verified); err != nil {
			return fmt.Errorf("archive entry %q: %w", hdr.Name, err)
		}
		mode := os.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			// A directory entry that lands on an existing link (a case-fold
			// pair on a case-insensitive filesystem) is not adopted.
			if fi, lerr := root.Lstat(name); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
				return fmt.Errorf("archive directory %q is an existing symlink", hdr.Name)
			}
			if err := root.MkdirAll(name, 0o700|mode); err != nil {
				return fmt.Errorf("archive directory %q: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			if err := writeRegular(root, name, mode, tr); err != nil {
				return fmt.Errorf("archive file %q: %w", hdr.Name, err)
			}
		case tar.TypeSymlink:
			if !symlinkStaysInside(name, hdr.Linkname) {
				return fmt.Errorf("archive symlink %q points to %q, outside the tree", hdr.Name, hdr.Linkname)
			}
			if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
				return fmt.Errorf("archive symlink %q: %w", hdr.Name, err)
			}
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return fmt.Errorf("archive symlink %q: %w", hdr.Name, err)
			}
			links[name] = filepath.FromSlash(hdr.Linkname)
		default:
			return fmt.Errorf("archive entry %q has unsupported type %q", hdr.Name, hdr.Typeflag)
		}
	}
}

// refuseLinkedParent returns an error when any directory on the way to name is
// a symlink. A missing parent ends the walk: nothing below it exists, and
// MkdirAll will create it as a real directory.
func refuseLinkedParent(root *os.Root, name string, verified map[string]bool) error {
	dir := filepath.Dir(name)
	if dir == "." {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if verified[cur] {
			continue
		}
		fi, err := root.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("parent %q is a symlink", cur)
		}
		verified[cur] = true
	}
	return nil
}

// writeRegular creates one file inside root with the archive's content.
func writeRegular(root *os.Root, name string, mode os.FileMode, content io.Reader) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600|mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// symlinkStaysInside reports whether a symlink at name with the given target
// resolves to a path inside the tree, judged by the names alone. An absolute
// target never does.
func symlinkStaysInside(name, target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return false
	}
	return filepath.IsLocal(filepath.Join(filepath.Dir(name), filepath.FromSlash(target)))
}

// maxLinkHops bounds how many links one resolution may follow, as the kernel
// does, so a cycle ends in an error.
const maxLinkHops = 40

// resolveLinks follows every symlink in links component by component, the way
// a reader would, and returns an error for one that ends outside the tree, is
// absolute, or does not resolve within maxLinkHops. The name check done as
// each link was written is lexical and cannot see a target that passes
// through another link ("x/.." with x -> "."), and that depends on the order
// the entries arrived in. Running after the last entry makes the verdict
// independent of that order.
func resolveLinks(links map[string]string) error {
	for name := range links {
		if err := resolveLink(links, name); err != nil {
			return fmt.Errorf("archive symlink %q: %w", name, err)
		}
	}
	return nil
}

// checkLinks refuses the links the archive wrote that could leave the tree.
// resolveLinks reads the archive's names byte for byte; the filesystem may not
// (APFS folds case and treats NFC and NFD as one name), so each link is then
// handed to the root itself, which resolves it the way a reader on this
// volume would and reports a path that escapes. A link to nothing is not an
// escape and stays; any other failure refuses the archive.
func checkLinks(root *os.Root, links map[string]string) error {
	if err := resolveLinks(links); err != nil {
		return err
	}
	for name := range links {
		if _, err := root.Stat(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("archive symlink %q: %w", name, err)
		}
	}
	return nil
}

func resolveLink(links map[string]string, name string) error {
	var cur []string
	if dir := filepath.Dir(name); dir != "." {
		cur = strings.Split(dir, string(filepath.Separator))
	}
	pending := strings.Split(links[name], string(filepath.Separator))
	hops := 0
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(cur) == 0 {
				return fmt.Errorf("target %q resolves outside the tree", links[name])
			}
			cur = cur[:len(cur)-1]
			continue
		}
		cur = append(cur, c)
		target, isLink := links[strings.Join(cur, string(filepath.Separator))]
		if !isLink {
			continue
		}
		hops++
		if hops > maxLinkHops {
			return fmt.Errorf("target %q does not resolve within %d links", links[name], maxLinkHops)
		}
		if target == "" || filepath.IsAbs(target) {
			return fmt.Errorf("target %q passes through a link to %q", links[name], target)
		}
		cur = cur[:len(cur)-1]
		pending = append(strings.Split(target, string(filepath.Separator)), pending...)
	}
	return nil
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

// Seal hollows the superseded tree trees/<sha>: every file is deleted, the
// directories stay, and each directory is set to mode 000, deepest first. A
// seat still holding a working directory there gets an error on every read
// instead of an empty answer. It refuses the tree cur names and anything that
// is not a commit id, and a tree that is already sealed or gone is not an
// error, so a retry or a restart may repeat it.
func (b *Builder) Seal(sha string) error {
	if !validCommitID(sha) {
		return fmt.Errorf("seal: %q is not a full commit id", sha)
	}
	if b.current() == sha {
		return fmt.Errorf("seal: %s is the tree cur names", sha)
	}
	tree := filepath.Join(b.Dir, treesName, sha)
	info, err := os.Lstat(tree)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("seal %s: %w", sha, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("seal: %s is not a directory", tree)
	}
	if treeSealed(info) {
		return nil
	}
	return sealTree(tree)
}

// treeSealed reports whether a tree's root is in the sealed state. The root is
// the last directory sealTree closes, so mode 000 means the whole tree is done.
func treeSealed(root fs.FileInfo) bool { return root.Mode().Perm() == 0 }

// sealTree deletes every file and symlink under root, then closes each
// directory, deepest first. Directories are opened top down first because the
// tree is read-only, and the closing order keeps a parent traversable while its
// children are closed.
func sealTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return os.Chmod(p, 0o700)
		}
		return os.Remove(p)
	})
	if err != nil {
		return fmt.Errorf("seal %s: %w", root, err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], 0); err != nil {
			return fmt.Errorf("seal %s: %w", root, err)
		}
	}
	return nil
}
