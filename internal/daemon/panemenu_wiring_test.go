package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The pane-menu source is isolated, so the wiring file is the only place the
// daemon hands it a way into a pane. That way is two driver methods and no
// others: CapturePaneJoined to read, PaneForeground to ask what is in front.
// Any other call on d.driver in the file fails (marvel#552 review).
func TestPaneMenuWiringTouchesOnlyTwoDriverMethods(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"CapturePaneJoined": true, "PaneForeground": true}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "panemenu.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok || inner.Sel.Name != "driver" {
			return true
		}
		seen[sel.Sel.Name] = true
		if !allowed[sel.Sel.Name] {
			t.Errorf("%s: panemenu.go calls d.driver.%s", fset.Position(sel.Pos()), sel.Sel.Name)
		}
		return true
	})
	for name := range allowed {
		if !seen[name] {
			t.Errorf("the wiring no longer uses d.driver.%s; update this guard with it", name)
		}
	}
}
