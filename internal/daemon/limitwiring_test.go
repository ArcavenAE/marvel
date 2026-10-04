package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The isolated packages (internal/panemenu, internal/limitact) can reach a pane
// only through what the daemon hands them. This reads EVERY non-test file of the
// daemon package, not a list of files, and pins that hand-off to exact
// expressions, so a new file cannot slip a different sender or capture in
// (marvel#556 review: the first guard enumerated files).

type pkgFiles struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

func parseDaemon(t *testing.T) pkgFiles {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("no daemon sources: %v", err)
	}
	p := pkgFiles{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		src, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(p.fset, n, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		p.files[n] = f
	}
	return p
}

func (p pkgFiles) each(fn func(name string, n ast.Node) bool) {
	var names []string
	for n := range p.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ast.Inspect(p.files[n], func(x ast.Node) bool { return fn(n, x) })
	}
}

func isSel(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// fieldExprs maps a composite literal's keys to their expressions as source text.
func fieldExprs(lit *ast.CompositeLit) map[string]string {
	out := map[string]string{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			out["<positional>"] = types.ExprString(el)
			continue
		}
		out[types.ExprString(kv.Key)] = types.ExprString(kv.Value)
	}
	return out
}

func expectFields(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: field %s is %q, pinned as %q", what, k, got[k], v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: unpinned field %s = %s", what, k, got[k])
		}
	}
}

// limitSender is the only SendKeys the limit action can cause: the constant
// key, not literal, no Enter. limitKey is "2".
func TestLimitSenderIsPinned(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	var keyConst string
	var sendCalls, senderDecls int
	p.each(func(name string, n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			if len(x.Names) == 1 && x.Names[0].Name == "limitKey" && len(x.Values) == 1 {
				if lit, ok := x.Values[0].(*ast.BasicLit); ok {
					keyConst, _ = strconv.Unquote(lit.Value)
				}
			}
		case *ast.FuncDecl:
			if x.Name.Name != "limitSender" {
				return true
			}
			senderDecls++
			ast.Inspect(x, func(m ast.Node) bool {
				c, ok := m.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SendKeys" {
					sendCalls++
					got := make([]string, len(c.Args))
					for i, a := range c.Args {
						got[i] = types.ExprString(a)
					}
					if strings.Join(got, ",") != "paneID,limitKey,false,false" {
						t.Errorf("SendKeys arguments are (%s), pinned as (paneID,limitKey,false,false)", strings.Join(got, ","))
					}
				}
				return true
			})
		}
		return true
	})
	if keyConst != "2" {
		t.Errorf("limitKey = %q, want \"2\"", keyConst)
	}
	if senderDecls != 1 || sendCalls != 1 {
		t.Errorf("limitSender declared %d times with %d SendKeys calls, want 1 and 1", senderDecls, sendCalls)
	}
}

// The action is built once, in newLimitAction, from exactly these four
// expressions, and limitSender is referenced nowhere else. A new file's sender or
// capture cannot be substituted without changing a pinned expression here.
func TestLimitActionIsBuiltOnlyFromThePinnedExpressions(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	var builds, senderUses, hooksAssigns, actAssigns int
	p.each(func(name string, n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if isSel(x.Fun, "limitact", "New") {
				builds++
				if len(x.Args) != 1 {
					t.Errorf("limitact.New takes one Deps literal")
					return true
				}
				lit, ok := x.Args[0].(*ast.CompositeLit)
				if !ok {
					t.Errorf("%s: limitact.New is not given a literal", name)
					return true
				}
				expectFields(t, "limitact.Deps", fieldExprs(lit), map[string]string{
					"Samples": "d.limitMenu",
					"Events":  "d.events",
					"Send":    "limitSender(d.driver)",
					"Capture": "d.driver.CapturePaneJoined",
				})
			}
		case *ast.Ident:
			if x.Name == "limitSender" {
				senderUses++
			}
		case *ast.AssignStmt:
			for i, l := range x.Lhs {
				switch types.ExprString(l) {
				case "d.paneMenu.Hooks":
					hooksAssigns++
					if i >= len(x.Rhs) || types.ExprString(x.Rhs[i]) != "d.limitAct.Hooks()" {
						t.Errorf("%s: Hooks assigned something other than d.limitAct.Hooks()", name)
					}
				case "d.limitAct":
					actAssigns++
					if i >= len(x.Rhs) || types.ExprString(x.Rhs[i]) != "d.newLimitAction()" {
						t.Errorf("%s: limitAct assigned something other than d.newLimitAction()", name)
					}
				}
			}
		}
		return true
	})
	if builds != 1 {
		t.Errorf("limitact.New is called %d times, want 1", builds)
	}
	if senderUses != 2 { // the declaration and its one use
		t.Errorf("limitSender is named %d times, want 2 (declaration and one use)", senderUses)
	}
	if hooksAssigns != 1 || actAssigns != 1 {
		t.Errorf("Hooks assigned %d times and limitAct %d times, want 1 and 1", hooksAssigns, actAssigns)
	}
}

// The pane-menu source is built once, with pinned fields; its in-front function
// calls d.driver.PaneForeground (a call, not a blank assignment) and no other
// driver method.
func TestPaneMenuSourceIsBuiltOnlyFromThePinnedExpressions(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	builds := 0
	p.each(func(name string, n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSel(lit.Type, "panemenu", "Source") {
			return true
		}
		builds++
		fields := fieldExprs(lit)
		inFront := fields["InFront"]
		delete(fields, "InFront")
		expectFields(t, "panemenu.Source", fields, map[string]string{
			"Samples":  "d.limitMenu",
			"Store":    "d.store",
			"Readings": "d.accounts",
			"Events":   "d.events",
			"Capture":  "d.driver.CapturePaneJoined",
			"HostZone": "d.hostLocation",
		})
		if inFront == "" {
			t.Error("panemenu.Source has no InFront")
		}
		var fn *ast.FuncLit
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok && types.ExprString(kv.Key) == "InFront" {
				fn, _ = kv.Value.(*ast.FuncLit)
			}
		}
		if fn == nil {
			t.Error("InFront is not a function literal")
			return true
		}
		called := false
		ast.Inspect(fn, func(m ast.Node) bool {
			if c, ok := m.(*ast.CallExpr); ok && types.ExprString(c.Fun) == "d.driver.PaneForeground" {
				called = true
			}
			if sel, ok := m.(*ast.SelectorExpr); ok {
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "driver" && sel.Sel.Name != "PaneForeground" {
					t.Errorf("InFront touches d.driver.%s", sel.Sel.Name)
				}
			}
			return true
		})
		if !called {
			t.Error("InFront does not call d.driver.PaneForeground")
		}
		return true
	})
	if builds != 1 {
		t.Errorf("panemenu.Source is built %d times, want 1", builds)
	}
}
