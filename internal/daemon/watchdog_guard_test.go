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

// The watchdog informs and never acts (docs/design/harness-state-watchdog-p1.md
// sections 2 and 6, test 10). This reads its source: no call that types into a
// pane, injects, or restarts, and no store write but SetHarnessState.
func TestWatchdogSourceNeverActsOnAPane(t *testing.T) {
	files := []string{"watchdog.go"}
	panestate, err := filepath.Glob("../panestate/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range panestate {
		if !strings.HasSuffix(f, "_test.go") {
			files = append(files, f)
		}
	}
	forbidden := []string{"SendKeys", "Inject", "Restart", "Paste", "SendText", "KillPane", "KillWindow", "Respawn", "Escapes"}
	allowedStore := map[string]bool{"ListSessions": true, "SetHarnessState": true}
	fset := token.NewFileSet()
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if strings.HasSuffix(name, "watchdog.go") {
				continue
			}
			if p := strings.Trim(imp.Path.Value, `"`); strings.Contains(p, "marvel/internal/tmux") || strings.Contains(p, "marvel/internal/daemon") {
				t.Errorf("%s imports %s; the matcher must not reach a pane", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			for _, bad := range forbidden {
				if strings.Contains(sel.Sel.Name, bad) {
					t.Errorf("%s: %s uses %s", name, fset.Position(sel.Pos()), sel.Sel.Name)
				}
			}
			// w.store.<Method>: the store is only read, plus the one field.
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "store" && !allowedStore[sel.Sel.Name] {
				t.Errorf("%s: %s calls store.%s", name, fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}
