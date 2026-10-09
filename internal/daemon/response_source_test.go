package daemon

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Redaction cannot be skipped if only respond builds a response body. This test
// reads every non-test package of the module and fails on anything else that
// sets daemon.Response's Result: a composite literal that sets it (keyed or
// positional), or an assignment to it (or taking its address). Matching is on
// the Response type, so the unrelated Result fields in other packages do not
// trip it. A structural check, so it may gate (diagnostic-not-gate.md).

const (
	modulePath   = "github.com/arcavenae/marvel"
	daemonPath   = modulePath + "/internal/daemon"
	allowedWrite = "respond"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// isResponse reports whether t is daemon.Response or a pointer to it.
func isResponse(t types.Type) bool {
	if t == nil {
		return false
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	return n.Obj().Name() == "Response" && n.Obj().Pkg().Path() == daemonPath
}

// responseWrites type-checks files as package pkgPath and returns one line for
// each write to Response.Result outside the allowed function of package daemon.
// A tolerant check goes on past type errors, which a planted positional literal
// raises (it lists too few fields) without losing the literal's type.
func responseWrites(fset *token.FileSet, pkgPath string, files []*ast.File, imp types.Importer, tolerant bool) ([]string, error) {
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	var firstErr error
	conf := types.Config{
		Importer: imp,
		Error: func(err error) {
			if firstErr == nil {
				firstErr = err
			}
		},
	}
	_, _ = conf.Check(pkgPath, fset, files, info)
	if firstErr != nil && !tolerant {
		return nil, fmt.Errorf("type-check %s: %w", pkgPath, firstErr)
	}

	var out []string
	flag := func(pos token.Pos, what string) {
		out = append(out, fmt.Sprintf("%s: %s", fset.Position(pos), what))
	}
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, isFunc := decl.(*ast.FuncDecl)
			if isFunc && pkgPath == daemonPath && fd.Name.Name == allowedWrite && fd.Recv == nil {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CompositeLit:
					if !isResponse(info.TypeOf(v)) {
						return true
					}
					for _, el := range v.Elts {
						kv, keyed := el.(*ast.KeyValueExpr)
						if !keyed {
							flag(el.Pos(), "positional Response literal sets Result")
							continue
						}
						if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Result" {
							flag(kv.Pos(), "Response literal sets Result")
						}
					}
				case *ast.AssignStmt:
					for _, lhs := range v.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Result" && isResponse(info.TypeOf(sel.X)) {
							flag(sel.Pos(), "assignment to Response.Result")
						}
					}
				case *ast.UnaryExpr:
					if sel, ok := v.X.(*ast.SelectorExpr); ok && v.Op == token.AND && sel.Sel.Name == "Result" && isResponse(info.TypeOf(sel.X)) {
						flag(sel.Pos(), "address of Response.Result taken")
					}
				}
				return true
			})
		}
	}
	return out, nil
}

// packageFiles parses the files of dir that build on this platform, never tests.
func packageFiles(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
	t.Helper()
	pkg, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		if _, none := err.(*build.NoGoError); none {
			return nil
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []*ast.File
	for _, name := range pkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	return files
}

func importsDaemon(files []*ast.File) bool {
	for _, f := range files {
		for _, im := range f.Imports {
			if strings.Trim(im.Path.Value, `"`) == daemonPath {
				return true
			}
		}
	}
	return false
}

// Only respond sets Response.Result, in every non-test package of the module,
// because Response is exported.
func TestOnlyRespondSetsResponseResult(t *testing.T) {
	root := moduleRoot(t)
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "_kos", "testdata", "vendor", "node_modules":
				return filepath.SkipDir
			}
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil)
	checked := 0
	for _, dir := range dirs {
		files := packageFiles(t, fset, dir)
		rel, _ := filepath.Rel(root, dir)
		pkgPath := modulePath
		if rel != "." {
			pkgPath += "/" + filepath.ToSlash(rel)
		}
		if len(files) == 0 || (pkgPath != daemonPath && !importsDaemon(files)) {
			continue
		}
		checked++
		bad, err := responseWrites(fset, pkgPath, files, imp, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bad {
			t.Errorf("%s", line)
		}
	}
	if checked < 2 {
		t.Fatalf("checked %d packages; the walk lost the daemon and its importers", checked)
	}
}

// Positive control: the checker catches each form when it is planted, so a
// pass above means the code is clean and not that the checker is blind.
func TestResponseSourceCheckCatchesPlantedWrites(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil)

	planted := func(name, src string) *ast.File {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("inside package daemon", func(t *testing.T) {
		files := append(packageFiles(t, fset, filepath.Join(root, "internal", "daemon")), planted("scratch_daemon.go", `package daemon

func plantedKeyed() Response { return Response{Result: nil} }
func plantedPositional() Response { return Response{nil} }
func plantedAssign(r Response) Response { r.Result = nil; return r }
func plantedPointerAssign(r *Response) { r.Result = nil }
func plantedAddress(r *Response) *[]byte { return (*[]byte)(&r.Result) }
`))
		bad, err := responseWrites(fset, daemonPath, files, imp, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"Response literal sets Result",
			"positional Response literal sets Result",
			"assignment to Response.Result",
			"address of Response.Result taken",
		} {
			if !anyLineHas(bad, "scratch_daemon.go", want) {
				t.Errorf("no planted write of the form %q was caught: %v", want, bad)
			}
		}
		if n := countLinesIn(bad, "scratch_daemon.go"); n != 5 {
			t.Errorf("caught %d planted writes in package daemon, want 5: %v", n, bad)
		}
		if n := countLinesIn(bad, "internal/daemon/"); n != 0 {
			t.Errorf("the real daemon code has %d writes outside respond: %v", n, bad)
		}
	})

	t.Run("from another package", func(t *testing.T) {
		files := []*ast.File{planted("scratch_other.go", `package other

import "github.com/arcavenae/marvel/internal/daemon"

func keyed() daemon.Response { return daemon.Response{Result: nil} }
func positional() daemon.Response { return daemon.Response{nil} }
func assign(r *daemon.Response) { r.Result = nil }
`)}
		bad, err := responseWrites(fset, modulePath+"/scratch/other", files, imp, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(bad) != 3 {
			t.Errorf("caught %d of 3 planted writes from another package: %v", len(bad), bad)
		}
	})

	t.Run("an unrelated Result field is left alone", func(t *testing.T) {
		files := []*ast.File{planted("scratch_unrelated.go", `package other

type Row struct{ Result string }

func set(r *Row) { r.Result = "x"; _ = Row{Result: "y"} }
`)}
		bad, err := responseWrites(fset, modulePath+"/scratch/unrelated", files, imp, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(bad) != 0 {
			t.Errorf("a Result field on another type was flagged: %v", bad)
		}
	})
}

func anyLineHas(lines []string, file, what string) bool {
	for _, l := range lines {
		if strings.Contains(l, file) && strings.HasSuffix(l, what) {
			return true
		}
	}
	return false
}

func countLinesIn(lines []string, file string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, file) {
			n++
		}
	}
	return n
}
