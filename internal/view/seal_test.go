package view

import (
	"bufio"
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// nestedTree writes a tree with a file at the root, a directory and a file one
// level down, and another two levels down.
func nestedTree(dest string) error {
	if err := os.MkdirAll(filepath.Join(dest, "a", "b"), 0o755); err != nil {
		return err
	}
	for name, body := range map[string]string{"top.txt": "top", "a/mid.txt": "mid", "a/b/low.txt": "low"} {
		if err := os.WriteFile(filepath.Join(dest, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// twoTrees builds shaOne then moves cur to shaTwo, leaving shaOne superseded.
func twoTrees(t *testing.T) (*Builder, *fakeGit) {
	t.Helper()
	g := &fakeGit{sha: shaOne, archiveFn: nestedTree}
	b := newBuilder(t, g)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.sha = shaTwo
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b, g
}

// skeleton reports what is left of a tree: its directories and files, and the
// mode each directory had before this call restored access to read it. It
// restores owner access top down, as teardown does, so the caller must not
// expect the tree to stay sealed.
func skeleton(t *testing.T, root string) (dirs map[string]fs.FileMode, files []string) {
	t.Helper()
	dirs = map[string]fs.FileMode{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			dirs[rel] = info.Mode().Perm()
			return os.Chmod(p, 0o700)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return dirs, files
}

// A sealed tree has lost every file and kept every directory, each at mode 000.
func TestSealHollowsATreeAndKeepsItsDirectories(t *testing.T) {
	b, _ := twoTrees(t)
	if err := b.Seal(shaOne); err != nil {
		t.Fatalf("seal: %v", err)
	}
	dirs, files := skeleton(t, filepath.Join(b.Dir, "trees", shaOne))
	if len(files) != 0 {
		t.Errorf("files left in a sealed tree: %v", files)
	}
	for _, d := range []string{".", "a", filepath.Join("a", "b")} {
		mode, ok := dirs[d]
		if !ok {
			t.Errorf("directory %q was removed; the skeleton keeps it", d)
		} else if mode != 0 {
			t.Errorf("directory %q has mode %o, want 000", d, mode)
		}
	}
}

// The tree cur names is the seat's current view and is never sealed.
func TestSealRefusesTheTreeCurNames(t *testing.T) {
	b, _ := twoTrees(t)
	if err := b.Seal(shaTwo); err == nil {
		t.Fatal("sealing the current tree succeeded")
	}
	if got := mustRead(t, filepath.Join(b.Dir, "cur", "a", "mid.txt")); got != "mid" {
		t.Errorf("the current tree changed: %q", got)
	}
}

// A name that is not a full commit id is refused before it touches a path.
func TestSealRefusesANameThatIsNotACommit(t *testing.T) {
	b, _ := twoTrees(t)
	for _, name := range []string{"", "..", "../mirror.git", shaOne[:12], strings.Repeat("A", 40), "cur"} {
		if err := b.Seal(name); err == nil {
			t.Errorf("Seal(%q) succeeded", name)
		}
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "trees", shaOne, "top.txt")); err != nil {
		t.Errorf("a refused seal touched the tree: %v", err)
	}
}

// Sealing a sealed tree, or one that is gone, is not an error: the controller
// retries on a tick and a restart may repeat it.
func TestSealIsIdempotent(t *testing.T) {
	b, _ := twoTrees(t)
	for i := 0; i < 2; i++ {
		if err := b.Seal(shaOne); err != nil {
			t.Fatalf("seal %d: %v", i+1, err)
		}
	}
	if err := b.Seal(strings.Repeat("3", 40)); err != nil {
		t.Errorf("sealing a tree that does not exist: %v", err)
	}
}

// If the ref returns to a commit whose tree is sealed, cur must not point at a
// hollow tree: the tree is built again.
func TestRefreshBackOntoASealedTreeBuildsItAgain(t *testing.T) {
	b, g := twoTrees(t)
	if err := b.Seal(shaOne); err != nil {
		t.Fatal(err)
	}
	g.sha = shaOne
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh back: %v", err)
	}
	if got := mustRead(t, filepath.Join(b.Dir, "cur", "a", "b", "low.txt")); got != "low" {
		t.Errorf("cur names a hollow tree: %q", got)
	}
}

// The held-directory contract: after the seal, a shell that was already in the
// tree root, or two levels down, gets an error from ls, from cat of a file it
// knew, and from find. These run the binaries, not a shell wrapper, which is
// what made find exit 0 in the macOS measurement. The same commands exit 0
// before the seal, as the positive control.
func TestSealedTreeRefusesAHeldDirectory(t *testing.T) {
	b, _ := twoTrees(t)
	root := filepath.Join(b.Dir, "trees", shaOne)
	cases := []struct{ name, dir, file string }{
		{"tree root", root, "./top.txt"},
		{"two levels down", filepath.Join(root, "a", "b"), "./low.txt"},
	}
	script := func(file string) string {
		return "ls . >/dev/null 2>&1; echo ls=$?\ncat " + file + " >/dev/null 2>&1; echo cat=$?\nfind . >/dev/null 2>&1; echo find=$?\n"
	}
	type shell struct {
		in  io.WriteCloser
		out *bufio.Reader
	}
	var shells []shell
	for _, c := range cases {
		cmd := exec.Command("sh")
		cmd.Dir = c.dir
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
		sh := shell{in, bufio.NewReader(out)}
		shells = append(shells, sh)

		// Positive control: before the seal the same three commands succeed.
		if _, err := io.WriteString(in, script(c.file)+"echo done\n"); err != nil {
			t.Fatal(err)
		}
		if got := readUntilDone(t, sh.out); got != "ls=0 cat=0 find=0" {
			t.Fatalf("%s: control before the seal = %q, want all three to exit 0", c.name, got)
		}
	}
	if err := b.Seal(shaOne); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if _, err := io.WriteString(shells[i].in, script(c.file)+"echo done\n"); err != nil {
			t.Fatal(err)
		}
		got := readUntilDone(t, shells[i].out)
		for _, cmd := range []string{"ls", "cat", "find"} {
			if strings.Contains(got, cmd+"=0") {
				t.Errorf("%s: %s exited 0 in a sealed directory (%q)", c.name, cmd, got)
			}
		}
	}
}

// readUntilDone collects the shell's lines up to its "done" marker, joined by
// spaces.
func readUntilDone(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var lines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read shell: %v (got %v)", err, lines)
		}
		line = strings.TrimSpace(line)
		if line == "done" {
			return strings.Join(lines, " ")
		}
		lines = append(lines, line)
	}
}

// partialSeal leaves a tree as a seal that died mid-walk leaves it: the
// directories opened for deletion and some files gone, nothing closed yet. It
// repeats what sealTree does first, so the test holds if that order is kept.
func partialSeal(t *testing.T, tree string) {
	t.Helper()
	for _, d := range []string{tree, filepath.Join(tree, "a"), filepath.Join(tree, "a", "b")} {
		if err := os.Chmod(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(tree, "a", "b", "low.txt")); err != nil {
		t.Fatal(err)
	}
}

// A seal that was interrupted must not be mistaken for a built tree: if the ref
// returns to that commit before the seal is retried, the refresh builds the tree
// again and cur never names one with files missing.
func TestRefreshBackOntoAPartlySealedTreeBuildsItAgain(t *testing.T) {
	b, g := twoTrees(t)
	partialSeal(t, filepath.Join(b.Dir, "trees", shaOne))

	g.sha = shaOne
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh back: %v", err)
	}
	if got := mustRead(t, filepath.Join(b.Dir, "cur", "a", "b", "low.txt")); got != "low" {
		t.Errorf("cur names a tree with a file missing: %q", got)
	}
	info, err := os.Stat(filepath.Join(b.Dir, "cur"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Errorf("the rebuilt tree is writable: %v", info.Mode())
	}
}

// Retrying an interrupted seal finishes it.
func TestSealFinishesAnInterruptedSeal(t *testing.T) {
	b, _ := twoTrees(t)
	partialSeal(t, filepath.Join(b.Dir, "trees", shaOne))
	if err := b.Seal(shaOne); err != nil {
		t.Fatalf("seal: %v", err)
	}
	dirs, files := skeleton(t, filepath.Join(b.Dir, "trees", shaOne))
	if len(files) != 0 {
		t.Errorf("files left after the retried seal: %v", files)
	}
	for d, mode := range dirs {
		if mode != 0 {
			t.Errorf("directory %q has mode %o after the retried seal, want 000", d, mode)
		}
	}
}

// A tree name that is a symlink is not followed: the seal refuses it and the
// directory it points at is untouched.
func TestSealDoesNotFollowASymlinkedTree(t *testing.T) {
	b, _ := twoTrees(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := strings.Repeat("4", 40)
	if err := os.Symlink(outside, filepath.Join(b.Dir, "trees", other)); err != nil {
		t.Fatal(err)
	}
	if err := b.Seal(other); err == nil {
		t.Error("sealing a symlinked tree succeeded")
	}
	if got := mustRead(t, filepath.Join(outside, "keep.txt")); got != "keep" {
		t.Errorf("the directory behind the link changed: %q", got)
	}
}

// A symlink inside a tree is removed like a file and never followed. The
// extractor only lets a relative link that stays inside the tree through, but the
// seal must not depend on that: a link to a directory elsewhere is deleted as a
// link, and the directory behind it keeps its mode and its files.
func TestSealRemovesASymlinkInsideATreeWithoutFollowingIt(t *testing.T) {
	outside := t.TempDir()
	// A seal that followed the link would leave this directory at mode 000, and
	// TempDir could not remove it. Registered after TempDir, so it runs first.
	t.Cleanup(func() { _ = os.Chmod(outside, 0o700) })
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &fakeGit{sha: shaOne, archiveFn: func(dest string) error {
		if err := nestedTree(dest); err != nil {
			return err
		}
		if err := os.Symlink(outside, filepath.Join(dest, "out")); err != nil {
			return err
		}
		return os.Symlink("a", filepath.Join(dest, "inner"))
	}}
	b := newBuilder(t, g)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.sha = shaTwo
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := b.Seal(shaOne); err != nil {
		t.Fatalf("seal: %v", err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 && info.Mode().Perm() != 0o755 {
		t.Errorf("the directory behind the link has mode %o after the seal", info.Mode().Perm())
	}
	if got := mustRead(t, filepath.Join(outside, "keep.txt")); got != "keep" {
		t.Errorf("a file behind the link changed: %q", got)
	}
	dirs, files := skeleton(t, filepath.Join(b.Dir, "trees", shaOne))
	if len(files) != 0 {
		t.Errorf("files and links left in the sealed tree: %v", files)
	}
	if _, ok := dirs["a"]; !ok {
		t.Error("the directory the in-tree link pointed at was removed")
	}
}
