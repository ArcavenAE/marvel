package usage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The resolution ladder is a ruled ordering, not an implementation
// detail of Resolve's branch sequence. These tests hold three properties
// that branch order alone cannot state: that the ordering covers every
// LimitSource that exists, that it is the order the operator ruled, and
// that Resolve actually obeys it.

// TestLimitLadderIsTotal fails when a LimitSource constant is declared
// without a place on the ladder.
//
// It reads the constants out of limits.go rather than listing them here,
// because a hand-maintained list in the test is the same omission the
// test is meant to catch: both the ladder and the list would be updated
// by the same person in the same edit, or by neither. Parsing the source
// is the only check that a NEW constant, added by someone who never read
// this file, fails rather than silently ranking last.
func TestLimitLadderIsTotal(t *testing.T) {
	t.Parallel()

	declared := limitSourceConstants(t, "limits.go")
	if len(declared) == 0 {
		t.Fatal("parsed no LimitSource constants from limits.go; the test's own premise is broken")
	}

	ranked := make(map[LimitSource]int, len(limitLadder))
	for i, s := range limitLadder {
		if prev, dup := ranked[s]; dup {
			t.Errorf("%q appears on the ladder twice, at rungs %d and %d", s, prev+1, i+1)
		}
		ranked[s] = i
	}

	for _, name := range declared {
		if _, ok := ranked[LimitSource(name)]; !ok {
			t.Errorf("LimitSource %q is declared in limits.go but has no rung on limitLadder; give it one, and put it where its authority belongs rather than at the end", name)
		}
	}
	if len(declared) != len(limitLadder) {
		t.Errorf("limits.go declares %d LimitSource constants, the ladder has %d rungs", len(declared), len(limitLadder))
	}
}

// TestLimitLadderIsTheRuledOrder pins the ordering itself. Changing it is
// a decision, so this test exists to make the change deliberate: the
// stream/feed asymmetry in particular reads like a bug and will attract
// a well-meaning fix.
func TestLimitLadderIsTheRuledOrder(t *testing.T) {
	t.Parallel()

	want := []LimitSource{
		LimitFromStream,
		LimitLearned,
		LimitFromManifest,
		LimitFromFeed,
		LimitFromTable,
		LimitFromTableAlias,
		LimitUnresolved,
	}
	if len(limitLadder) != len(want) {
		t.Fatalf("ladder has %d rungs, want %d", len(limitLadder), len(want))
	}
	for i := range want {
		if limitLadder[i] != want[i] {
			t.Errorf("rung %d = %q, want %q", i+1, limitLadder[i], want[i])
		}
		if got := want[i].Rank(); got != i+1 {
			t.Errorf("%q.Rank() = %d, want %d", want[i], got, i+1)
		}
	}
	if got := LimitSource("something-a-later-writer-added").Rank(); got <= LimitUnresolved.Rank() {
		t.Errorf("an undeclared source ranks %d, at or above unresolved (%d); it must rank last, not first", got, LimitUnresolved.Rank())
	}
}

// rung is one way to satisfy a ladder position, with a window value
// unique to it so the winning number names the winner.
type rung struct {
	source LimitSource
	limit  int
	// apply sets whatever field of the request satisfies this rung.
	apply func(*Request)
	// learn, when true, requires the resolver to have learned the
	// request's model before Resolve is called.
	learn bool
	// tableKey is the model id this rung needs. Rungs that do not care
	// leave it empty.
	tableKey string
}

// TestResolveAgreesWithTheLadder is the behavioral half: for every pair
// of rungs that can be satisfied at once, the higher-ranked one wins.
//
// Pairwise rather than a single request satisfying everything, because a
// single request only proves the top rung and would pass with the rest of
// the ladder shuffled.
//
// The pair order is read from limitLadder, not from a second list written
// here. An earlier draft did keep its own ordered list, and swapping two
// rungs in limitLadder left this test green while
// TestLimitLadderIsTheRuledOrder went red: it was pinning Resolve to the
// test's opinion rather than to the ladder. Driving it from limitLadder
// is what makes a ladder edit and a Resolve edit fail together.
func TestResolveAgreesWithTheLadder(t *testing.T) {
	t.Parallel()

	const (
		tableModel = "claude-haiku-4-5"
		aliasModel = "haiku"
	)
	table := Table{tableModel: 905_000}

	bySource := map[LimitSource]rung{
		LimitFromStream:     {source: LimitFromStream, limit: 901_000, apply: func(r *Request) { r.SampleLimit = 901_000 }},
		LimitLearned:        {source: LimitLearned, limit: 902_000, learn: true},
		LimitFromManifest:   {source: LimitFromManifest, limit: 903_000, apply: func(r *Request) { r.ManifestLimit = 903_000 }},
		LimitFromFeed:       {source: LimitFromFeed, limit: 904_000, apply: func(r *Request) { r.FeedLimit = 904_000 }},
		LimitFromTable:      {source: LimitFromTable, limit: 905_000, tableKey: tableModel},
		LimitFromTableAlias: {source: LimitFromTableAlias, limit: 905_000, tableKey: aliasModel},
	}

	// LimitUnresolved is the absence of every rung, so nothing satisfies
	// it and it takes no part in the pairwise matrix.
	var rungs []rung
	for _, s := range limitLadder {
		if s == LimitUnresolved {
			continue
		}
		r, ok := bySource[s]
		if !ok {
			t.Fatalf("ladder rung %q has no way to satisfy it in this test; add one rather than skipping it", s)
		}
		rungs = append(rungs, r)
	}

	for i, hi := range rungs {
		for j, lo := range rungs {
			if i >= j {
				continue
			}
			// table and table-alias are structurally exclusive: an alias
			// is only consulted when the exact key misses, so no request
			// satisfies both. Their order is fixed by that exclusion
			// rather than by precedence, and TestResolveLadderPrecedence
			// covers each singly.
			if isTableRung(hi.source) && isTableRung(lo.source) {
				continue
			}

			model := tableModel
			if hi.tableKey == aliasModel || lo.tableKey == aliasModel {
				model = aliasModel
			}

			req := Request{StreamModel: model}
			for _, r := range []rung{hi, lo} {
				if r.apply != nil {
					r.apply(&req)
				}
			}

			res := NewResolver(table)
			if hi.learn || lo.learn {
				w := hi.limit
				if lo.learn {
					w = lo.limit
				}
				res.Learn(model, w)
			}

			limit, src, _ := res.Resolve(req)
			if src != hi.source {
				t.Errorf("%q (rung %d) against %q (rung %d): source = %q, want the higher rung %q",
					hi.source, hi.source.Rank(), lo.source, lo.source.Rank(), src, hi.source)
			}
			if limit != hi.limit {
				t.Errorf("%q against %q: limit = %d, want %d", hi.source, lo.source, limit, hi.limit)
			}
		}
	}
}

func isTableRung(s LimitSource) bool {
	return s == LimitFromTable || s == LimitFromTableAlias
}

// limitSourceConstants returns the string values of every constant in
// file declared with the type LimitSource.
func limitSourceConstants(t *testing.T, file string) []string {
	t.Helper()

	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	var out []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "LimitSource" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Errorf("LimitSource constant with a non-literal value at %v; this test only understands string literals", vs.Names)
					continue
				}
				out = append(out, lit.Value[1:len(lit.Value)-1])
			}
		}
	}
	return out
}
