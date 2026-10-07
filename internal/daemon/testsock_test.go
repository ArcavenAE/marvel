package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxSocketPath is the longest unix socket path macOS accepts (104 bytes,
// marvel#383), less one for the terminator.
const maxSocketPath = 103

// testSocket is a socket path no other test process can be using. Stub:
// the real helper is in the next commit.
func testSocket(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(os.TempDir(), "marvel-test-"+name+".sock")
}

// A path from testSocket is unique per call, even for the same name, and
// short enough for the socket path limit.
func TestTestSocketIsUniquePerCallAndShort(t *testing.T) {
	a, b := testSocket(t, "detach-1"), testSocket(t, "detach-1")
	if a == b {
		t.Errorf("two calls returned the same path %q", a)
	}
	for _, p := range []string{a, b} {
		if len(p) > maxSocketPath {
			t.Errorf("path is %d bytes, over the %d-byte socket limit: %q", len(p), maxSocketPath, p)
		}
		if !strings.HasSuffix(p, ".sock") {
			t.Errorf("path %q should end in .sock", p)
		}
	}
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
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSelector(call.Fun, "filepath", "Join") || len(call.Args) < 2 || !isTempDirCall(call.Args[0]) {
				return true
			}
			for _, arg := range call.Args[1:] {
				ast.Inspect(arg, func(m ast.Node) bool {
					if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, ".sock") {
						t.Errorf("%s: socket path joins os.TempDir() with a fixed name %s; use testSocket", fset.Position(lit.Pos()), lit.Value)
					}
					return true
				})
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
