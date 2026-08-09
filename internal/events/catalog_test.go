package events

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// declaredKinds parses events.go and returns every string literal assigned
// to a constant of type Kind, keyed by the constant's name.
//
// Parsing the source is the point. A test that enumerated the constants by
// hand would need the same edit a new kind already needs, which is the
// drift it is supposed to catch.
func declaredKinds(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "events.go", nil, 0)
	if err != nil {
		t.Fatalf("parse events.go: %v", err)
	}
	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "Kind" {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s = %s: %v", name.Name, lit.Value, err)
				}
				out[name.Name] = value
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("parsed no Kind constants from events.go; the parser, not the catalog, is wrong")
	}
	return out
}

// TestAllKindsCoversEveryDeclaredConstant is the drift guard on the
// catalog. Adding a Kind constant without adding it to allKinds fails
// here, at the commit that introduces the gap, rather than in an
// operator's `--list-kinds` output some months later.
func TestAllKindsCoversEveryDeclaredConstant(t *testing.T) {
	declared := declaredKinds(t)
	listed := map[string]bool{}
	for _, k := range AllKinds() {
		if listed[string(k)] {
			t.Errorf("AllKinds lists %q twice", k)
		}
		listed[string(k)] = true
	}
	for name, value := range declared {
		if !listed[value] {
			t.Errorf("Kind constant %s (%q) is declared but missing from allKinds", name, value)
		}
	}
	if len(listed) != len(declared) {
		t.Errorf("AllKinds has %d entries, events.go declares %d Kind constants", len(listed), len(declared))
	}
}

// TestAllKindsIsACopy holds the accessor to returning a copy. The catalog
// is package-level state and the CLI renders it; a caller that sorted the
// returned slice in place would reorder it for every later caller.
func TestAllKindsIsACopy(t *testing.T) {
	first := AllKinds()
	if len(first) == 0 {
		t.Fatal("AllKinds returned nothing")
	}
	original := first[0]
	first[0] = Kind("mutated")
	if second := AllKinds(); second[0] != original {
		t.Errorf("AllKinds()[0] = %q after a caller mutated an earlier result, want %q", second[0], original)
	}
}

func TestIsKnownKind(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"control-plane kind", string(KindSessionCrashed), true},
		{"agent kind", string(KindAgentToolCall), true},
		{"plausible typo", "session.crash", false},
		{"adapter vocabulary without the agent prefix", "tool.call", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsKnownKind(tt.in); got != tt.want {
				t.Errorf("IsKnownKind(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
