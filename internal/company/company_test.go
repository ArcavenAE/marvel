package company

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodeCompanyAcceptsAGoodDefinitionsLibrary(t *testing.T) {
	c, err := DecodeCompany(defsDir, []byte(goodDefs))
	if err != nil {
		t.Fatalf("DecodeCompany: %v", err)
	}
	if c.Name != libName || c.Version != libVer || c.Kind != "definitions" || c.Schema != 1 || c.DefinitionSchema != 1 || c.RequiresMarvel != "0.9.0" {
		t.Errorf("company = %+v, want the library's own keys", c)
	}
	if c.Source.URL != "https://example.invalid/acme-roles.git" || c.Source.Commit != "ecc28ac" || len(c.Source.ContentsTree) != 40 {
		t.Errorf("source = %+v", c.Source)
	}
	if len(c.Definitions) != 1 || c.Definitions[0].ID != "role/builder--steady" || c.Definitions[0].Identity != "steady@1.0.0" ||
		c.Definitions[0].Status != "ratified" || c.Definitions[0].IdentityStatus != "ratified" || c.Definitions[0].SHA256 != hex64 {
		t.Errorf("definitions = %+v", c.Definitions)
	}
}

func TestDecodeCompanyAcceptsAGoodRecipesLibrary(t *testing.T) {
	c, err := DecodeCompany(recsDir, []byte(goodRecs))
	if err != nil {
		t.Fatalf("DecodeCompany: %v", err)
	}
	if c.Kind != "recipes" || c.RecipeSchema != 1 || c.Requires.DefinitionSchema != 1 || c.Source.Commit != "b41d0e2" || len(c.Definitions) != 0 {
		t.Errorf("company = %+v, want a recipes library", c)
	}
}

// A role on its own, with no identity, is an entry too.
func TestDecodeCompanyAcceptsARoleOnItsOwn(t *testing.T) {
	doc := strings.Replace(goodDefs, "role/builder--steady", "role/builder", 1)
	doc = strings.Replace(doc, "identity = \"steady@1.0.0\"\n", "", 1)
	doc = strings.Replace(doc, "identity_status = \"ratified\"\n", "", 1)
	c, err := DecodeCompany(defsDir, []byte(doc))
	if err != nil || len(c.Definitions) != 1 || c.Definitions[0].ID != "role/builder" || c.Definitions[0].Identity != "" {
		t.Fatalf("company %+v err %v, want a role on its own to decode", c, err)
	}
}

// Spec row 1: an unknown key anywhere in the file is refused, naming the key.
func TestCompanyTOMLRefusesAnUnknownKey(t *testing.T) {
	cases := []struct{ name, doc, key string }{
		{"top level", goodDefs + "colour = \"red\"\n", "colour"},
		{"in source", replaceOnce(t, goodDefs, `commit = "ecc28ac" }`, `commit = "ecc28ac", extra = "x" }`), "source.extra"},
		{"in an entry", goodDefs + "colour = \"red\"\n", "definitions.colour"},
	}
	// "in an entry": the line above lands inside [[definitions]], the last table.
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := decodeCompanyRefused(t, defsDir, c.doc)
			wantRefusal(t, err, "1", defsPrefx+`unknown key "`+c.key+`"`)
		})
	}
	t.Run("in requires", func(t *testing.T) {
		doc := replaceOnce(t, goodRecs, `requires = { definition_schema = 1 }`, `requires = { definition_schema = 1, extra = 2 }`)
		err := decodeCompanyRefused(t, recsDir, doc)
		wantRefusal(t, err, "1", "company acme-recipes@2026-11-01-b41d0e2: company.toml: unknown key \"requires.extra\"")
	})
	// Kind-specific keys are unknown keys for the other kind.
	t.Run("a recipes library with an index", func(t *testing.T) {
		err := decodeCompanyRefused(t, recsDir, goodRecs+"\n[[definitions]]\nid = \"role/x\"\n")
		wantRefusal(t, err, "1", "company acme-recipes@2026-11-01-b41d0e2: company.toml: unknown key \"definitions\"")
	})
	t.Run("a definitions library with recipe_schema", func(t *testing.T) {
		doc := replaceOnce(t, goodDefs, "definition_schema = 1\n", "definition_schema = 1\nrecipe_schema = 1\n")
		wantRefusal(t, decodeCompanyRefused(t, defsDir, doc), "1", defsPrefx+`unknown key "recipe_schema"`)
	})
	t.Run("a definitions library with requires", func(t *testing.T) {
		doc := replaceOnce(t, goodDefs, "definition_schema = 1\n", "definition_schema = 1\nrequires = { definition_schema = 1 }\n")
		wantRefusal(t, decodeCompanyRefused(t, defsDir, doc), "1", defsPrefx+`unknown key "requires"`)
	})
	// The twins decode: see TestDecodeCompanyAccepts above.
}

