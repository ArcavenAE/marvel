package team

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// callSites returns "file:function" for every call of method on a sessMgr
// field in the non-test Go files of dir, sorted.
func callSites(t *testing.T, dir, method string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s: %v", dir, err)
	}
	var out []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != method {
					return true
				}
				if recv, ok := sel.X.(*ast.SelectorExpr); ok && recv.Sel.Name == "sessMgr" {
					out = append(out, filepath.Base(file)+":"+fn.Name.Name)
				}
				return true
			})
		}
	}
	slices.Sort(out)
	return out
}

// TestOnlyTerminalStopsReapAHome pins which delete sites reap a seat's home and
// which do not. Delete is also the restart, shift and respawn path, where the
// same key reuses the same home, so those sites must stay on plain Delete. A
// site added or moved fails here until it is placed on purpose.
func TestOnlyTerminalStopsReapAHome(t *testing.T) {
	reaping := map[string][]string{
		// scale-down, a role the manifest dropped, and a prune retire a seat;
		// a team delete or prune deletes its sessions.
		".": {"prune.go:deleteSessionsLocked", "retire.go:advanceRetirement", "retire.go:killRetired", "retire.go:retireSession", "retire.go:retireSession"},
		// an operator delete of a session, and the workspace cascade.
		"../daemon": {"daemon.go:handleDelete", "daemon.go:handleDelete"},
	}
	plain := map[string][]string{
		// the finished-run drop, the health restart, the shift abort and the
		// rolling-shift drain, and the schedule clock.
		".": {"controller.go:abortStuckShift", "controller.go:applyRolePlan", "controller.go:restartSession", "controller.go:shiftDrain", "schedule_clock.go:stepScheduleClock"},
	}
	for dir, want := range reaping {
		got := callSites(t, dir, "DeleteAndReap")
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s DeleteAndReap sites = %v, want %v", dir, got, want)
		}
	}
	for dir, want := range plain {
		got := callSites(t, dir, "Delete")
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s plain Delete sites = %v, want %v", dir, got, want)
		}
	}
	if got := callSites(t, "../daemon", "Delete"); len(got) != 0 {
		t.Errorf("../daemon calls plain Delete at %v; an operator delete reaps", got)
	}
}
