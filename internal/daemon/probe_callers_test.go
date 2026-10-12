package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// knownhosts.Probe opens a TCP connection and the dial inventory in
// limitwiring_test.go does not list it, because the CLI reaches it and the
// daemon does not. This pins that: a daemon-side call would be a new dial the
// inventory cannot see, since this package already imports knownhosts.
func TestNoDaemonCodeCallsKnownhostsProbe(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Probe" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "knownhosts" {
				t.Errorf("%s uses knownhosts.Probe; the probe belongs to the CLI, and a daemon-side dial must be inventoried in limitwiring_test.go", name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no non-test source files found; the scan checked nothing")
	}
}
