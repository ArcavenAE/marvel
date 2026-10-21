package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// marryAPIDir is internal/api, relative to this package.
var marryAPIDir = filepath.Join("..", "..", "internal", "api")

// apiDecl is one package-level declaration of internal/api: the names it
// references that can be package-level, and the packages it uses by import
// path.
type apiDecl struct {
	file    string
	refs    []string
	imports []string
}

// apiDecls parses internal/api's non-test files and indexes each
// package-level name by its declaration. A method is indexed under its
// receiver type, so reaching a type reaches its methods.
func apiDecls(t *testing.T) map[string][]apiDecl {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(marryAPIDir, "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no internal/api source (%v)", err)
	}
	out := map[string][]apiDecl{}
	fset := token.NewFileSet()
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		// The name each import is used by in this file.
		byName := map[string]string{}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			byName[name] = path
		}
		for _, d := range f.Decls {
			decl := apiDecl{file: filepath.Base(p)}
			skip := map[*ast.Ident]bool{}
			ast.Inspect(d, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					skip[n.Sel] = true
					if id, ok := n.X.(*ast.Ident); ok {
						if path, ok := byName[id.Name]; ok {
							skip[id] = true
							decl.imports = append(decl.imports, path)
						}
					}
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
						decl.refs = append(decl.refs, n.Name)
					}
				}
				return true
			})
			for _, name := range declNames(d) {
				out[name] = append(out[name], decl)
			}
		}
	}
	return out
}

func declNames(d ast.Decl) []string {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil || len(d.Recv.List) == 0 {
			return []string{d.Name.Name}
		}
		e := d.Recv.List[0].Type
		if s, ok := e.(*ast.StarExpr); ok {
			e = s.X
		}
		if id, ok := e.(*ast.Ident); ok {
			return []string{id.Name}
		}
	case *ast.GenDecl:
		var names []string
		for _, s := range d.Specs {
			switch s := s.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if n.Name != "_" {
						names = append(names, n.Name)
					}
				}
			}
		}
		return names
	}
	return nil
}

// reachedImports follows package-level references from root and returns the
// import paths the reached declarations use, and the reached names.
func reachedImports(decls map[string][]apiDecl, root string) (imports, reached []string) {
	seen := map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		ds, ok := decls[n]
		if seen[n] || !ok {
			continue
		}
		seen[n] = true
		reached = append(reached, n)
		for _, d := range ds {
			queue = append(queue, d.refs...)
			for _, p := range d.imports {
				if !slices.Contains(imports, p) {
					imports = append(imports, p)
				}
			}
		}
	}
	slices.Sort(imports)
	return imports, reached
}

// execPackages start or replace a process.
var execPackages = []string{"os/exec", "syscall", "golang.org/x/sys/unix"}

// The verb's purity rests on api.Marry, and internal/api imports os/exec
// elsewhere. Following package-level references from Marry, no reached
// declaration uses a package that starts a process, and no marvel package it
// reaches depends on os/exec.
func TestMarryVerbReachesNoExec(t *testing.T) {
	decls := apiDecls(t)
	imports, reached := reachedImports(decls, "Marry")
	// Positive control: the reach crosses files (MarryAddress's matcher is in
	// readiness_address.go) and the check finds exec where it is (GitMainRoot
	// runs git).
	if !slices.Contains(reached, "agentAddress") {
		t.Fatalf("the reach from Marry missed agentAddress, so it does not follow references: %v", reached)
	}
	if ctl, _ := reachedImports(decls, "GitMainRoot"); !slices.Contains(ctl, "os/exec") {
		t.Fatalf("the reach from GitMainRoot found no os/exec, so the check proves nothing: %v", ctl)
	}

	for _, p := range imports {
		if slices.Contains(execPackages, p) {
			t.Errorf("a declaration Marry reaches uses %s", p)
		}
		if !strings.HasPrefix(p, "github.com/arcavenae/marvel/") {
			continue
		}
		out, err := exec.Command("go", "list", "-deps", p).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", p, err)
		}
		if slices.Contains(strings.Fields(string(out)), "os/exec") {
			t.Errorf("Marry uses %s, which depends on os/exec", p)
		}
	}
}
