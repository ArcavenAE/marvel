package bus

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
// without proving the process first. They are matched by what an identifier
// resolves to, so an alias (import sc "syscall"), a value (k := syscall.Kill)
// and a chain (os.FindProcess(pid).Signal(...)) are all caught, which matching
// on names is not.
var rawSignalFuncs = map[string]bool{
	"syscall.Kill":               true,
	"golang.org/x/sys/unix.Kill": true,
	"(*os.Process).Signal":       true,
	"(*os.Process).Kill":         true,
}

// rawSignals returns the position of every use of a raw signalling function.
func rawSignals(fset *token.FileSet, info *types.Info) []token.Position {
	var out []token.Position
	for id, obj := range info.Uses {
		if fn, ok := obj.(*types.Func); ok && rawSignalFuncs[fn.FullName()] {
			out = append(out, fset.Position(id.Pos()))
		}
	}
	return out
}

// checkFiles type-checks one package's files and returns the uses.
func checkFiles(t *testing.T, fset *token.FileSet, files []*ast.File) *types.Info {
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

// Every signal to the broker goes through childproof's Prober.Signal, which
// re-proves the child first (design section 4.5). A raw syscall.Kill, or a
// signal sent through an os.Process, anywhere in this package's non-test files
// would bypass it, and the process table in the tests could not see it.
func TestBusSignalsOnlyThroughTheProber(t *testing.T) {
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
	if len(files) < 5 {
		t.Fatalf("parsed only %d non-test files; the glob is not reaching the package", len(files))
	}
	var bad []string
	for _, pos := range rawSignals(fset, checkFiles(t, fset, files)) {
		bad = append(bad, pos.String())
	}
	if len(bad) > 0 {
		t.Errorf("signals sent without the prober: %v", bad)
	}
}

// Positive controls: the check sees each way a file can signal a pid without
// the prober, and lets the harmless look-alikes through.
func TestRawSignalCheckSeesEachWayToSignal(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		hits int
	}{
		"syscall.Kill":                {"package p\nimport \"syscall\"\nfunc f() { _ = syscall.Kill(1, 0) }\n", 1},
		"syscall under another name":  {"package p\nimport sc \"syscall\"\nfunc f() { _ = sc.Kill(1, 0) }\n", 1},
		"syscall.Kill as a value":     {"package p\nimport \"syscall\"\nvar k = syscall.Kill\n", 1},
		"a Process.Signal call":       {"package p\nimport \"os\"\nfunc f(c *os.Process) { _ = c.Signal(nil) }\n", 1},
		"a Process.Kill call":         {"package p\nimport \"os\"\nfunc f(c *os.Process) { _ = c.Kill() }\n", 1},
		"FindProcess then Signal":     {"package p\nimport \"os\"\nfunc f(pid int) { p, _ := os.FindProcess(pid); _ = p.Signal(nil) }\n", 1},
		"FindProcess chained":         {"package p\nimport \"os\"\nfunc f(pid int) { _ = (func() *os.Process { p, _ := os.FindProcess(pid); return p })().Signal(nil) }\n", 1},
		"a Cmd's Process.Signal":      {"package p\nimport \"os/exec\"\nfunc f(c *exec.Cmd) { _ = c.Process.Signal(nil) }\n", 1},
		"syscall.SIGHUP is no signal": {"package p\nimport \"syscall\"\nvar x = syscall.SIGHUP\n", 0},
		"a method named Kill":         {"package p\ntype P struct{}\nfunc (P) Kill() {}\nfunc f(p P) { p.Kill() }\n", 0},
		"syscall.Getpid":              {"package p\nimport \"syscall\"\nvar x = syscall.Getpid()\n", 0},
	} {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", tc.src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(rawSignals(fset, checkFiles(t, fset, []*ast.File{f}))); got != tc.hits {
				t.Errorf("%d raw signals found, want %d", got, tc.hits)
			}
		})
	}
}
