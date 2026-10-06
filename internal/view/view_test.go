package view

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	shaOne = "1111111111111111111111111111111111111111"
	shaTwo = "2222222222222222222222222222222222222222"
)

// fakeGit stands in for git. Archive writes a small tree whose content
// names the commit, or fails after writing part of it.
type fakeGit struct {
	sha        string
	fetchErr   error
	archiveErr error
	archives   int
	// archiveFn, when set, replaces the default tree Archive writes.
	archiveFn func(dest string) error
}

func (g *fakeGit) Fetch(context.Context, string, string, string) error { return g.fetchErr }

func (g *fakeGit) Resolve(context.Context, string, string) (string, error) { return g.sha, nil }

func (g *fakeGit) Archive(_ context.Context, _, sha, dest string) error {
	g.archives++
	if g.archiveFn != nil {
		return g.archiveFn(dest)
	}
	if err := os.MkdirAll(filepath.Join(dest, "sub"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "sub", "file.txt"), []byte("tree "+sha), 0o644); err != nil {
		return err
	}
	return g.archiveErr
}

func newBuilder(t *testing.T, g Git) *Builder {
	t.Helper()
	b := New(filepath.Join(t.TempDir(), "views", "ws", "seat", "repo"), "remote", "main", g)
	// The trees are read-only; restore write so TempDir can remove them.
	t.Cleanup(func() { _ = forceRemove(b.Dir) })
	return b
}

func curTarget(t *testing.T, b *Builder) string {
	t.Helper()
	got, err := os.Readlink(filepath.Join(b.Dir, "cur"))
	if err != nil {
		t.Fatalf("readlink cur: %v", err)
	}
	return got
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func asRefreshError(t *testing.T, err error, step Step) {
	t.Helper()
	var re *RefreshError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want a *RefreshError", err)
	}
	if re.Step != step {
		t.Errorf("step = %q, want %q", re.Step, step)
	}
}

func TestRefreshBuildsTheFirstTree(t *testing.T) {
	g := &fakeGit{sha: shaOne}
	b := newBuilder(t, g)

	res, err := b.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !res.Changed || res.Commit != shaOne || res.Previous != "" {
		t.Errorf("result = %+v, want a first build of %s", res, shaOne)
	}
	if got := curTarget(t, b); got != filepath.Join("trees", shaOne) {
		t.Errorf("cur -> %q, want trees/%s", got, shaOne)
	}
	cur := filepath.Join(b.Dir, "cur")
	if got := strings.TrimSpace(mustRead(t, filepath.Join(cur, "VIEW_SHA"))); got != shaOne {
		t.Errorf("VIEW_SHA = %q, want the full commit id", got)
	}
	if got := mustRead(t, filepath.Join(cur, "sub", "file.txt")); got != "tree "+shaOne {
		t.Errorf("file = %q", got)
	}
}

func TestRefreshUnchangedRefIsANoOp(t *testing.T) {
	g := &fakeGit{sha: shaOne}
	b := newBuilder(t, g)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	res, err := b.Refresh(context.Background())
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if res.Changed || res.Commit != shaOne {
		t.Errorf("result = %+v, want unchanged at %s", res, shaOne)
	}
	if g.archives != 1 {
		t.Errorf("archives = %d, want 1: an unchanged ref must not extract again", g.archives)
	}
}

