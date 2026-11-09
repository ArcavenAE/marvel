package company

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv is the Env the tests hand the resolver and Check: roots in a set
// order, tools and vars as maps, and a running version.
type fakeEnv struct {
	roots    []string
	rootsErr error
	tools    map[string]string
	vars     map[string]string
	version  string
}

func (f *fakeEnv) Roots() ([]string, error) { return f.roots, f.rootsErr }
func (f *fakeEnv) Tool(n string) (string, bool) {
	p, ok := f.tools[n]
	return p, ok
}

func (f *fakeEnv) Var(n string) (string, bool) {
	v, ok := f.vars[n]
	return v, ok
}
func (f *fakeEnv) MarvelVersion() string { return f.version }

// installVersion writes <root>/<name>/<version>/ with the given files.
func installVersion(t *testing.T, root, name, version string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name, version)
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func setCurrent(t *testing.T, root, target string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(root, "x", "current")); err != nil {
		t.Fatal(err)
	}
}

const stubToml = "name = \"x\"\n"

func exactPin(name, version string) Pin {
	return Pin{Team: "core", Key: "company", Name: name, Version: version}
}

func namePin() Pin { return Pin{Team: "core", Key: "company", Name: "x"} }

func rootsSuffix(roots ...string) string {
	if len(roots) == 0 {
		return "(roots read: none)"
	}
	q := make([]string, len(roots))
	for i, r := range roots {
		q[i] = quoteField(r, false)
	}
	return "(roots read: " + strings.Join(q, ", ") + ")"
}

