package tmux

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Unguarded is the one way past the suspend gate, so every place that calls it is
// listed here and a new one shows in review, the way childproof's Spawned
// allowlist does. The key is the file and the function that holds the call.
func TestEveryUnguardedCallSiteIsListed(t *testing.T) {
	want := map[string]int{
		// The collector reads the server pid and the pane pids while the hold is
		// already up, before the server is stopped.
		"internal/daemon/suspend.go (*Daemon).productionCollector": 1,
	}
	root := filepath.Join("..", "..")
	got := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			switch de.Name() {
			case ".git", "vendor", "node_modules", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, path)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				name = "(" + exprName(fd.Recv.List[0].Type) + ")." + name
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Unguarded" {
					got[filepath.ToSlash(rel)+" "+name]++
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The definition lives in this package; its own method is not a call site.
	delete(got, "internal/tmux/driver.go (*Driver).Unguarded")
	keys := map[string]bool{}
	for k := range got {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	var all []string
	for k := range keys {
		all = append(all, k)
	}
	sort.Strings(all)
	for _, k := range all {
		if got[k] != want[k] {
			t.Errorf("%s calls Unguarded %d time(s), listed as %d", k, got[k], want[k])
		}
	}
}

func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return "*" + exprName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return "?"
}
