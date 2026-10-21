package workload

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawSignalFuncs are the functions and methods that send a signal to a pid
// without proving the process first, matched by what an identifier resolves
// to (the same set as internal/bus signal_source_test.go).
var rawSignalFuncs = map[string]bool{
	"syscall.Kill":               true,
	"golang.org/x/sys/unix.Kill": true,
	"(*os.Process).Signal":       true,
	"(*os.Process).Kill":         true,
}

// allowedRawSignal is the one raw signal this package may send: Start kills
// the child it has just started, not yet reaped, when BeforePidFile fails.
// Its pid cannot have been reused, so no proof is needed (process.go, Start).
const allowedRawSignal = "Start (*os.Process).Kill"

// rawSignal is one use of a raw signalling function and the top-level
// function it sits in.
type rawSignal struct {
	pos  token.Position
	fn   string
	name string
}

func (r rawSignal) key() string { return r.fn + " " + r.name }

// rawSignals returns every use of a raw signalling function in files.
func rawSignals(fset *token.FileSet, files []*ast.File, info *types.Info) []rawSignal {
	var out []rawSignal
	for id, obj := range info.Uses {
		fn, ok := obj.(*types.Func)
		if !ok || !rawSignalFuncs[fn.FullName()] {
			continue
		}
		r := rawSignal{pos: fset.Position(id.Pos()), name: fn.FullName()}
		for _, f := range files {
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Pos() <= id.Pos() && id.Pos() < fd.End() {
					r.fn = fd.Name.Name
				}
			}
		}
		out = append(out, r)
	}
	return out
}

func typeCheck(t *testing.T, fset *token.FileSet, files []*ast.File) *types.Info {
	t.Helper()
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
		Error:    func(err error) { t.Errorf("type error: %v", err) },
	}
	if _, err := conf.Check("p", fset, files, info); err != nil {
		t.Fatalf("type-checking the package: %v", err)
	}
	return info
}

// Every signal Process sends goes through childproof's Prober.Signal, which
// re-proves the child first (restructure plan, Move 2, "The gate checks"). The
// only raw signal allowed in this package's non-test files is Start's kill of
// its own unreaped child.
func TestWorkloadSignalsOnlyThroughTheProber(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) < 2 {
		t.Fatalf("parsed only %d non-test files; the glob is not reaching the package", len(files))
	}
	allowed := 0
	for _, r := range rawSignals(fset, files, typeCheck(t, fset, files)) {
		if r.key() == allowedRawSignal && allowed == 0 {
			allowed++
			continue
		}
		t.Errorf("%s: %s in %s sends a signal without the prober", r.pos, r.name, r.fn)
	}
	if allowed != 1 {
		t.Errorf("the allowed kill in Start was not found (%d); the check is not reading process.go", allowed)
	}
}

// Positive controls: the check sees a raw signal anywhere but the allowed
// site, and sees a second kill inside Start.
func TestWorkloadRawSignalCheckSeesEachSite(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want []string
	}{
		"the allowed kill":            {"package p\nimport \"os\"\nfunc Start(c *os.Process) { _ = c.Kill() }\n", []string{allowedRawSignal}},
		"a kill outside Start":        {"package p\nimport \"os\"\nfunc stop(c *os.Process) { _ = c.Kill() }\n", []string{"stop (*os.Process).Kill"}},
		"syscall.Kill in Start":       {"package p\nimport \"syscall\"\nfunc Start() { _ = syscall.Kill(1, 0) }\n", []string{"Start syscall.Kill"}},
		"a Signal in Start":           {"package p\nimport \"os\"\nfunc Start(c *os.Process) { _ = c.Signal(nil) }\n", []string{"Start (*os.Process).Signal"}},
		"syscall.SIGTERM is a number": {"package p\nimport \"syscall\"\nvar x = syscall.SIGTERM\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", tc.src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files := []*ast.File{f}
			var got []string
			for _, r := range rawSignals(fset, files, typeCheck(t, fset, files)) {
				got = append(got, r.key())
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("found %v, want %v", got, tc.want)
			}
		})
	}
}