func TestRefreshExtractFailureRemovesThePartialTree(t *testing.T) {
	g := &fakeGit{sha: shaOne}
	b := newBuilder(t, g)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	g.sha, g.archiveErr = shaTwo, errors.New("disk full")
	_, err := b.Refresh(context.Background())
	asRefreshError(t, err, StepExtract)

	if got := curTarget(t, b); got != filepath.Join("trees", shaOne) {
		t.Errorf("cur -> %q after a failed extract, want the old tree", got)
	}
	entries, rerr := os.ReadDir(filepath.Join(b.Dir, "trees"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 1 || entries[0].Name() != shaOne {
		t.Errorf("trees = %v, want only %s: the partial tree must be gone", names(entries), shaOne)
	}
}

func TestRefreshSwapFailureKeepsCur(t *testing.T) {
	g := &fakeGit{sha: shaOne}
	b := newBuilder(t, g)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	g.sha = shaTwo
	b.rename = func(string, string) error { return errors.New("rename refused") }
	_, err := b.Refresh(context.Background())
	asRefreshError(t, err, StepSwap)

	if got := curTarget(t, b); got != filepath.Join("trees", shaOne) {
		t.Errorf("cur -> %q after a failed swap, want the old tree", got)
	}
	if _, serr := os.Lstat(filepath.Join(b.Dir, "trees", shaTwo)); !os.IsNotExist(serr) {
		t.Errorf("trees/%s still exists after a failed swap (err %v)", shaTwo, serr)
	}
	if _, serr := os.Lstat(filepath.Join(b.Dir, "cur.new")); !os.IsNotExist(serr) {
		t.Errorf("cur.new still exists after a failed swap (err %v)", serr)
	}
}

func TestRefreshFetchFailureKeepsTheCurrentTree(t *testing.T) {
	g := &fakeGit{sha: shaOne}
	b := newBuilder(t, g)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	g.sha, g.fetchErr = shaTwo, errors.New("network down")
	_, err := b.Refresh(context.Background())
	asRefreshError(t, err, StepFetch)
	if got := curTarget(t, b); got != filepath.Join("trees", shaOne) {
		t.Errorf("cur -> %q after a failed fetch, want the old tree", got)
	}
}

// A superseded tree stays readable: retention is a later step's decision.
func TestRefreshLeavesTheSupersededTreeReadable(t *testing.T) {
	g := &fakeGit{sha: shaOne}
	b := newBuilder(t, g)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.sha = shaTwo
	res, err := b.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Previous != shaOne || res.Commit != shaTwo {
		t.Errorf("result = %+v, want %s to %s", res, shaOne, shaTwo)
	}
	old := filepath.Join(b.Dir, "trees", shaOne, "sub", "file.txt")
	if got := mustRead(t, old); got != "tree "+shaOne {
		t.Errorf("superseded file = %q", got)
	}
}

func names(entries []os.DirEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// An archive that places VIEW_SHA as a symlink to a file outside the tree must
// not have that file written: VIEW_SHA is created through the tree's root with
// O_EXCL, so the link makes the build fail and the file outside is untouched.
func TestRefreshRefusesAVIEWSHASymlinkFromTheArchive(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "ESCAPED_VIEW_SHA")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &fakeGit{sha: shaOne, archiveFn: func(dest string) error {
		return os.Symlink(outside, filepath.Join(dest, "VIEW_SHA"))
	}}
	b := newBuilder(t, g)

	_, err := b.Refresh(context.Background())
	asRefreshError(t, err, StepExtract)
	if got := mustRead(t, outside); got != "keep" {
		t.Errorf("the file outside the tree holds %q, want it untouched", got)
	}
	if _, serr := os.Lstat(filepath.Join(b.Dir, "cur")); !os.IsNotExist(serr) {
		t.Errorf("cur exists after a refused build (err %v)", serr)
	}
}

// An archive that carries a VIEW_SHA of its own is refused: marvel writes that
// file, so a committed one would be silently replaced.
func TestRefreshRefusesAnArchiveThatCarriesAVIEWSHA(t *testing.T) {
	g := &fakeGit{sha: shaOne, archiveFn: func(dest string) error {
		return os.WriteFile(filepath.Join(dest, "VIEW_SHA"), []byte("not the commit\n"), 0o644)
	}}
	b := newBuilder(t, g)

	_, err := b.Refresh(context.Background())
	asRefreshError(t, err, StepExtract)
	if _, serr := os.Lstat(filepath.Join(b.Dir, "trees", shaOne)); !os.IsNotExist(serr) {
		t.Errorf("a tree was built from an archive with its own VIEW_SHA (err %v)", serr)
	}
}
