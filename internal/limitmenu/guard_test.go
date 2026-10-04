package limitmenu

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The matcher reads text and returns a verdict (the package comment). It is
// handed to code that sits next to a key, so it must have no way to reach a pane
// or the system: standard library only, none of the packages that run programs,
// open sockets or defeat the type system, no key-sending names, no `any`, no
// type assertion, no package-level func variable. Every non-test file is read, so
// a file added later is read too (marvel#556 review).
func TestMatcherCannotReachAPaneOrTheSystem(t *testing.T) {
	t.Parallel()
	bannedImports := map[string]bool{"os": true, "os/exec": true, "net": true, "net/http": true, "syscall": true, "unsafe": true, "reflect": true, "plugin": true, "io/ioutil": true}
	bannedNames := map[string]bool{"SendKeys": true, "SendLiteral": true, "Inject": true, "Paste": true, "Driver": true, "Notify": true}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(p, ".") {
				t.Errorf("%s imports %s; the matcher is standard library only", name, p)
			}
			if bannedImports[p] {
				t.Errorf("%s imports %s", name, p)
			}
		}
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
				for _, spec := range gen.Specs {
					vs := spec.(*ast.ValueSpec)
					if _, isFunc := vs.Type.(*ast.FuncType); isFunc {
						t.Errorf("%s: package-level func variable %v", fset.Position(vs.Pos()), vs.Names)
					}
					for _, v := range vs.Values {
						if _, lit := v.(*ast.FuncLit); lit {
							t.Errorf("%s: package-level func literal %v", fset.Position(vs.Pos()), vs.Names)
						}
					}
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.TypeAssertExpr:
				t.Errorf("%s: type assertion", fset.Position(x.Pos()))
			case *ast.InterfaceType:
				t.Errorf("%s: interface type", fset.Position(x.Pos()))
			case *ast.Ident:
				if x.Name == "any" || bannedNames[x.Name] {
					t.Errorf("%s: names %s", fset.Position(x.Pos()), x.Name)
				}
			case *ast.SelectorExpr:
				if bannedNames[x.Sel.Name] || strings.HasPrefix(x.Sel.Name, "Send") {
					t.Errorf("%s: names %s", fset.Position(x.Pos()), x.Sel.Name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no non-test source was read")
	}
}
