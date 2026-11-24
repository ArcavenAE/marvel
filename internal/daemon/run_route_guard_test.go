package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The one route from the daemon to a process is session.RunChild, which takes a
// composed plan and puts its sandbox-exec prefix in front of the argv. The
// daemon package starts no process itself (the os/exec guard above). A call to
// session.RunChild may appear only in the run.sandboxed handler's file, so a new
// caller is a new route and fails here (marvel#1596).
const runChildAllowedFile = "run_sandboxed.go"

// runChildUseViolations lists every mention of session.RunChild in p outside
// runChildAllowedFile, a call or a method value alike.
func runChildUseViolations(p pkgFiles) []string {
	var out []string
	p.each(func(file string, n ast.Node) bool {
		// An aliased import of the session package would hide the selector from
		// the scan below, so any name other than session is a violation.
		if imp, ok := n.(*ast.ImportSpec); ok && imp.Path.Value == `"github.com/arcavenae/marvel/internal/session"` &&
			imp.Name != nil && imp.Name.Name != "session" {
			out = append(out, file+": the session package is imported as "+imp.Name.Name+"; the route guard reads it as session")
		}
		sel, ok := n.(*ast.SelectorExpr)
		if ok && isSel(sel, "session", "RunChild") && file != runChildAllowedFile {
			out = append(out, file+": session.RunChild at "+p.fset.Position(sel.Pos()).String()+"; only "+runChildAllowedFile+" may")
		}
		return true
	})
	return out
}

func TestSessionRunChildIsCalledOnlyFromTheRunSandboxedHandler(t *testing.T) {
	t.Parallel()
	for _, v := range runChildUseViolations(parseDaemon(t)) {
		t.Error(v)
	}
}

// The inventory: exactly one call, in the handler.
func TestRunSandboxedHandlerCallsSessionRunChildOnce(t *testing.T) {
	t.Parallel()
	n := 0
	parseDaemon(t).eachFunc(func(file, fun string, node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && isSel(call.Fun, "session", "RunChild") {
			if file != runChildAllowedFile || fun != "*Daemon.handleRunSandboxed" {
				t.Errorf("session.RunChild called in %s (%s)", fun, file)
			}
			n++
		}
		return true
	})
	if n != 1 {
		t.Errorf("session.RunChild is called %d times, inventoried as 1", n)
	}
}

// A planted call proves the ban still holds for every other file: the same
// source passes under the allowed name and fails under any other.
func TestAPlantedSessionRunChildCallInAnyOtherFileIsRefused(t *testing.T) {
	t.Parallel()
	const src = "package daemon\n\nimport \"github.com/arcavenae/marvel/internal/session\"\n\nvar _ = session.RunChild\n"
	plant := func(name string) pkgFiles {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return pkgFiles{fset: fset, files: map[string]*ast.File{name: f}}
	}
	if got := runChildUseViolations(plant(runChildAllowedFile)); len(got) != 0 {
		t.Errorf("the allowed file was refused: %v", got)
	}
	const aliased = "package daemon\n\nimport s \"github.com/arcavenae/marvel/internal/session\"\n\nvar _ = s.RunChild\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, runChildAllowedFile, aliased, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := runChildUseViolations(pkgFiles{fset: fset, files: map[string]*ast.File{runChildAllowedFile: f}}); len(got) != 1 || !strings.Contains(got[0], "imported as s") {
		t.Errorf("an aliased session import: violations = %v, want one naming the alias", got)
	}
	for _, other := range []string{"daemon.go", "methods.go", "planted.go"} {
		if got := runChildUseViolations(plant(other)); len(got) != 1 || !strings.Contains(got[0], other) {
			t.Errorf("session.RunChild in %s: violations = %v, want one naming the file", other, got)
		}
	}
}
