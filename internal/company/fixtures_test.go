package company

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	hex64     = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	libName   = "acme-roles"
	libVer    = "2026-11-01-ecc28ac"
	recName   = "acme-recipes"
	recVer    = "2026-11-01-b41d0e2"
	defsPrefx = "company acme-roles@2026-11-01-ecc28ac: company.toml: "
)

var (
	defsDir = Ident{Name: libName, Version: libVer}
	recsDir = Ident{Name: recName, Version: recVer}
)

// goodDefs is a definitions library that decodes. The tests below break one
// thing at a time in a copy of it, and each refusing fixture has this as its
// passing twin.
const goodDefs = `name = "acme-roles"
version = "2026-11-01-ecc28ac"
kind = "definitions"
schema = 1
requires_marvel = "0.9.0"
definition_schema = 1
source = { url = "https://example.invalid/acme-roles.git", contents_tree = "0123456789012345678901234567890123456789", commit = "ecc28ac" }

[[definitions]]
id = "role/builder--steady"
version = "1.2.0"
identity = "steady@1.0.0"
status = "ratified"
identity_status = "ratified"
sha256 = "` + hex64 + `"
slice = "sha256:` + hex64 + `"
border = "sha256:` + hex64 + `"
`

const goodRecs = `name = "acme-recipes"
version = "2026-11-01-b41d0e2"
kind = "recipes"
schema = 1
requires_marvel = "0.9.0"
recipe_schema = 1
requires = { definition_schema = 1 }
source = { url = "https://example.invalid/acme-recipes.git", commit = "b41d0e2" }
`

// goodRecipe is a recipe that decodes, and the passing twin of every refusing
// recipe fixture. The tool names are ones no host has.
const goodRecipe = `schema = 1
adapter = "claude"
command = "tool:stub-shim"
prompt = ["template:cast", "definition", "notes"]
args = ["--x"]

[params.cue]
type = "bool"

[params.scope]
type = "string"
required = true
pattern = "^[a-z][a-z0-9-]{0,63}$"

[env]
BUS_ROLE = "{role}"
BUS_SCOPE = "{param:scope}"

[mcp.bus]
command = "tool:stub-shim"
args = ["serve"]
strict = true

[preflight]
command = ["tool:stub-preflight", "--check"]
timeout = "20s"

[when.cue]
env = { BUS_CUE = "1" }
args = ["--load"]

[when.global.holder]
env = { BUS_GLOBAL_DOMAIN = "{var:bus.global_domain}" }

[when.global.non_holder]
unset = ["BUS_GLOBAL_DOMAIN", "BUS_GLOBAL_ROLE"]
`

// constructedKeys are the keys the test predicate reports as set by marvel:
// the nine identity keys and the three address keys.
var constructedKeys = []string{
	"MARVEL_SESSION", "MARVEL_ROLE", "MARVEL_TEAM", "MARVEL_WORKSPACE",
	"DIRECTOR_AGENT_ID", "DIRECTOR_TEAM", "DIRECTOR_WORKSPACE", "BEADS_ACTOR", "MARVEL_SEAT_URI",
	"NATS_URL", "MARVEL_SOCKET", "MARVEL_HOME",
}

func testPred() Predicates {
	return Predicates{
		Constructed:       func(k string) bool { return slices.Contains(constructedKeys, k) },
		AdapterRegistered: func(n string) bool { return n == "claude" || n == "codex" },
		PreflightCeiling:  5 * time.Minute,
	}
}

func recipeCtx() RecipeContext {
	return RecipeContext{Library: recsDir, Name: "bus-seat", RecipeSchema: 1, Pred: testPred()}
}

// refusalOf returns the *Refusal an error carries, failing the test when it
// carries none.
func refusalOf(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r == nil {
		t.Fatalf("err = %v, want a *Refusal", err)
	}
	return r
}

// wantRefusal asserts err is the refusal of row id with exactly msg.
func wantRefusal(t *testing.T, err error, id, msg string) {
	t.Helper()
	r := refusalOf(t, err)
	if r.Row != id || r.Message != msg {
		t.Fatalf("refusal = row %q %q\nwant       row %q %q", r.Row, r.Message, id, msg)
	}
}

// replaceOnce replaces the one occurrence of old in s with new, failing the
// test when old is absent or repeated, so a fixture edit cannot silently miss.
func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if strings.Count(s, old) != 1 {
		t.Fatalf("fixture edit: %q occurs %d times, want 1", old, strings.Count(s, old))
	}
	return strings.Replace(s, old, new, 1)
}

// assertNothingLeft is called after a refusal. A refused read returns no
// value and leaves the process environment as it found it. The decoders take
// bytes and touch no file, so there is no scratch directory to check.
func assertNothingLeft(t *testing.T, env []string, got any) {
	t.Helper()
	switch v := got.(type) {
	case *Company:
		if v != nil {
			t.Errorf("a refused company decode returned %+v, want nil", v)
		}
	case *Recipe:
		if v != nil {
			t.Errorf("a refused recipe decode returned %+v, want nil", v)
		}
	}
	if after := os.Environ(); !slices.Equal(slices.Sorted(slices.Values(env)), slices.Sorted(slices.Values(after))) {
		t.Error("the process environment changed across a refusal")
	}
}

// decodeCompanyRefused runs DecodeCompany and, when it refuses, checks the
// refusal left nothing; it returns the error.
func decodeCompanyRefused(t *testing.T, dir Ident, doc string) error {
	t.Helper()
	env := os.Environ()
	c, err := DecodeCompany(dir, []byte(doc))
	if err != nil {
		assertNothingLeft(t, env, c)
	}
	return err
}

func decodeRecipeRefused(t *testing.T, ctx RecipeContext, doc string) error {
	t.Helper()
	env := os.Environ()
	r, err := DecodeRecipe(ctx, []byte(doc))
	if err != nil {
		assertNothingLeft(t, env, r)
	}
	return err
}
