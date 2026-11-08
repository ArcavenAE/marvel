package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// workWords are names marvel does not declare: work-queue and lease vocabulary
// belongs to director, which holds the marrying step and the routing rule.
var workWords = []string{"Lease", "MarryStop", "MarryItem"}

// workCommand is the command text marvel does not read: it has no work queue.
const workCommand = "bd ready"

// workVocabularyViolations lists, for the non-test Go files under root, each
// declaration of a work word and each string literal that holds the work
// command. Files ending in _test.go and any testdata directory are skipped.
func workVocabularyViolations(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		report := func(pos token.Pos, what string) {
			out = append(out, fmt.Sprintf("%s: %s", fset.Position(pos), what))
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeSpec:
				if slices.Contains(workWords, n.Name.Name) {
					report(n.Pos(), "declares type "+n.Name.Name)
				}
			case *ast.ValueSpec:
				for _, id := range n.Names {
					if slices.Contains(workWords, id.Name) {
						report(id.Pos(), "declares "+id.Name)
					}
				}
			case *ast.FuncDecl:
				if n.Recv == nil && slices.Contains(workWords, n.Name.Name) {
					report(n.Pos(), "declares func "+n.Name.Name)
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING && strings.Contains(n.Value, workCommand) {
					report(n.Pos(), "reads "+workCommand)
				}
			}
			return true
		})
		return nil
	})
	sort.Strings(out)
	return out, err
}

// workVocabularyRoot is the directory the guard walks: the module root, so the
// commands under cmd/ are held to it as well as the packages under internal/.
func workVocabularyRoot() string { return ".." }

// The guard walks the module root, not internal/ alone.
func TestWorkVocabularyRootIsModuleRoot(t *testing.T) {
	root := workVocabularyRoot()
	for _, rel := range []string{"go.mod", filepath.Join("cmd", "marvel"), filepath.Join("internal", "api")} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("guard root %q lacks %s: %v", root, rel, err)
		}
	}
}

// Marvel's non-test Go, under cmd/ and internal/, declares no work word and
// never names the work command.
func TestMarvelHoldsNoWorkVocabulary(t *testing.T) {
	got, err := workVocabularyViolations(workVocabularyRoot())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("marvel holds work vocabulary:\n%s", strings.Join(got, "\n"))
	}
}

// The check flags each kind of violation in a planted tree, and passes clean
// code, test files, testdata and comments.
func TestWorkVocabularyCheckFlagsWhatItShould(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a/lease.go", "package a\n\ntype Lease struct{}\n")
	write("a/item.go", "package a\n\ntype MarryItem struct{ ID string }\n")
	write("a/stop.go", "package a\n\nconst MarryStop = \"stop\"\n")
	write("a/fn.go", "package a\n\nfunc Lease() {}\n")
	write("a/cmd.go", "package a\n\nvar c = []string{\"bd ready\", \"--json\"}\n")
	write("internal/x/split.go", "package x\n\nimport \"os/exec\"\n\nvar _ = exec.Command(\"bd\", \"ready\")\n")
	write("cmd/marvel/lit.go", "package main\n\nvar c = \"bd ready\"\n")
	write("cmd/marvel/argv.go", "package main\n\nvar argv = []string{\"bd\", \"ready\", \"--json\"}\n")
	write("b/clean.go", "package b\n\n// Lease and bd ready are named in this comment only.\ntype Seat struct{}\n\nfunc (Seat) Lease() {}\n")
	write("b/clean_test.go", "package b\n\ntype Lease struct{}\n\nvar c = \"bd ready\"\n")
	write("b/split_ok.go", "package b\n\nvar _ = []string{\"bd\", \"list\"}\n\nfunc f(a, b string) {}\n\nvar _ = f(\"ready\", \"bd\")\n")
	write("b/testdata/x/x.go", "package x\n\ntype Lease struct{}\n")

	got, err := workVocabularyViolations(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"a/lease.go": false, "a/item.go": false, "a/stop.go": false, "a/fn.go": false, "a/cmd.go": false,
		"internal/x/split.go": false, "cmd/marvel/lit.go": false, "cmd/marvel/argv.go": false,
	}
	for _, line := range got {
		matched := false
		for f := range want {
			if strings.Contains(line, filepath.FromSlash(f)) {
				want[f], matched = true, true
			}
		}
		if !matched {
			t.Errorf("flagged something it should not have: %s", line)
		}
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("did not flag %s; got %v", f, got)
		}
	}
}
