package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// marryEntryPoints are the functions the shared vectors call.
var marryEntryPoints = []string{"Marry", "MarryAddress", "AnswerTake", "RecheckBeforeSend"}

// marrySourceFiles returns every non-test file of this package that declares
// a package-level name the entry points reach, following references between
// package-level declarations.
func marrySourceFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	declFile := map[string]string{}
	refs := map[string][]string{}
	fset := token.NewFileSet()
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			var names []string
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil {
					// A method is reached through its type.
					names = []string{receiverType(d.Recv)}
				} else {
					names = []string{d.Name.Name}
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						names = append(names, s.Name.Name)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names = append(names, n.Name)
						}
					}
				}
			}
			used := referencedNames(d)
			for _, n := range names {
				if n == "" || n == "_" {
					continue
				}
				if _, seen := declFile[n]; !seen {
					declFile[n] = filepath.ToSlash(filepath.Join("internal", "api", p))
				}
				refs[n] = append(refs[n], used...)
			}
		}
	}
	reached := map[string]bool{}
	queue := slices.Clone(marryEntryPoints)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if reached[n] {
			continue
		}
		if _, ok := declFile[n]; !ok {
			continue
		}
		reached[n] = true
		queue = append(queue, refs[n]...)
	}
	var files []string
	for n := range reached {
		if !slices.Contains(files, declFile[n]) {
			files = append(files, declFile[n])
		}
	}
	slices.Sort(files)
	return files
}

// referencedNames returns the identifiers in d that can name a package-level
// declaration. A selector's field or method name, a struct field's name and a
// composite literal's key name members of a type, not package-level names, so
// they are skipped; counting them would reach every type with a field of the
// same name.
func referencedNames(d ast.Decl) []string {
	skip := map[*ast.Ident]bool{}
	var used []string
	ast.Inspect(d, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			skip[n.Sel] = true
		case *ast.Field:
			for _, id := range n.Names {
				skip[id] = true
			}
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.Ident:
			if !skip[n] {
				used = append(used, n.Name)
			}
		}
		return true
	})
	return used
}

func receiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	e := recv.List[0].Type
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// The trigger row's grep reads every file of the marry source, so a send path
// added to a helper the step reaches is caught, not only one in
// readiness_marry.go.
func TestMarryGrepReadsEveryMarrySourceFile(t *testing.T) {
	t.Parallel()
	want := marrySourceFiles(t)
	if !slices.Contains(want, "internal/api/readiness_marry.go") {
		t.Fatalf("the reach found %q, which misses the marry file itself", want)
	}
	var grepped []string
	for _, v := range loadMarryVectors(t).Vectors {
		for _, st := range v.Steps {
			if st.Op == "grep" {
				grepped = append(grepped, st.File)
			}
		}
	}
	for _, f := range want {
		if !slices.Contains(grepped, f) {
			t.Errorf("the marry source file %s has no grep step", f)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "internal", "api", "readiness_marry.go")); err != nil {
		t.Fatalf("the grep paths do not resolve from here: %v", err)
	}
}
