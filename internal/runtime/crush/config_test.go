package crush

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/gittest"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func inspect(t *testing.T, cwd, boundary string, approved map[string]string) Verdict {
	t.Helper()
	v, err := InspectConfig(cwd, boundary, approved)
	if err != nil {
		t.Fatalf("InspectConfig: %v", err)
	}
	return v
}

func refusal(t *testing.T, v Verdict, rel string) Reason {
	t.Helper()
	for _, f := range v.Refused() {
		if f.Rel == rel {
			return f.Reason
		}
	}
	t.Fatalf("no refusal for %q in %+v", rel, v)
	return ""
}

func TestAWorkspaceWithNoConfigIsAdmitted(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), "package main")
	v := inspect(t, dir, dir, nil)
	if !v.OK() || len(v.Files) != 0 {
		t.Fatalf("verdict = %+v, want OK with no files", v)
	}
}

func TestEveryConfigNameIsRefusedUntilApproved(t *testing.T) {
	for _, name := range ConfigNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, name), "{}")
			v := inspect(t, dir, dir, nil)
			if v.OK() {
				t.Fatalf("%s was admitted with no approval", name)
			}
			if got := refusal(t, v, name); got != ReasonUnapproved {
				t.Fatalf("reason = %q, want %q", got, ReasonUnapproved)
			}
			approved := map[string]string{name: sum("{}")}
			if v := inspect(t, dir, dir, approved); !v.OK() {
				t.Fatalf("%s refused although approved: %+v", name, v)
			}
		})
	}
}

func TestAnApprovedFileThatChangedIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".crushrc"), "echo changed")
	v := inspect(t, dir, dir, map[string]string{".crushrc": sum("echo original")})
	if got := refusal(t, v, ".crushrc"); got != ReasonChanged {
		t.Fatalf("reason = %q, want %q", got, ReasonChanged)
	}
}

func TestTheApprovedHashIsComparedWithoutRegardToCase(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "crush.json"), "{}")
	upper := []byte(sum("{}"))
	for i, c := range upper {
		if c >= 'a' && c <= 'f' {
			upper[i] = c - 32
		}
	}
	if v := inspect(t, dir, dir, map[string]string{"crush.json": string(upper)}); !v.OK() {
		t.Fatalf("an upper-case digest was refused: %+v", v)
	}
}

// APFS and other case-insensitive filesystems load a config file whose name
// differs from Crush's only in case, and Unicode case folding also maps the
// long s (U+017F) onto s. A scan that compares exact strings misses both.
func TestSpellingVariantsOfAConfigNameAreFound(t *testing.T) {
	for _, name := range []string{".CRUSHRC", "Crush.JSON", ".Crush.Json", "cruſh.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, name), "{}")
			v := inspect(t, dir, dir, nil)
			if v.OK() {
				t.Fatalf("%q was admitted", name)
			}
			if got := refusal(t, v, name); got != ReasonUnapproved {
				t.Fatalf("reason = %q", got)
			}
		})
	}
}

func TestNamesThatOnlyResembleAConfigNameAreIgnored(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"crush.json.bak", "mycrush.json", "crush.yaml", "crushrc.sh", "crush"} {
		write(t, filepath.Join(dir, name), "x")
	}
	if v := inspect(t, dir, dir, nil); !v.OK() || len(v.Files) != 0 {
		t.Fatalf("verdict = %+v, want no config files found", v)
	}
}

func TestASymlinkedConfigIsRefusedEvenWhenItsTargetIsApproved(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "real.json"), "{}")
	if err := os.Symlink(filepath.Join(outside, "real.json"), filepath.Join(dir, "crush.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	v := inspect(t, dir, dir, map[string]string{"crush.json": sum("{}")})
	if got := refusal(t, v, "crush.json"); got != ReasonSymlink {
		t.Fatalf("reason = %q, want %q", got, ReasonSymlink)
	}
	for _, f := range v.Files {
		if f.SHA256 != "" {
			t.Fatalf("a symlinked config was read: %+v", f)
		}
	}
}

func TestADanglingSymlinkedConfigIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, ".crushrc")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	v := inspect(t, dir, dir, nil)
	if got := refusal(t, v, ".crushrc"); got != ReasonSymlink {
		t.Fatalf("reason = %q, want %q", got, ReasonSymlink)
	}
}

func TestADirectoryWithAConfigNameIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "crush.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := inspect(t, dir, dir, nil)
	if got := refusal(t, v, "crush.json"); got != ReasonNotRegular {
		t.Fatalf("reason = %q, want %q", got, ReasonNotRegular)
	}
}

