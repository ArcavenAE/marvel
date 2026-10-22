package bus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// childStartFuncs are the functions and methods that start a child process.
// Like rawSignalFuncs they are matched by what an identifier resolves to, so
// an alias, a value and a chain are all caught.
var childStartFuncs = map[string]bool{
	"os/exec.Command":                true,
	"os/exec.CommandContext":         true,
	"(*os/exec.Cmd).Start":           true,
	"(*os/exec.Cmd).Run":             true,
	"(*os/exec.Cmd).Output":          true,
	"(*os/exec.Cmd).CombinedOutput":  true,
	"os.StartProcess":                true,
	"syscall.ForkExec":               true,
	"syscall.StartProcess":           true,
	"golang.org/x/sys/unix.ForkExec": true,
}

// allowedChildStarts are the child starts a non-test file in this package may
// have, each as "<enclosing func> <function>": binaryVersion's one
// `<bin> --version` run, which reads the version token and never serves. The
// broker itself starts through workload.Process.
var allowedChildStarts = []string{
	"binaryVersion os/exec.Command",
	"binaryVersion (*os/exec.Cmd).Output",
}

// childStarts returns "<enclosing func> <function>" for every use of a
// child-starting function in files, with its position.
func childStarts(fset *token.FileSet, files []*ast.File, info *types.Info) map[string][]token.Position {
	out := map[string][]token.Position{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if fn, ok := info.Uses[id].(*types.Func); ok && childStartFuncs[fn.FullName()] {
					key := fd.Name.Name + " " + fn.FullName()
					out[key] = append(out[key], fset.Position(id.Pos()))
				}
				return true
			})
		}
		// A use outside any function (a package-level value) is a start too.
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			ast.Inspect(gd, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if fn, ok := info.Uses[id].(*types.Func); ok && childStartFuncs[fn.FullName()] {
					key := "<package level> " + fn.FullName()
					out[key] = append(out[key], fset.Position(id.Pos()))
				}
				return true
			})
		}
	}
	return out
}

// The broker is a managed child of workload.Process, which holds the one spawn
// path and its allowlisted environment (aae-orc-oo62t). No non-test file in
// this package starts a child except binaryVersion's `--version` run.
func TestBusStartsNoChildExceptTheVersionProbe(t *testing.T) {
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
	seen := map[string]int{}
	for key, poss := range childStarts(fset, files, checkFiles(t, fset, files)) {
		seen[key] = len(poss)
		if !slices.Contains(allowedChildStarts, key) || len(poss) != 1 {
			for _, p := range poss {
				bad = append(bad, key+" at "+p.String())
			}
		}
	}
	for _, key := range allowedChildStarts {
		if seen[key] != 1 {
			t.Errorf("the allowed child start %q appears %d times, want 1: the allowlist is stale", key, seen[key])
		}
	}
	slices.Sort(bad)
	if len(bad) > 0 {
		t.Errorf("a child is started outside workload.Process: %v", bad)
	}
}

// Positive controls: the check sees each way a file can start a child, and
// lets the look-alikes through.
func TestChildStartCheckSeesEachWayToStartAChild(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		hits int
	}{
		"exec.Command":           {"package p\nimport \"os/exec\"\nfunc f() { _ = exec.Command(\"x\") }\n", 1},
		"under another name":     {"package p\nimport e \"os/exec\"\nfunc f() { _ = e.Command(\"x\") }\n", 1},
		"as a value":             {"package p\nimport \"os/exec\"\nvar c = exec.Command\n", 1},
		"CommandContext":         {"package p\nimport (\"context\"; \"os/exec\")\nfunc f(ctx context.Context) { _ = exec.CommandContext(ctx, \"x\") }\n", 1},
		"Cmd.Start":              {"package p\nimport \"os/exec\"\nfunc f(c *exec.Cmd) { _ = c.Start() }\n", 1},
		"Cmd.Run":                {"package p\nimport \"os/exec\"\nfunc f(c *exec.Cmd) { _ = c.Run() }\n", 1},
		"Cmd.Output":             {"package p\nimport \"os/exec\"\nfunc f(c *exec.Cmd) { _, _ = c.Output() }\n", 1},
		"Cmd.CombinedOutput":     {"package p\nimport \"os/exec\"\nfunc f(c *exec.Cmd) { _, _ = c.CombinedOutput() }\n", 1},
		"os.StartProcess":        {"package p\nimport \"os\"\nfunc f() { _, _ = os.StartProcess(\"x\", nil, nil) }\n", 1},
		"syscall.ForkExec":       {"package p\nimport \"syscall\"\nfunc f() { _, _ = syscall.ForkExec(\"x\", nil, nil) }\n", 1},
		"LookPath is no start":   {"package p\nimport \"os/exec\"\nfunc f() { _, _ = exec.LookPath(\"x\") }\n", 0},
		"a type named Cmd":       {"package p\nimport \"os/exec\"\nfunc f(c *exec.Cmd) { _ = c.Env }\n", 0},
		"a method named Start":   {"package p\ntype P struct{}\nfunc (P) Start() {}\nfunc f(p P) { p.Start() }\n", 0},
		"a func named Command":   {"package p\nfunc Command() {}\nfunc f() { Command() }\n", 0},
		"two starts in one func": {"package p\nimport \"os/exec\"\nfunc f() { _ = exec.Command(\"x\"); _ = exec.Command(\"y\") }\n", 2},
	} {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", tc.src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files := []*ast.File{f}
			got := 0
			for _, poss := range childStarts(fset, files, checkFiles(t, fset, files)) {
				got += len(poss)
			}
			if got != tc.hits {
				t.Errorf("%d child starts found, want %d", got, tc.hits)
			}
		})
	}
}