// N19: one fixture per required key per kind, each beside its full twin.
func TestCompanyTOMLRefusesAMissingRequiredKey(t *testing.T) {
	type fx struct{ key, remove string }
	defs := []fx{
		{"name", "name = \"acme-roles\"\n"},
		{"version", "version = \"2026-11-01-ecc28ac\"\n"},
		{"kind", "kind = \"definitions\"\n"},
		{"schema", "schema = 1\n"},
		{"requires_marvel", "requires_marvel = \"0.9.0\"\n"},
		{"definition_schema", "definition_schema = 1\n"},
		{"source", `source = { url = "https://example.invalid/acme-roles.git", contents_tree = "0123456789012345678901234567890123456789", commit = "ecc28ac" }` + "\n"},
		{"source.url", `url = "https://example.invalid/acme-roles.git", `},
		{"source.contents_tree", `contents_tree = "0123456789012345678901234567890123456789", `},
		{"definitions", "\n[[definitions]]"}, // handled below: the whole index
		{"definitions[0].id", "id = \"role/builder--steady\"\n"},
		{"definitions[0].version", "version = \"1.2.0\"\n"},
		{"definitions[0].status", "status = \"ratified\"\n"},
		{"definitions[0].sha256", "sha256 = \"" + hex64 + "\"\n"},
		{"definitions[0].slice", "slice = \"sha256:" + hex64 + "\"\n"},
		{"definitions[0].border", "border = \"sha256:" + hex64 + "\"\n"},
	}
	for _, c := range defs {
		t.Run("definitions/"+c.key, func(t *testing.T) {
			var doc string
			if c.key == "definitions" {
				doc = goodDefs[:strings.Index(goodDefs, "\n[[definitions]]")] + "\n"
			} else {
				doc = replaceOnce(t, goodDefs, c.remove, "")
			}
			err := decodeCompanyRefused(t, defsDir, doc)
			wantRefusal(t, err, "N19", fmt.Sprintf("%srequired key %q is missing", defsPrefx, c.key))
		})
	}
	recs := []fx{
		{"name", "name = \"acme-recipes\"\n"},
		{"version", "version = \"2026-11-01-b41d0e2\"\n"},
		{"kind", "kind = \"recipes\"\n"},
		{"schema", "schema = 1\n"},
		{"requires_marvel", "requires_marvel = \"0.9.0\"\n"},
		{"recipe_schema", "recipe_schema = 1\n"},
		{"requires", "requires = { definition_schema = 1 }\n"},
		{"source", `source = { url = "https://example.invalid/acme-recipes.git", commit = "b41d0e2" }` + "\n"},
		{"source.url", `url = "https://example.invalid/acme-recipes.git", `},
		{"source.commit", `, commit = "b41d0e2"`},
	}
	const recsPrefix = "company acme-recipes@2026-11-01-b41d0e2: company.toml: "
	for _, c := range recs {
		t.Run("recipes/"+c.key, func(t *testing.T) {
			doc := replaceOnce(t, goodRecs, c.remove, "")
			wantRefusal(t, decodeCompanyRefused(t, recsDir, doc), "N19", fmt.Sprintf("%srequired key %q is missing", recsPrefix, c.key))
		})
	}
	t.Run("requires.definition_schema", func(t *testing.T) {
		doc := replaceOnce(t, goodRecs, "requires = { definition_schema = 1 }", "requires = {}")
		wantRefusal(t, decodeCompanyRefused(t, recsDir, doc), "N19", recsPrefix+`required key "requires.definition_schema" is missing`)
	})
	// requires_marvel is required, as the director ruled: no default stands in.
}

// When the file cannot supply its own name and version, the message takes them
// from the directory.
func TestRequiredKeyRefusalTakesNameAndVersionFromTheDirectory(t *testing.T) {
	doc := replaceOnce(t, goodDefs, "name = \"acme-roles\"\nversion = \"2026-11-01-ecc28ac\"\n", "")
	err := decodeCompanyRefused(t, defsDir, doc)
	wantRefusal(t, err, "N19", defsPrefx+`required key "name" is missing`)
}

