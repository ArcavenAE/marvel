package api

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

// marryReach is what the marrying step reaches in one package: the files of
// the reached declarations, and for each package a reached declaration uses,
// the declarations that use it.
type marryReach struct {
	files []string
	uses  map[string][]string
}

// reachFrom follows package-level references from roots through the
// non-test Go files of dir. Files are named under fileRoot.
func reachFrom(t *testing.T, dir, fileRoot string, roots []string) marryReach {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no Go files in %s (%v)", dir, err)
	}
	type decl struct {
		file, name string
		refs       []string
		pkgs       []string
	}
	byName := map[string][]*decl{}
	fset := token.NewFileSet()
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		imports := map[string]string{}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = path
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
			refs, pkgs := referencedNames(d, imports)
			for _, n := range names {
				if n == "" || n == "_" {
					continue
				}
				byName[n] = append(byName[n], &decl{
					file: filepath.ToSlash(filepath.Join(fileRoot, filepath.Base(p))),
					name: n, refs: refs, pkgs: pkgs,
				})
			}
		}
	}
	r := marryReach{uses: map[string][]string{}}
	reached := map[string]bool{}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if reached[n] || byName[n] == nil {
			continue
		}
		reached[n] = true
		for _, d := range byName[n] {
			if !slices.Contains(r.files, d.file) {
				r.files = append(r.files, d.file)
			}
			for _, p := range d.pkgs {
				if !slices.Contains(r.uses[p], d.name) {
					r.uses[p] = append(r.uses[p], d.name)
				}
			}
			queue = append(queue, d.refs...)
		}
	}
	slices.Sort(r.files)
	return r
}

// referencedNames returns the identifiers in d that can name a package-level
// declaration, and the import paths d uses. A selector's field or method name,
// a struct field's name and a composite literal's key name members of a type,
// not package-level names, so they are skipped.
func referencedNames(d ast.Decl, imports map[string]string) (refs, pkgs []string) {
	skip := map[*ast.Ident]bool{}
	ast.Inspect(d, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			skip[n.Sel] = true
			if id, ok := n.X.(*ast.Ident); ok {
				if path, ok := imports[id.Name]; ok {
					skip[id] = true
					if !slices.Contains(pkgs, path) {
						pkgs = append(pkgs, path)
					}
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
				refs = append(refs, n.Name)
			}
		}
		return true
	})
	return refs, pkgs
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

// marryReachAllowed are the packages a declaration the marrying step reaches
// may use. A logger, a store or anything that starts a process is not among
// them, wherever in the file the rest of the package uses one.
var marryReachAllowed = []string{
	"encoding/json", "errors", "fmt", "regexp", "regexp/syntax", "slices", "sort", "strings", "time",
	"github.com/arcavenae/marvel/contracts/go/envelopeschema",
	"github.com/arcavenae/marvel/internal/asof",
}

// processPackages start or replace a process.
var processPackages = []string{"os/exec", "syscall", "golang.org/x/sys/unix"}

// marryReachViolations lists each package a reached declaration uses that is
// not allowed, with the declarations that use it.
func marryReachViolations(r marryReach, allowed []string) []string {
	var out []string
	for p, decls := range r.uses {
		if !slices.Contains(allowed, p) {
			slices.Sort(decls)
			out = append(out, p+" (used by "+strings.Join(decls, ", ")+")")
		}
	}
	slices.Sort(out)
	return out
}

// Every declaration the marrying step reaches uses only allowed packages,
// checked per declaration: readiness.go imports a logger and a store for
// other declarations, and a reached one using either would fail here.
func TestMarryReachUsesOnlyAllowedPackages(t *testing.T) {
	t.Parallel()
	r := reachFrom(t, ".", "internal/api", marryEntryPoints)
	if len(r.uses) == 0 {
		t.Fatal("the reach uses no package, so the check read nothing")
	}
	for _, v := range marryReachViolations(r, marryReachAllowed) {
		t.Errorf("a declaration the marrying step reaches uses %s", v)
	}
}

// No package a reached declaration uses depends on a package that starts a
// process. syscall is checked only as a direct use: time and os depend on it
// on every platform.
func TestMarryReachDependsOnNoProcessPackage(t *testing.T) {
	t.Parallel()
	r := reachFrom(t, ".", "internal/api", marryEntryPoints)
	for p := range r.uses {
		out, err := exec.Command("go", "list", "-deps", p).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", p, err)
		}
		deps := strings.Fields(string(out))
		if len(deps) == 0 {
			t.Fatalf("go list -deps %s named nothing", p)
		}
		for _, bad := range []string{"os/exec", "golang.org/x/sys/unix"} {
			if slices.Contains(deps, bad) {
				t.Errorf("the marrying step uses %s, which depends on %s", p, bad)
			}
		}
	}
}

// The walk must see through what a syntactic reading misses. Each fixture
// package's Marry reaches a forbidden package by one route, and the walk
// must find it.
func TestMarryReachFollowsEachRoute(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"dispatch":   "os/exec", // an interface method whose implementation an init installs
		"setter":     "os/exec", // an implementation installed only from outside
		"mapkey":     "os/exec", // a package-level value used only as a map-literal key
		"initfunc":   "os/exec", // a function value an init assigns
		"declimport": "log",     // a reached declaration's own use, not its file's
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join("testdata", "marryreach", name)
			r := reachFrom(t, dir, dir, []string{"Marry"})
			if _, ok := r.uses[want]; !ok {
				t.Errorf("the walk from Marry did not reach %s; it found %v", want, r.uses)
			}
		})
	}
}
