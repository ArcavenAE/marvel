package view

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// entry is one member of a crafted tar stream.
type entry struct {
	name string
	typ  byte
	body string
	link string
}

func craftTar(t *testing.T, entries ...entry) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Linkname: e.link, Size: int64(len(e.body))}
		if e.typ == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

// scratch returns a tree directory and a sibling directory that holds a
// sentinel, standing for anything outside the view.
func scratch(t *testing.T) (tree, outside string) {
	t.Helper()
	base := t.TempDir()
	tree = filepath.Join(base, "tree")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{tree, outside} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	return tree, outside
}

func assertOutsideUntouched(t *testing.T, outside string) {
	t.Helper()
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Errorf("outside the view holds %v after extraction, want only the sentinel", names(entries))
	}
	if got := mustRead(t, filepath.Join(outside, "sentinel")); got != "keep" {
		t.Errorf("sentinel = %q, want it unchanged", got)
	}
}

// A symlink that points out of the view, then a file written through it: the
// archive is refused with an error and nothing lands outside.
func TestExtractRefusesAFileWrittenThroughAnEscapingSymlink(t *testing.T) {
	tree, outside := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "link", typ: tar.TypeSymlink, link: outside},
		entry{name: "link/pwned", typ: tar.TypeReg, body: "x"},
	), tree)
	if err == nil {
		t.Fatal("extractTar accepted a file under a symlink that leaves the view")
	}
	assertOutsideUntouched(t, outside)
}

// The same with a relative target that climbs out of the tree.
func TestExtractRefusesARelativeSymlinkThatClimbsOut(t *testing.T) {
	tree, outside := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "d", typ: tar.TypeDir},
		entry{name: "d/link", typ: tar.TypeSymlink, link: "../../outside"},
		entry{name: "d/link/pwned", typ: tar.TypeReg, body: "x"},
	), tree)
	if err == nil {
		t.Fatal("extractTar accepted a symlink that climbs out of the view")
	}
	assertOutsideUntouched(t, outside)
}

// The case-fold pair of CVE-2021-21300: a symlink and a directory whose names
// differ only by case. On a case-insensitive filesystem the directory entry
// lands on the symlink; on a case-sensitive one they are two names. The
// symlink leaves the view, so the archive is refused, and on either kind of
// filesystem nothing is written outside.
func TestExtractCaseFoldPairNeverWritesOutside(t *testing.T) {
	tree, outside := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "Foo", typ: tar.TypeSymlink, link: outside},
		entry{name: "foo", typ: tar.TypeDir},
		entry{name: "foo/pwned", typ: tar.TypeReg, body: "x"},
	), tree)
	if err == nil {
		t.Error("extractTar accepted a symlink that leaves the view")
	}
	assertOutsideUntouched(t, outside)
}

// An entry type the extractor does not handle is an error, not a silent skip.
func TestExtractRefusesAnUnsupportedEntryType(t *testing.T) {
	tree, _ := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "a", typ: tar.TypeReg, body: "x"},
		entry{name: "hard", typ: tar.TypeLink, link: "a"},
	), tree)
	if err == nil {
		t.Fatal("extractTar skipped a hard link without an error")
	}
}

// A symlink that stays inside the view is kept, so a repository that links
// within itself extracts whole.
func TestExtractKeepsASymlinkInsideTheTree(t *testing.T) {
	tree, _ := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "docs", typ: tar.TypeDir},
		entry{name: "docs/real.md", typ: tar.TypeReg, body: "hello"},
		entry{name: "docs/alias.md", typ: tar.TypeSymlink, link: "real.md"},
		entry{name: "top", typ: tar.TypeSymlink, link: "docs/real.md"},
	), tree)
	if err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	if got := mustRead(t, filepath.Join(tree, "top")); got != "hello" {
		t.Errorf("read through an inside symlink = %q, want hello", got)
	}
}

