package bus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"testing"
)

// unattached returns one entry per NewSupervisor call in f that is not matched
// by its own attach call, so it would run the real kill seam with no process
// table behind it. A call is matched when its result is assigned to a
// variable (or field) that an attach call takes as its second argument, and
// each attach covers one build: two builds into one name need two attaches. A
// test that never starts a broker may be listed in allowed.
func unattached(f *ast.File, allowed map[string]bool) []string {
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || allowed[fn.Name.Name] {
			continue
		}
		isNew := func(e ast.Expr) bool {
			call, ok := e.(*ast.CallExpr)
			if !ok {
				return false
			}
			id, ok := call.Fun.(*ast.Ident)
			return ok && id.Name == "NewSupervisor"
		}
		builds := map[string]int{} // target expression -> builds into it
		attaches := map[string]int{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				if len(n.Rhs) == 1 && isNew(n.Rhs[0]) {
					if name := types.ExprString(n.Lhs[0]); name != "_" {
						builds[name]++
					}
				}
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "attach" && len(n.Args) >= 2 {
					attaches[types.ExprString(n.Args[1])]++
				}
			}
			return true
		})
		total := 0
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if isNew(asExpr(n)) {
				total++
			}
			return true
		})
		named := 0
		for _, c := range builds {
			named += c
		}
		bare := total - named // results with no name to attach
		for name, c := range builds {
			for i := attaches[name]; i < c; i++ {
				out = append(out, fn.Name.Name)
			}
		}
		for i := 0; i < bare; i++ {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

func asExpr(n ast.Node) ast.Expr {
	e, _ := n.(ast.Expr)
	return e
}

// newTestSupervisor is the single registration point (design section 7), so a
// test that builds a supervisor another way must attach the same table. One
// that never starts a broker, because it asks for the binary that is not there,
// is the only exception.
func TestEverySupervisorATestBuildsIsAttachedToAProcessTable(t *testing.T) {
	allowed := map[string]bool{"TestNewSupervisorNamesMissingBinary": true}
	paths, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 10 {
		t.Fatalf("found %d test files; the glob is not reaching the package", len(paths))
	}
	fset := token.NewFileSet()
	for _, path := range paths {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range unattached(f, allowed) {
			t.Errorf("%s: %s builds a Supervisor with NewSupervisor and never attaches a process table", path, name)
		}
	}
}

func TestUnattachedSeesEachWayToBuildOne(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want int
	}{
		"built and attached":       {"package p\nfunc TestA() { s, _ := NewSupervisor(); attach(t, s, nil) }\n", 0},
		"built, never attached":    {"package p\nfunc TestA() { s, _ := NewSupervisor(); _ = s }\n", 1},
		"attached in a closure":    {"package p\nfunc TestA() { s, _ := NewSupervisor(); func() { attach(t, s, nil) }() }\n", 0},
		"not built":                {"package p\nfunc TestA() { _ = Other() }\n", 0},
		"the allowed exception":    {"package p\nfunc TestNewSupervisorNamesMissingBinary() { _, _ = NewSupervisor() }\n", 0},
		"two builds, one attach":   {"package p\nfunc TestA() { s1, _ := NewSupervisor(); s2, _ := NewSupervisor(); attach(t, s1, nil); _ = s2 }\n", 1},
		"two builds, two attaches": {"package p\nfunc TestA() { s1, _ := NewSupervisor(); s2, _ := NewSupervisor(); attach(t, s1, nil); attach(t, s2, nil) }\n", 0},
		"attached to another one":  {"package p\nfunc TestA() { s1, _ := NewSupervisor(); other := 1; attach(t, other, nil); _ = s1 }\n", 1},
		"one variable built twice": {"package p\nfunc TestA() { s, _ := NewSupervisor(); s, _ = NewSupervisor(); attach(t, s, nil) }\n", 1},
		"built into a field":       {"package p\nfunc TestA() { x.s, _ = NewSupervisor(); attach(t, x.s, nil) }\n", 0},
		"built, passed on":         {"package p\nfunc TestA() { use(NewSupervisor()) }\n", 1},
		"two functions, one bad":   {"package p\nfunc TestA() { _, _ = NewSupervisor() }\nfunc TestB() { s, _ := NewSupervisor(); attach(t, s, nil) }\n", 1},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x_test.go", tc.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := unattached(f, map[string]bool{"TestNewSupervisorNamesMissingBinary": true}); len(got) != tc.want {
			t.Errorf("%s: %v, want %d", name, got, tc.want)
		}
	}
}