func TestTheWalkCoversEachDirectoryUpToTheBoundaryAndNoFurther(t *testing.T) {
	root := t.TempDir()
	boundary := filepath.Join(root, "repo")
	cwd := filepath.Join(boundary, "a", "b")
	write(t, filepath.Join(root, "crush.json"), "{}")               // above the boundary
	write(t, filepath.Join(boundary, ".crushrc"), "x")              // at the boundary
	write(t, filepath.Join(boundary, "a", "crush.json"), "{}")      // between
	write(t, filepath.Join(cwd, "crushrc"), "x")                    // at cwd
	write(t, filepath.Join(boundary, "a", "c", "crush.json"), "{}") // a sibling, never walked

	v := inspect(t, cwd, boundary, nil)
	got := map[string]bool{}
	for _, f := range v.Files {
		got[f.Rel] = true
	}
	for _, want := range []string{".crushrc", "a/crush.json", "a/b/crushrc"} {
		if !got[want] {
			t.Errorf("config %q was not found; found %v", want, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("found %v, want exactly the three files on the walk", got)
	}
}

func TestAnEmptyBoundaryScansTheWorkingDirectoryOnly(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "work")
	write(t, filepath.Join(root, ".crushrc"), "x")
	write(t, filepath.Join(cwd, "crush.json"), "{}")
	v := inspect(t, cwd, "", nil)
	if len(v.Files) != 1 || v.Files[0].Rel != "crush.json" {
		t.Fatalf("files = %+v, want only the working directory's crush.json", v.Files)
	}
}

func TestABoundaryReachedThroughASymlinkStillStopsTheWalk(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	write(t, filepath.Join(real, "proj", "crush.json"), "{}") // inside the boundary
	write(t, filepath.Join(root, "crush.json"), "{}")         // above the boundary
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// cwd is spelled through the link and the boundary through the real path.
	v := inspect(t, filepath.Join(link, "proj"), real, nil)
	if len(v.Files) != 1 || v.Files[0].Rel != "proj/crush.json" {
		t.Fatalf("files = %+v, want only proj/crush.json: the walk must stop at the boundary", v.Files)
	}
}

// The sha256 covers the entry file. An approved config that sources another
// file keeps its approval when that file changes. This pins the documented
// limit so a change to it is deliberate.
func TestTheHashCoversOnlyTheEntryFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".crushrc"), "source ./extra.sh")
	write(t, filepath.Join(dir, "extra.sh"), "echo first")
	approved := map[string]string{".crushrc": sum("source ./extra.sh")}
	if !inspect(t, dir, dir, approved).OK() {
		t.Fatal("approved config refused")
	}
	write(t, filepath.Join(dir, "extra.sh"), "echo something else")
	if !inspect(t, dir, dir, approved).OK() {
		t.Fatal("the guard now covers sourced files; update the package doc and this test together")
	}
}

func TestAnUnreadableConfigFailsTheScan(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "crush.json")
	write(t, p, "{}")
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	if _, err := InspectConfig(dir, dir, map[string]string{"crush.json": sum("{}")}); err == nil {
		t.Fatal("an unreadable config produced a verdict; the scan must fail closed")
	}
}

func TestAnAncestorThatCannotBeListedFailsTheScan(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists everything")
	}
	root := t.TempDir()
	mid := filepath.Join(root, "mid")
	cwd := filepath.Join(mid, "work")
	write(t, filepath.Join(mid, "crush.json"), "{}")
	write(t, filepath.Join(cwd, "main.go"), "package main")
	// Search without read: os.Stat of crush.json by name still succeeds, as
	// Crush's probe does, but the directory cannot be listed.
	if err := os.Chmod(mid, 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(mid, 0o755) })
	if _, err := os.Stat(filepath.Join(mid, "crush.json")); err != nil {
		t.Fatalf("precondition: crush.json must be reachable by name: %v", err)
	}
	if _, err := InspectConfig(cwd, root, nil); err == nil {
		t.Fatal("an ancestor that cannot be listed produced a verdict; the scan must fail closed")
	}
}

func TestAScanOfAMissingDirectoryFails(t *testing.T) {
	if _, err := InspectConfig(filepath.Join(t.TempDir(), "gone"), "", nil); err == nil {
		t.Fatal("a missing working directory produced a verdict")
	}
}

func TestBoundaryIsTheGitRootAndFallsBackToTheWorkingDirectory(t *testing.T) {
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "repo"))
	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Boundary(sub)
	if err != nil {
		t.Fatalf("Boundary(repo subdir): %v", err)
	}
	if got != repo {
		t.Fatalf("Boundary(repo subdir) = %q, want %q", got, repo)
	}

	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err = Boundary(plain)
	if err != nil {
		t.Fatalf("Boundary(no repo): %v", err)
	}
	if got != plain {
		t.Fatalf("Boundary(no repo) = %q, want the directory itself %q", got, plain)
	}
}