// A relative symlink that climbs out of a nested directory and still lands
// inside the tree extracts as a link and reads through to its target. This is
// the shape aae-orc tracks (.agents/skills/<name> -> ../../.claude/skills/<name>):
// a test on a "../" prefix alone would refuse it. The link entry comes before
// its target, so it is created dangling and resolves once the target exists.
func TestExtractKeepsANestedRelativeSymlinkThatResolvesInside(t *testing.T) {
	tree, _ := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: ".agents/", typ: tar.TypeDir},
		entry{name: ".agents/skills/", typ: tar.TypeDir},
		entry{name: ".agents/skills/review", typ: tar.TypeSymlink, link: "../../.claude/skills/review"},
		entry{name: ".claude/", typ: tar.TypeDir},
		entry{name: ".claude/skills/", typ: tar.TypeDir},
		entry{name: ".claude/skills/review/", typ: tar.TypeDir},
		entry{name: ".claude/skills/review/SKILL.md", typ: tar.TypeReg, body: "the skill"},
	), tree)
	if err != nil {
		t.Fatalf("extractTar refused a link that resolves inside the tree: %v", err)
	}
	link := filepath.Join(tree, ".agents", "skills", "review")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("the entry is not a symlink: %v", err)
	}
	if target != filepath.FromSlash("../../.claude/skills/review") {
		t.Errorf("link target = %q, want it kept as written", target)
	}
	if got := mustRead(t, filepath.Join(link, "SKILL.md")); got != "the skill" {
		t.Errorf("read through the link = %q, want the target's content", got)
	}
}

// One level further up than the tree has: refused, though it starts the same.
func TestExtractRefusesANestedRelativeSymlinkOneLevelTooHigh(t *testing.T) {
	tree, _ := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: ".agents/", typ: tar.TypeDir},
		entry{name: ".agents/skills/", typ: tar.TypeDir},
		entry{name: ".agents/skills/review", typ: tar.TypeSymlink, link: "../../../outside"},
	), tree)
	if err == nil {
		t.Fatal("extractTar accepted a link that climbs one level above the tree")
	}
}

// A link is judged by where it really sits. After a link to "." (what a
// case-fold collision leaves behind), "x/y" is the tree's own y, so a link at
// x/y/up with target ../.. resolves above the tree even though its name alone
// looks like it stays inside. An entry whose parent path passes through a
// symlink is refused.
func TestExtractRefusesAnEntryUnderASymlinkedParent(t *testing.T) {
	tree, outside := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "x", typ: tar.TypeSymlink, link: "."},
		entry{name: "x/y", typ: tar.TypeDir},
		entry{name: "x/y/up", typ: tar.TypeSymlink, link: "../.."},
	), tree)
	if err == nil {
		t.Fatal("extractTar accepted an entry whose parent is a symlink")
	}
	if up := filepath.Join(tree, "y", "up"); func() bool { _, e := os.Lstat(up); return e == nil }() {
		resolved, rerr := filepath.EvalSymlinks(up)
		if rerr == nil && !isInside(tree, resolved) {
			t.Errorf("%s resolves to %s, outside the tree", up, resolved)
		}
	}
	assertOutsideUntouched(t, outside)
}

// A regular file under a symlinked parent is refused too, even when the link
// stays inside the tree.
func TestExtractRefusesAFileUnderAnInsideSymlink(t *testing.T) {
	tree, _ := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "real", typ: tar.TypeDir},
		entry{name: "alias", typ: tar.TypeSymlink, link: "real"},
		entry{name: "alias/f", typ: tar.TypeReg, body: "x"},
	), tree)
	if err == nil {
		t.Fatal("extractTar wrote a file through a symlinked parent")
	}
}

// A directory entry that names an existing symlink (what a case-fold pair does
// on a case-insensitive filesystem) is refused, not adopted.
func TestExtractRefusesADirectoryEntryOverASymlink(t *testing.T) {
	tree, _ := scratch(t)
	err := extractTar(craftTar(t,
		entry{name: "real", typ: tar.TypeDir},
		entry{name: "alias", typ: tar.TypeSymlink, link: "real"},
		entry{name: "alias", typ: tar.TypeDir},
	), tree)
	if err == nil {
		t.Fatal("extractTar adopted a symlink as a directory")
	}
}

// isInside reports whether path is dir or below it, comparing resolved
// paths so a temp directory behind a link does not read as outside.
func isInside(dir, path string) bool {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(d, path)
	return err == nil && filepath.IsLocal(rel)
}
