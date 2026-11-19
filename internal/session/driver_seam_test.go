package session

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The manager depends on the TmuxDriver interface, not on *tmux.Driver, so a
// fake from internal/tmux/tmuxtest can stand in for tmux in a test (I-458
// row 8). This reads the field and the constructor's parameter from the source,
// so a change that puts the concrete driver back fails here.
func TestManagerHoldsTheDriverInterface(t *testing.T) {
	t.Parallel()
	f, err := parser.ParseFile(token.NewFileSet(), "manager.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var field, param string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TypeSpec:
			if x.Name.Name != "Manager" {
				return true
			}
			st, ok := x.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fl := range st.Fields.List {
				for _, name := range fl.Names {
					if name.Name == "driver" {
						field = exprString(fl.Type)
					}
				}
			}
		case *ast.FuncDecl:
			if x.Name.Name != "NewManager" {
				return true
			}
			for _, p := range x.Type.Params.List {
				for _, name := range p.Names {
					if name.Name == "driver" {
						param = exprString(p.Type)
					}
				}
			}
		}
		return true
	})
	if field != "TmuxDriver" {
		t.Errorf("Manager.driver is typed %q, want TmuxDriver", field)
	}
	if param != "TmuxDriver" {
		t.Errorf("NewManager takes its driver as %q, want TmuxDriver", param)
	}
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	}
	return "?"
}