func TestResolveFindsAnExactVersion(t *testing.T) {
	root := t.TempDir()
	dir := installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
	lib, err := Resolve(&fakeEnv{roots: []string{root}}, exactPin("x", "1"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if lib.Name != "x" || lib.Version != "1" || lib.Root != root || lib.Dir != dir || string(lib.Data) != stubToml {
		t.Fatalf("library = %+v, want x@1 in %s read from %s", lib, root, dir)
	}
}

func TestResolveRefusesAnAbsentExactVersion(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	installVersion(t, a, "x", "1", map[string]string{"company.toml": stubToml})
	// x is held, at another version: still not installed at 2, and no fallback.
	_, err := Resolve(&fakeEnv{roots: []string{a, b}}, exactPin("x", "2"))
	wantRefusal(t, err, "2", "team core: company pin x@2 is not installed in any root; marvel does not fall back to current "+rootsSuffix(a, b))
}

func TestAbsentPinRefusalNamesTheRootsRead(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	cases := []struct {
		name  string
		pin   Pin
		roots []string
		row   string
		msg   string
	}{
		{"exact, two roots", exactPin("x", "1"), []string{a, b}, "2", "team core: company pin x@1 is not installed in any root; marvel does not fall back to current " + rootsSuffix(a, b)},
		{"exact, one root", exactPin("x", "1"), []string{b}, "2", "team core: company pin x@1 is not installed in any root; marvel does not fall back to current " + rootsSuffix(b)},
		{"exact, none", exactPin("x", "1"), nil, "2", "team core: company pin x@1 is not installed in any root; marvel does not fall back to current (roots read: none)"},
		{"name only, two roots", namePin(), []string{a, b}, "N8", "team core: company pin x is not installed in any root; marvel does not fall back " + rootsSuffix(a, b)},
		{"name only, none", namePin(), nil, "N8", "team core: company pin x is not installed in any root; marvel does not fall back (roots read: none)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Resolve(&fakeEnv{roots: c.roots}, c.pin)
			wantRefusal(t, err, c.row, c.msg)
		})
	}
	t.Run("a root that does not exist is read and named", func(t *testing.T) {
		gone := filepath.Join(a, "gone")
		_, err := Resolve(&fakeEnv{roots: []string{gone}}, exactPin("x", "1"))
		wantRefusal(t, err, "2", "team core: company pin x@1 is not installed in any root; marvel does not fall back to current "+rootsSuffix(gone))
	})
}

func TestResolveNameOnlyFollowsCurrent(t *testing.T) {
	root := t.TempDir()
	installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
	installVersion(t, root, "x", "2", map[string]string{"company.toml": "name = \"y\"\n"})
	setCurrent(t, root, "2")
	lib, err := Resolve(&fakeEnv{roots: []string{root}}, namePin())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if lib.Version != "2" || string(lib.Data) != "name = \"y\"\n" || lib.Dir != filepath.Join(root, "x", "2") {
		t.Fatalf("library = %+v, want x@2 read through its own directory", lib)
	}
	// Control: flipping current moves the answer.
	if err := os.Remove(filepath.Join(root, "x", "current")); err != nil {
		t.Fatal(err)
	}
	setCurrent(t, root, "1")
	lib, err = Resolve(&fakeEnv{roots: []string{root}}, namePin())
	if err != nil || lib.Version != "1" {
		t.Fatalf("after the flip: %+v, %v; want x@1", lib, err)
	}
}

func TestResolveNameOnlyReadsCurrentOnce(t *testing.T) {
	root := t.TempDir()
	installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
	setCurrent(t, root, "1")
	var calls []string
	orig := readLink
	readLink = func(p string) (string, error) { calls = append(calls, p); return os.Readlink(p) }
	t.Cleanup(func() { readLink = orig })

	if _, err := Resolve(&fakeEnv{roots: []string{root}}, namePin()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(calls) != 1 || calls[0] != filepath.Join(root, "x", "current") {
		t.Fatalf("readlink calls = %v, want exactly one, of %s", calls, filepath.Join(root, "x", "current"))
	}
	// Control: an exact pin reads no current at all.
	calls = nil
	if _, err := Resolve(&fakeEnv{roots: []string{root}}, exactPin("x", "1")); err != nil {
		t.Fatalf("Resolve exact: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("an exact pin read current: %v", calls)
	}
}

func TestCurrentIsNeverListedAsAVersion(t *testing.T) {
	root := t.TempDir()
	installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
	setCurrent(t, root, "1")
	// A pin that names the word current is not the link.
	_, err := Resolve(&fakeEnv{roots: []string{root}}, exactPin("x", "current"))
	wantRefusal(t, err, "2", "team core: company pin x@current is not installed in any root; marvel does not fall back to current "+rootsSuffix(root))
	// Even a real directory called current is not a version.
	installVersion(t, root, "y", "current", map[string]string{"company.toml": stubToml})
	_, err = Resolve(&fakeEnv{roots: []string{root}}, exactPin("y", "current"))
	wantRefusal(t, err, "2", "team core: company pin y@current is not installed in any root; marvel does not fall back to current "+rootsSuffix(root))
	// Control: the version beside the link is listed.
	if _, err := Resolve(&fakeEnv{roots: []string{root}}, exactPin("x", "1")); err != nil {
		t.Fatalf("x@1 beside current must resolve: %v", err)
	}
}

func TestSymlinkedVersionDirIsSkipped(t *testing.T) {
	root := t.TempDir()
	real := installVersion(t, t.TempDir(), "x", "1", map[string]string{"company.toml": stubToml})
	if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "x", "1")); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(&fakeEnv{roots: []string{root}}, exactPin("x", "1"))
	wantRefusal(t, err, "2", "team core: company pin x@1 is not installed in any root; marvel does not fall back to current "+rootsSuffix(root))
	// A symlinked name directory is skipped the same way.
	other := t.TempDir()
	realName := t.TempDir()
	installVersion(t, realName, "z", "1", map[string]string{"company.toml": stubToml})
	if err := os.Symlink(filepath.Join(realName, "z"), filepath.Join(other, "z")); err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(&fakeEnv{roots: []string{other}}, exactPin("z", "1"))
	wantRefusal(t, err, "2", "team core: company pin z@1 is not installed in any root; marvel does not fall back to current "+rootsSuffix(other))
}

func TestResolveSkipsDotNamesAndDirectoriesWithoutACompanyToml(t *testing.T) {
	root := t.TempDir()
	installVersion(t, root, "x", ".1.partial", map[string]string{"company.toml": stubToml})
	installVersion(t, root, "x", "2", map[string]string{"roles/a.md": "a"})
	installVersion(t, root, ".hidden", "1", map[string]string{"company.toml": stubToml})
	for _, p := range []Pin{exactPin("x", ".1.partial"), exactPin("x", "2"), exactPin(".hidden", "1")} {
		_, err := Resolve(&fakeEnv{roots: []string{root}}, p)
		wantRefusal(t, err, "2", fmt.Sprintf("team core: company pin %s@%s is not installed in any root; marvel does not fall back to current %s", p.Name, p.Version, rootsSuffix(root)))
	}
	// A directory called company.toml is not a company.toml.
	installVersion(t, root, "w", "1", map[string]string{"company.toml/inner": "x"})
	_, err := Resolve(&fakeEnv{roots: []string{root}}, exactPin("w", "1"))
	wantRefusal(t, err, "2", "team core: company pin w@1 is not installed in any root; marvel does not fall back to current "+rootsSuffix(root))
	// Control: a good version in the same name resolves.
	installVersion(t, root, "x", "3", map[string]string{"company.toml": stubToml})
	if _, err := Resolve(&fakeEnv{roots: []string{root}}, exactPin("x", "3")); err != nil {
		t.Fatalf("x@3 must resolve: %v", err)
	}
}

func TestDanglingCurrentGivesTheCurrentRow(t *testing.T) {
	t.Run("N9a: current names a version that is not installed", func(t *testing.T) {
		root := t.TempDir()
		installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
		setCurrent(t, root, "7")
		_, err := Resolve(&fakeEnv{roots: []string{root}}, namePin())
		wantRefusal(t, err, "N9a", "team core: company pin x follows current 7, which is not installed in any root; marvel does not fall back")
	})
	t.Run("N9a: the target is quoted when it is not a plain name", func(t *testing.T) {
		root := t.TempDir()
		installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
		setCurrent(t, root, "a b")
		_, err := Resolve(&fakeEnv{roots: []string{root}}, namePin())
		wantRefusal(t, err, "N9a", `team core: company pin x follows current "a b", which is not installed in any root; marvel does not fall back`)
	})
	t.Run("N9b: no current", func(t *testing.T) {
		root := t.TempDir()
		installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
		_, err := Resolve(&fakeEnv{roots: []string{root}}, namePin())
		wantRefusal(t, err, "N9b", "team core: company pin x has no readable current in "+quoteField(root, false))
	})
	t.Run("N9b: current is a directory, not a link", func(t *testing.T) {
		root := t.TempDir()
		installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
		if err := os.Mkdir(filepath.Join(root, "x", "current"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(&fakeEnv{roots: []string{root}}, namePin())
		wantRefusal(t, err, "N9b", "team core: company pin x has no readable current in "+quoteField(root, false))
	})
	t.Run("N8: the name is in no root", func(t *testing.T) {
		root := t.TempDir()
		_, err := Resolve(&fakeEnv{roots: []string{root}}, namePin())
		wantRefusal(t, err, "N8", "team core: company pin x is not installed in any root; marvel does not fall back "+rootsSuffix(root))
	})
}

func TestCurrentThatLeavesItsNameIsRefused(t *testing.T) {
	for _, target := range []string{"../y/1", "/abs/1", "1/sub", "..", "."} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			installVersion(t, root, "x", "1", map[string]string{"company.toml": stubToml})
			installVersion(t, root, "y", "1", map[string]string{"company.toml": stubToml})
			setCurrent(t, root, target)
			_, err := Resolve(&fakeEnv{roots: []string{root}}, namePin())
			wantRefusal(t, err, "N10", "company x in "+quoteField(root, false)+": current points outside x")
		})
	}
}

