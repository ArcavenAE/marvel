package pty

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This package is the one place the daemon's pty code, and its unsafe pointer,
// live (director ruling 01M88CD904, item 3). It earns that by being a leaf: no
// process, no socket, and none of marvel's wiring packages. The daemon package
// keeps its own ban on unsafe; this guard is what lets the exception sit here
// and nowhere near the daemon's authority.

// forbiddenImport reports why an import is not allowed in this package, or "".
func forbiddenImport(path string) string {
	switch {
	case path == "os/exec":
		return "starts a process"
	case path == "net" || strings.HasPrefix(path, "net/"):
		return "opens a socket or a route to one"
	case strings.HasSuffix(path, "/internal/panemenu") || strings.HasSuffix(path, "/internal/limitact"):
		return "is wiring a leaf must not reach"
	}
	return ""
}

// importViolations lists every forbidden import in the given sources.
func importViolations(files map[string]*ast.File) []string {
	var out []string
	for name, f := range files {
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if why := forbiddenImport(path); why != "" {
				out = append(out, name+" imports "+path+", which "+why)
			}
		}
	}
	return out
}

func parseSources(t *testing.T, sources map[string]string) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
	}
	return files
}

func TestThePtyPackageIsALeaf(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("no pty sources: %v", err)
	}
	sources := map[string]string{}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		sources[n] = string(b)
	}
	if got := importViolations(parseSources(t, sources)); len(got) != 0 {
		t.Errorf("the pty package is not a leaf: %v", got)
	}
}

// Planted imports prove the guard still bites: each forbidden path is found,
// and a clean source is not.
func TestALeafGuardPlantIsFound(t *testing.T) {
	for _, path := range []string{
		"os/exec", "net", "net/http", "net/rpc",
		"github.com/arcavenae/marvel/internal/panemenu",
		"github.com/arcavenae/marvel/internal/limitact",
	} {
		src := "package pty\n\nimport _ \"" + path + "\"\n"
		got := importViolations(parseSources(t, map[string]string{"planted.go": src}))
		if len(got) != 1 || !strings.Contains(got[0], path) {
			t.Errorf("a planted import of %s: violations = %v, want one naming it", path, got)
		}
	}
	clean := "package pty\n\nimport (\n\t\"os\"\n\t\"unsafe\"\n\t\"golang.org/x/sys/unix\"\n)\n"
	if got := importViolations(parseSources(t, map[string]string{"clean.go": clean})); len(got) != 0 {
		t.Errorf("a clean source was refused: %v", got)
	}
}
