package bus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// unattached returns the names of functions in f that build a Supervisor
// with NewSupervisor and never call attach, so they would run the real kill
// seam with no process table behind it. A test that never starts a broker may
// be listed in allowed.
func unattached(f *ast.File, allowed map[string]bool) []string {
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || allowed[fn.Name.Name] {
			continue
		}
		var builds, attaches bool
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok {
					switch id.Name {
					case "NewSupervisor":
						builds = true
					case "attach":
						attaches = true
					}
				}
			}
			return true
		})
		if builds && !attaches {
			out = append(out, fn.Name.Name)
		}
	}
	return out
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
		"built and attached":     {"package p\nfunc TestA() { s, _ := NewSupervisor(); attach(t, s, nil) }\n", 0},
		"built, never attached":  {"package p\nfunc TestA() { s, _ := NewSupervisor(); _ = s }\n", 1},
		"attached in a closure":  {"package p\nfunc TestA() { s, _ := NewSupervisor(); func() { attach(t, s, nil) }() }\n", 0},
		"not built":              {"package p\nfunc TestA() { _ = Other() }\n", 0},
		"the allowed exception":  {"package p\nfunc TestNewSupervisorNamesMissingBinary() { _, _ = NewSupervisor() }\n", 0},
		"two functions, one bad": {"package p\nfunc TestA() { _, _ = NewSupervisor() }\nfunc TestB() { s, _ := NewSupervisor(); attach(t, s, nil) }\n", 1},
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