func TestAnAmbiguousNameIsRefusedForEveryPinForm(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	installVersion(t, a, "x", "1", map[string]string{"company.toml": stubToml})
	installVersion(t, b, "x", "1", map[string]string{"company.toml": stubToml})
	setCurrent(t, a, "1")
	want := "company x is in two roots: " + quoteField(a, false) + " and " + quoteField(b, false)
	for _, p := range []Pin{exactPin("x", "1"), namePin()} {
		_, err := Resolve(&fakeEnv{roots: []string{a, b}}, p)
		wantRefusal(t, err, "13", want)
	}
	// Control: a name held in one root of two resolves.
	installVersion(t, a, "only", "1", map[string]string{"company.toml": stubToml})
	if _, err := Resolve(&fakeEnv{roots: []string{a, b}}, exactPin("only", "1")); err != nil {
		t.Fatalf("a name in one root must resolve: %v", err)
	}
}

func TestResolveReportsARootsReadFailure(t *testing.T) {
	_, err := Resolve(&fakeEnv{rootsErr: errors.New("config unreadable")}, namePin())
	if err == nil || !strings.Contains(err.Error(), "config unreadable") {
		t.Fatalf("err = %v, want the roots failure", err)
	}
	var r *Refusal
	if errors.As(err, &r) {
		t.Fatalf("a roots read failure is not a refusal row: %+v", r)
	}
}