// N5: the name and the version in the file equal the directory's.
func TestCompanyTOMLRefusesANameOrVersionThatIsNotItsDirectory(t *testing.T) {
	err := decodeCompanyRefused(t, Ident{Name: libName, Version: "2026-11-02-ecc28ac"}, goodDefs)
	wantRefusal(t, err, "N5", `company acme-roles@2026-11-02-ecc28ac: company.toml: version "2026-11-01-ecc28ac" does not equal its directory "2026-11-02-ecc28ac"`)
	err = decodeCompanyRefused(t, Ident{Name: "acme-other", Version: libVer}, goodDefs)
	wantRefusal(t, err, "N5", `company acme-other@2026-11-01-ecc28ac: company.toml: name "acme-roles" does not equal its directory "acme-other"`)
}

// Row 12: the entry carries an identity if and only if its id has "--", and the
// part of the id after "--" is the identity's name.
func TestIndexRefusesTheLibraryWhenIdentityAndIDDisagree(t *testing.T) {
	cases := []struct{ name, edit, old, new, id, identity string }{
		{"pair id with no identity", "", "identity = \"steady@1.0.0\"\n", "", "role/builder--steady", ""},
		{"identity on a role on its own", "", "role/builder--steady", "role/builder", "role/builder", "steady@1.0.0"},
		{"identity of another name", "", "steady@1.0.0", "calm@1.0.0", "role/builder--steady", "calm@1.0.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := replaceOnce(t, goodDefs, c.old, c.new)
			err := decodeCompanyRefused(t, defsDir, doc)
			wantRefusal(t, err, "12", fmt.Sprintf("%scompany.toml: definition %s: identity %s does not match the id", "company acme-roles@2026-11-01-ecc28ac: ", c.id, bareOrEmpty(c.identity)))
		})
	}
}

// bareOrEmpty prints an absent identity the way the refusal does: as the empty
// quoted field.
func bareOrEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

// N15: index fields the spawn line copies unchecked must be well formed.
func TestIndexRefusesAMalformedField(t *testing.T) {
	cases := []struct{ field, old, new string }{
		{"slice", "slice = \"sha256:" + hex64 + "\"", "slice = \"sha256:ABC\""},
		{"border", "border = \"sha256:" + hex64 + "\"", "border = \"" + hex64 + "\""},
		{"sha256", "sha256 = \"" + hex64 + "\"", "sha256 = \"short\""},
		{"version", `version = "1.2.0"`, `version = "1.2 0"`},
		{"identity", `identity = "steady@1.0.0"`, "identity = \"steady@1.0.0\\n\""},
		{"status", `status = "ratified"`, `status = "approved"`},
		{"identity_status", `identity_status = "ratified"`, `identity_status = "approved"`},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			doc := replaceOnce(t, goodDefs, c.old, c.new)
			err := decodeCompanyRefused(t, defsDir, doc)
			wantRefusal(t, err, "N15", fmt.Sprintf("company acme-roles@2026-11-01-ecc28ac: company.toml: definition role/builder--steady: %s is not well formed", c.field))
		})
	}
	// The twin: lowercase hex of the right length and plain strings decode.
	if _, err := DecodeCompany(defsDir, []byte(goodDefs)); err != nil {
		t.Fatalf("the twin must decode: %v", err)
	}
}

// N23: a company-level key of the wrong form.
func TestCompanyTOMLRefusesAKeyOfTheWrongForm(t *testing.T) {
	cases := []struct{ name, old, new, key, form string }{
		{"kind", `kind = "definitions"`, `kind = "other"`, "kind", `"definitions" or "recipes"`},
		{"schema type", "schema = 1\n", "schema = \"one\"\n", "schema", "an integer"},
		{"source.url type", `url = "https://example.invalid/acme-roles.git"`, `url = 5`, "source.url", "a string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := replaceOnce(t, goodDefs, c.old, c.new)
			wantRefusal(t, decodeCompanyRefused(t, defsDir, doc), "N23", fmt.Sprintf("%s%s must be %s", defsPrefx, c.key, c.form))
		})
	}
}

// N25: TOML that does not parse names the line and nothing the file said.
func TestCompanyTOMLThatDoesNotParseNamesOnlyTheLine(t *testing.T) {
	doc := goodDefs + "this is not toml SECRET-FILE-CONTENT\n"
	err := decodeCompanyRefused(t, defsDir, doc)
	r := refusalOf(t, err)
	if r.Row != "N25" || !strings.HasPrefix(r.Message, "company acme-roles@2026-11-01-ecc28ac: company.toml does not parse (line ") {
		t.Fatalf("refusal = %+v, want N25 naming a line", r)
	}
	if strings.Contains(r.Message, "SECRET-FILE-CONTENT") {
		t.Errorf("message %q echoes the file's content", r.Message)
	}
}
