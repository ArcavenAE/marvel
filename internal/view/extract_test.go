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
