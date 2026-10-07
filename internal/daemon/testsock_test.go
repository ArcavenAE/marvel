package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// maxSocketPath is the longest unix socket path macOS accepts (104 bytes,
// marvel#383), less one for the terminator.
const maxSocketPath = 103

// testSocket is a socket path no other test process can be using: a short
// directory made for this call, removed with the test. It is not under
// t.TempDir(), whose macOS path is long enough to pass the socket limit
// (marvel#383); the directory name carries a random part, and the file name
// is the caller's, so two calls with one name still differ.
func testSocket(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mv")
	if err != nil {
		t.Fatalf("make socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name+".sock")
}

// A path from testSocket is unique per call, even for the same name.
func TestTestSocketIsUniquePerCall(t *testing.T) {
	a, b := testSocket(t, "detach-1"), testSocket(t, "detach-1")
	if a == b {
		t.Errorf("two calls returned the same path %q", a)
	}
	for _, p := range []string{a, b} {
		if !strings.HasSuffix(p, ".sock") {
			t.Errorf("path %q should end in .sock", p)
		}
	}
}

// The path stays under the socket limit however long TMPDIR is, because seat
// scratch directories are long: a daemon test that binds a socket under a
// 64-character TMPDIR passed and failed at 65 before the directory was rooted
// at /tmp. It is checked for the longest name any daemon test passes.
func TestTestSocketStaysUnderTheLimitWithALongTMPDIR(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 200))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)
	name := longestSocketName(t)
	p := testSocket(t, name)
	if len(p) > maxSocketPath {
		t.Errorf("path for the longest name %q is %d bytes under a %d-byte TMPDIR, over the %d-byte socket limit: %q",
			name, len(p), len(long), maxSocketPath, p)
	}
}

// longestSocketName is the longest literal name a daemon test hands to
// testSocket, or to startTestDaemon, which names its socket after its
// workspace argument.
func longestSocketName(t *testing.T) string {
	t.Helper()
	files, _ := filepath.Glob("*_test.go")
	fset := token.NewFileSet()
	longest := ""
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			idx := -1
			switch id.Name {
			case "testSocket":
				idx = 1
			case "startTestDaemon":
				idx = 1
			}
			if idx < 0 || len(call.Args) <= idx {
				return true
			}
			if lit, ok := call.Args[idx].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && len(v) > len(longest) {
					longest = v
				}
			}
			return true
		})
	}
	if longest == "" {
		t.Fatal("found no literal socket names")
	}
	return longest
}

// No daemon test binds a fixed socket name in the shared temp directory:
// two test runs on one host, such as several seats building marvel at once,
// would fail each other (marvel#480). Every socket path comes from
// testSocket.
func TestDaemonTestsBindNoFixedSocketNames(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files found: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if f == "testsock_test.go" {
			continue
		}
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		reported := map[token.Pos]bool{}
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.CallExpr, *ast.BinaryExpr:
			default:
				return true
			}
			var usesTempDir bool
			var lits []*ast.BasicLit
			ast.Inspect(n, func(m ast.Node) bool {
				if c, ok := m.(*ast.CallExpr); ok && isTempDirCall(c) {
					usesTempDir = true
				}
				if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, ".sock") {
					lits = append(lits, lit)
				}
				return true
			})
			if !usesTempDir {
				return true
			}
			for _, lit := range lits {
				if !reported[lit.Pos()] {
					reported[lit.Pos()] = true
					t.Errorf("%s: socket path built from os.TempDir() with a fixed name %s; use testSocket", fset.Position(lit.Pos()), lit.Value)
				}
			}
			return true
		})
	}
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func isTempDirCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	return ok && isSelector(call.Fun, "os", "TempDir")
}
