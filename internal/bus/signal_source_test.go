package bus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawSignals returns the position of every selector that sends a signal
// without the prober: syscall.Kill, or a Signal call on an os.Process.
func rawSignals(fset *token.FileSet, f *ast.File) []token.Position {
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "syscall" && sel.Sel.Name == "Kill" {
			out = append(out, fset.Position(sel.Pos()))
		}
		if sel.Sel.Name == "Signal" {
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Process" {
				out = append(out, fset.Position(sel.Pos()))
			}
		}
		return true
	})
	return out
}

// Every signal to the broker goes through childproof's Prober.Signal, which
// re-proves the child first (design section 4.5). A raw syscall.Kill in this
// package would bypass it, and the process table in the tests could not see
// it. Killing a child through its own *exec.Cmd handle (Process.Kill, before
// it is reaped) is a different thing and is not matched.
func TestBusSignalsOnlyThroughTheProber(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	var bad []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, pos := range rawSignals(fset, f) {
			bad = append(bad, pos.String())
		}
	}
	if checked < 5 {
		t.Fatalf("parsed only %d non-test files; the glob is not reaching the package", checked)
	}
	if len(bad) > 0 {
		t.Errorf("signals sent without the prober: %v", bad)
	}
}

func TestRawSignalCheckSeesEachWayToSignal(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		hits int
	}{
		"syscall.Kill":                {"package p\nimport \"syscall\"\nfunc f() { _ = syscall.Kill(1, 0) }\n", 1},
		"a Process.Signal call":       {"package p\nfunc f(c *C) { _ = c.Process.Signal(nil) }\n", 1},
		"a Process.Kill on a handle":  {"package p\nfunc f(c *C) { _ = c.Process.Kill() }\n", 0},
		"syscall.SIGHUP is no signal": {"package p\nimport \"syscall\"\nvar x = syscall.SIGHUP\n", 0},
		"a method named Kill":         {"package p\nfunc f(p *P) { p.Kill() }\n", 0},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", tc.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := len(rawSignals(fset, f)); got != tc.hits {
			t.Errorf("%s: %d raw signals found, want %d", name, got, tc.hits)
		}
	}
}
