package team

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Test 5: the limited condition is restart-neutral. The reconciler, restart
// path and shift code must not read it, so a limited session is never
// restarted, killed or shifted because of it. This is a guard over the source
// of the package, not a behavior test: the claim is that nothing reads the
// field, and the way to hold that is to fail when something starts to.
func TestNothingInTeamReadsTheLimitedCondition(t *testing.T) {
	t.Parallel()
	banned := map[string]bool{"Condition": true, "ConditionLimited": true, "Limit": true, "LimitProvenance": true, "EvaluateLimit": true}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v (%d files)", err, len(files))
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if banned[sel.Sel.Name] {
				t.Errorf("%s: %s reads %s; the limited condition must stay out of the restart, kill and shift paths",
					fset.Position(sel.Pos()), f, sel.Sel.Name)
			}
			return true
		})
	}
}
