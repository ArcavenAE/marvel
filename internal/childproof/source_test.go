package childproof

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot walks up from the test's directory to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// testOnlyNames are the exported names that exist for tests: one builds a
// Prober from the caller's seams, the other reads real processes for a test
// that fakes only the pids it plants.
var testOnlyNames = map[string]bool{"NewForTest": true, "ReadIdentityForTest": true}

// newForTestRefs returns the position of every identifier named in
// testOnlyNames in a parsed non-test file, except the declaration of the
// function itself.
func newForTestRefs(fset *token.FileSet, f *ast.File) []token.Position {
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if testOnlyNames[n.Name.Name] && n.Recv == nil {
				// The declaration is allowed; its body is still read.
				if n.Body != nil {
					ast.Inspect(n.Body, func(m ast.Node) bool {
						if id, ok := m.(*ast.Ident); ok && testOnlyNames[id.Name] {
							out = append(out, fset.Position(id.Pos()))
						}
						return true
					})
				}
				return false
			}
		case *ast.Ident:
			if testOnlyNames[n.Name] {
				out = append(out, fset.Position(n.Pos()))
			}
		}
		return true
	})
	return out
}

// NewForTest hands out a Prober whose kill and read are the caller's, and
// testing.Testing() is also true for non-test code linked into a test binary,
// so the gate in NewForTest is not enough. This test fails on any use of it in
// a file that is not a _test.go file (design section 7, item 9).
func TestNewForTestIsReferencedOnlyFromTestFiles(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	var bad []string
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil // a file the toolchain would refuse is not this test's business
		}
		checked++
		for _, pos := range newForTestRefs(fset, f) {
			bad = append(bad, pos.String())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("parsed only %d non-test files under %s; the walk is not reaching the module", checked, root)
	}
	if len(bad) > 0 {
		t.Errorf("NewForTest is referenced outside a _test.go file: %v", bad)
	}
}

// Positive controls: the check sees each way a non-test file can reach
// NewForTest, and lets the declaration alone through.
func TestNewForTestCheckSeesEachWayToReachIt(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		hits int
	}{
		"the declaration alone":    {"package p\nfunc NewForTest() {}\n", 0},
		"a call":                   {"package p\nfunc f() { childproof.NewForTest(nil, nil) }\n", 1},
		"a method value":           {"package p\nvar g = childproof.NewForTest\n", 1},
		"an alias":                 {"package p\nimport cp \"x/childproof\"\nvar g = cp.NewForTest\n", 1},
		"inside the declaration":   {"package p\nfunc NewForTest() { NewForTest() }\n", 1},
		"a method of that name":    {"package p\ntype T struct{}\nfunc (T) NewForTest() {}\n", 1},
		"the reader, called":       {"package p\nfunc f() { childproof.ReadIdentityForTest(1) }\n", 1},
		"the reader, passed on":    {"package p\nvar r = childproof.ReadIdentityForTest\n", 1},
		"the reader's declaration": {"package p\nfunc ReadIdentityForTest(int) {}\n", 0},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", tc.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := len(newForTestRefs(fset, f)); got != tc.hits {
			t.Errorf("%s: %d references found, want %d", name, got, tc.hits)
		}
	}
}
