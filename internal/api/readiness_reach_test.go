package api

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// marryReach is what the marrying step reaches in one package: the files of
// the reached declarations; for each package a reached declaration uses, the
// declarations that use it; and the same for each member of another package
// (fmt.Errorf, for one).
type marryReach struct {
	files   []string
	uses    map[string][]string
	members map[string][]string
}

// reachFrom follows references from roots through the type-checked non-test
// Go files of dir, and names files under fileRoot. It follows objects, not
// names, so a struct field's key is a field and a map literal's key is the
// value it names. A reached type brings every method declared on it, since a
// caller outside the package (encoding/json, fmt) may call any of them. A
// reached method of an interface declared in the package brings that method
// of every type in the package that implements the interface. A reached
// package variable brings every declaration that mentions it, init included:
// a write can come through the bare name, a field, an element, a pointer or a
// copy of a map or slice, so any mention counts, reads too.
//
// The limit: it reads one package. An implementation or function value that
// another package supplies, and anything reached through reflection, are not
// followed; TestMarryReachDependsOnNoProcessPackage covers the packages the
// reached declarations import, not values they are handed.
func reachFrom(t *testing.T, dir, fileRoot string, roots []string) marryReach {
	t.Helper()
	r, err := reachIn(dir, fileRoot, roots)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// reachIn is reachFrom without a test, for a walk shared between tests.
func reachIn(dir, fileRoot string, roots []string) (marryReach, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(paths) == 0 {
		return marryReach{}, fmt.Errorf("no Go files in %s (%v)", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return marryReach{}, err
		}
		files = append(files, f)
	}
	pkgPath := "marryreach/" + filepath.Base(dir)
	if dir == "." {
		pkgPath = "github.com/arcavenae/marvel/internal/api"
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(pkgPath, fset, files, info)
	if err != nil {
		return marryReach{}, fmt.Errorf("type-check %s: %w", dir, err)
	}

	type decl struct {
		file    string
		name    string
		uses    []types.Object
		ifaces  []*types.Func
		pkgs    []string
		members []string
	}
	defined := map[types.Object]*decl{}
	methods := map[*types.TypeName][]*decl{}
	mentions := map[types.Object][]*decl{}
	packageLevel := func(o types.Object) bool { return o.Pkg() == pkg && o.Parent() == pkg.Scope() }
	for _, f := range files {
		file := filepath.ToSlash(filepath.Join(fileRoot, filepath.Base(fset.Position(f.Pos()).Filename)))
		for _, d := range f.Decls {
			n := &decl{file: file}
			switch d := d.(type) {
			case *ast.FuncDecl:
				n.name = d.Name.Name
				obj := info.Defs[d.Name]
				defined[obj] = n
				if recv := obj.Type().(*types.Signature).Recv(); recv != nil {
					rt := recv.Type()
					if p, ok := rt.(*types.Pointer); ok {
						rt = p.Elem()
					}
					if named, ok := rt.(*types.Named); ok {
						tn := named.Obj()
						methods[tn] = append(methods[tn], n)
						n.name = tn.Name() + "." + n.name
					}
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						defined[info.Defs[s.Name]] = n
						n.name = s.Name.Name
					case *ast.ValueSpec:
						for _, id := range s.Names {
							if o := info.Defs[id]; o != nil {
								defined[o] = n
								n.name = id.Name
							}
						}
					}
				}
			}
			ast.Inspect(d, func(x ast.Node) bool {
				switch x := x.(type) {
				case *ast.Ident:
					o := info.Uses[x]
					if o == nil {
						break
					}
					if pn, ok := o.(*types.PkgName); ok {
						if p := pn.Imported().Path(); !slices.Contains(n.pkgs, p) {
							n.pkgs = append(n.pkgs, p)
						}
						break
					}
					if o.Pkg() != pkg {
						// A member of another package, such as fmt.Println.
						if o.Pkg() != nil && o.Parent() == o.Pkg().Scope() {
							if m := o.Pkg().Path() + "." + o.Name(); !slices.Contains(n.members, m) {
								n.members = append(n.members, m)
							}
						}
						break
					}
					if fn, ok := o.(*types.Func); ok {
						if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
							if types.IsInterface(recv.Type()) {
								n.ifaces = append(n.ifaces, fn)
							} else {
								n.uses = append(n.uses, fn)
							}
							break
						}
					}
					if packageLevel(o) {
						n.uses = append(n.uses, o)
						if v, ok := o.(*types.Var); ok {
							mentions[v] = append(mentions[v], n)
						}
					}
				}
				return true
			})
		}
	}

	// implementers returns that method of every package type implementing the
	// interface m belongs to.
	implementers := func(m *types.Func) []*decl {
		iface, _ := m.Type().(*types.Signature).Recv().Type().Underlying().(*types.Interface)
		if iface == nil {
			return nil
		}
		var out []*decl
		for _, name := range pkg.Scope().Names() {
			tn, ok := pkg.Scope().Lookup(name).(*types.TypeName)
			if !ok || types.IsInterface(tn.Type()) {
				continue
			}
			if !types.Implements(tn.Type(), iface) && !types.Implements(types.NewPointer(tn.Type()), iface) {
				continue
			}
			o, _, _ := types.LookupFieldOrMethod(tn.Type(), true, pkg, m.Name())
			if d := defined[o]; d != nil {
				out = append(out, d)
			}
		}
		return out
	}

	r := marryReach{uses: map[string][]string{}, members: map[string][]string{}}
	seen := map[*decl]bool{}
	var queue []*decl
	for _, name := range roots {
		if d := defined[pkg.Scope().Lookup(name)]; d != nil {
			queue = append(queue, d)
		} else {
			return marryReach{}, fmt.Errorf("%s: no package-level %s", dir, name)
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		if !slices.Contains(r.files, n.file) {
			r.files = append(r.files, n.file)
		}
		for _, p := range n.pkgs {
			if !slices.Contains(r.uses[p], n.name) {
				r.uses[p] = append(r.uses[p], n.name)
			}
		}
		for _, m := range n.members {
			if !slices.Contains(r.members[m], n.name) {
				r.members[m] = append(r.members[m], n.name)
			}
		}
		for _, o := range n.uses {
			if d := defined[o]; d != nil {
				queue = append(queue, d)
			}
			if tn, ok := o.(*types.TypeName); ok {
				queue = append(queue, methods[tn]...)
			}
			queue = append(queue, mentions[o]...)
		}
		for _, m := range n.ifaces {
			queue = append(queue, implementers(m)...)
		}
	}
	slices.Sort(r.files)
	return r, nil
}

var (
	marrySourceOnce sync.Once
	marrySource     marryReach
	marrySourceErr  error
)

// marrySourceReach is the walk of this package from the entry points,
// computed once per test binary: type-checking from source takes seconds.
func marrySourceReach(t *testing.T) marryReach {
	t.Helper()
	marrySourceOnce.Do(func() {
		marrySource, marrySourceErr = reachIn(".", "internal/api", marryEntryPoints)
	})
	if marrySourceErr != nil {
		t.Fatal(marrySourceErr)
	}
	return marrySource
}

// marryReachAllowed are the packages a declaration the marrying step reaches
// may use. A logger, a store or anything that starts a process is not among
// them, wherever in the file the rest of the package uses one.
var marryReachAllowed = []string{
	"encoding/json", "errors", "fmt", "regexp", "regexp/syntax", "slices", "strings", "time",
	"github.com/arcavenae/marvel/contracts/go/envelopeschema",
	"github.com/arcavenae/marvel/internal/asof",
}

// marryReachNarrow are allowed packages of which a reached declaration may
// use only the members named: fmt can write to stdout, so only the
// formatting functions the step uses today are allowed.
var marryReachNarrow = map[string][]string{
	"fmt": {"Errorf", "Sprintf"},
}

// marryReachViolations lists each package a reached declaration uses that is
// not allowed, and each member of a narrowed package outside its list, with
// the declarations that use it.
func marryReachViolations(r marryReach, allowed []string) []string {
	var out []string
	for p, decls := range r.uses {
		if !slices.Contains(allowed, p) {
			slices.Sort(decls)
			out = append(out, p+" (used by "+strings.Join(decls, ", ")+")")
		}
	}
	for m, decls := range r.members {
		i := strings.LastIndex(m, ".")
		names, narrow := marryReachNarrow[m[:i]]
		if narrow && !slices.Contains(names, m[i+1:]) {
			slices.Sort(decls)
			out = append(out, m+" (used by "+strings.Join(decls, ", ")+")")
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
	r := marrySourceReach(t)
	if len(r.uses) == 0 {
		t.Fatal("the reach uses no package, so the check read nothing")
	}
	for _, v := range marryReachViolations(r, marryReachAllowed) {
		t.Errorf("a declaration the marrying step reaches uses %s", v)
	}
}

// No package a reached declaration uses depends on a package that starts a
// process: os/exec or x/sys/unix. syscall is refused as a direct use by
// marryReachAllowed, but not here, since time and os depend on it.
func TestMarryReachDependsOnNoProcessPackage(t *testing.T) {
	t.Parallel()
	r := marrySourceReach(t)
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
		"method":     "os/exec", // a method of a reached type, called from outside
		"field":      "os/exec", // an init sets a field of a reached variable
		"mapelem":    "os/exec", // an init sets an element of a reached map
		"pointer":    "os/exec", // an init writes a reached variable through a pointer
		"alias":      "os/exec", // an init writes a reached map through a copy of it
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
