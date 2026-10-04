package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

type sendCall struct {
	pane, text     string
	literal, enter bool
}

type fakeKeys struct{ calls []sendCall }

func (f *fakeKeys) SendKeys(pane, text string, literal, enter bool) error {
	f.calls = append(f.calls, sendCall{pane, text, literal, enter})
	return nil
}

// The real sender, through a fake driver: exactly one SendKeys call, the digit 2,
// not literal, no Enter. Rewriting the key or turning Enter on fails this
// (marvel#556 review mutants c and d).
func TestLimitSenderSendsExactlyTheDigitTwo(t *testing.T) {
	t.Parallel()
	fake := &fakeKeys{}
	if err := limitSender(fake)("%9"); err != nil {
		t.Fatal(err)
	}
	want := sendCall{pane: "%9", text: "2", literal: false, enter: false}
	if len(fake.calls) != 1 || fake.calls[0] != want {
		t.Fatalf("calls = %+v, want exactly [%+v]", fake.calls, want)
	}
}

// The wiring files hand the isolated packages a way into a pane, so they are read
// as source: on d.driver they may call only CapturePaneJoined and PaneForeground;
// SendKeys appears only inside limitSender, with the constant key, not literal
// and no Enter; limitKey is "2"; and there are no package-level func variables.
// A new daemon file that sends a key cannot reach the action, which lives in
// internal/limitact and only holds the sender it is handed.
func TestLimitWiringCanOnlyCauseTheDigitTwo(t *testing.T) {
	t.Parallel()
	driverOK := map[string]bool{"CapturePaneJoined": true, "PaneForeground": true}
	fset := token.NewFileSet()
	var keyConst string
	sends, usedDriver := 0, map[string]bool{}
	for _, name := range []string{"panemenu.go", "limitaction.go"} {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
				t.Errorf("%s: package-level var in a wiring file", name)
			} else if ok && gen.Tok == token.CONST {
				for _, spec := range gen.Specs {
					vs := spec.(*ast.ValueSpec)
					if len(vs.Names) == 1 && vs.Names[0].Name == "limitKey" {
						if lit, ok := vs.Values[0].(*ast.BasicLit); ok {
							keyConst, _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "driver" {
					usedDriver[sel.Sel.Name] = true
					if !driverOK[sel.Sel.Name] {
						t.Errorf("%s: %s calls d.driver.%s", name, fset.Position(sel.Pos()), sel.Sel.Name)
					}
				}
				if sel.Sel.Name == "SendKeys" {
					call := false
					ast.Inspect(fn, func(m ast.Node) bool {
						if c, ok := m.(*ast.CallExpr); ok && c.Fun == sel {
							call = true
							sends++
							if fn.Name.Name != "limitSender" {
								t.Errorf("%s: SendKeys called in %s", fset.Position(sel.Pos()), fn.Name.Name)
							}
							if len(c.Args) != 4 {
								t.Errorf("%s: SendKeys with %d arguments", fset.Position(sel.Pos()), len(c.Args))
								return false
							}
							for i, want := range []string{"paneID", "limitKey", "false", "false"} {
								id, ok := c.Args[i].(*ast.Ident)
								if !ok || id.Name != want {
									t.Errorf("%s: SendKeys argument %d is not %s", fset.Position(sel.Pos()), i, want)
								}
							}
						}
						return true
					})
					if !call {
						t.Errorf("%s: SendKeys used as a value", fset.Position(sel.Pos()))
					}
				}
				return true
			})
		}
	}
	if keyConst != "2" {
		t.Errorf("limitKey = %q, want \"2\"", keyConst)
	}
	if sends != 1 {
		t.Errorf("SendKeys is called %d times in the wiring, want 1", sends)
	}
	for name := range driverOK {
		if !usedDriver[name] {
			t.Errorf("the wiring no longer uses d.driver.%s; update this guard with it", name)
		}
	}
}
