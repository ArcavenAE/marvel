package company

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	recPrefix  = "recipe bus-seat: "
	fileRecipe = "company acme-recipes@2026-11-01-b41d0e2: recipes/bus-seat.toml: "
)

func TestDecodeRecipeAcceptsAGoodRecipe(t *testing.T) {
	r, err := DecodeRecipe(recipeCtx(), []byte(goodRecipe))
	if err != nil {
		t.Fatalf("DecodeRecipe: %v", err)
	}
	if r.Schema != 1 || r.Adapter != "claude" || r.Command != "tool:stub-shim" || len(r.Prompt) != 3 || len(r.Args) != 1 {
		t.Errorf("recipe = %+v, want the file's own keys", r)
	}
	if r.Env["BUS_ROLE"] != "{role}" || r.Env["BUS_SCOPE"] != "{param:scope}" {
		t.Errorf("env = %v", r.Env)
	}
	if p := r.Params["scope"]; p.Type != "string" || !p.Required || p.Pattern != "^[a-z][a-z0-9-]{0,63}$" || r.Params["cue"].Type != "bool" {
		t.Errorf("params = %+v", r.Params)
	}
	if m := r.MCP["bus"]; m.Command != "tool:stub-shim" || !m.Strict || len(m.Args) != 1 {
		t.Errorf("mcp = %+v", r.MCP)
	}
	if r.Preflight == nil || r.Preflight.Timeout != 20*time.Second || len(r.Preflight.Command) != 2 {
		t.Errorf("preflight = %+v", r.Preflight)
	}
	if w := r.When["cue"]; w.Env["BUS_CUE"] != "1" || len(w.Args) != 1 {
		t.Errorf("when.cue = %+v", w)
	}
	if r.Holder == nil || r.Holder.Env["BUS_GLOBAL_DOMAIN"] != "{var:bus.global_domain}" || r.NonHolder == nil || len(r.NonHolder.Unset) != 2 {
		t.Errorf("global blocks = %+v %+v", r.Holder, r.NonHolder)
	}
}

// Spec row 1, in every table of a recipe.
func TestRecipeRefusesAnUnknownKeyInEveryTable(t *testing.T) {
	cases := []struct{ name, old, new, key string }{
		{"top level", "args = [\"--x\"]\n", "args = [\"--x\"]\nbogus = 1\n", "bogus"},
		{"mcp", "strict = true\n", "strict = true\nbogus = 1\n", "mcp.bus.bogus"},
		{"preflight", "timeout = \"20s\"\n", "timeout = \"20s\"\nbogus = 1\n", "preflight.bogus"},
		{"params", "type = \"bool\"\n", "type = \"bool\"\nbogus = 1\n", "params.cue.bogus"},
		{"when param", "args = [\"--load\"]\n", "args = [\"--load\"]\nbogus = 1\n", "when.cue.bogus"},
		{"when global", "[when.global.holder]\n", "[when.global]\nbogus = 1\n\n[when.global.holder]\n", "when.global.bogus"},
		{"when global branch", "[when.global.holder]\n", "[when.global.holder]\nbogus = 1\n", "when.global.holder.bogus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, c.old, c.new)
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "1", fileRecipe+`unknown key "`+c.key+`"`)
		})
	}
}

func TestRecipeRefusesAMissingRequiredKey(t *testing.T) {
	cases := []struct{ key, old string }{
		{"schema", "schema = 1\n"},
		{"adapter", "adapter = \"claude\"\n"},
		{"params.cue.type", "[params.cue]\ntype = \"bool\"\n"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			repl := ""
			if c.key == "params.cue.type" {
				repl = "[params.cue]\n"
			}
			doc := replaceOnce(t, goodRecipe, c.old, repl)
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N19", fmt.Sprintf("%srequired key %q is missing", fileRecipe, c.key))
		})
	}
}

// Rows 15 and 16: placeholders are a closed set, and a parameter used is a
// parameter declared.
func TestRecipeRefusesAnUnknownPlaceholder(t *testing.T) {
	cases := []struct{ name, old, new, key, text string }{
		{"env", `BUS_ROLE = "{role}"`, `BUS_ROLE = "{bogus}"`, "env.BUS_ROLE", "{bogus}"},
		{"args", `args = ["--x"]`, `args = ["--{team2}"]`, "args[0]", "{team2}"},
		{"when env", `BUS_CUE = "1"`, `BUS_CUE = "{sesion}"`, "when.cue.env.BUS_CUE", "{sesion}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, c.old, c.new)
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "15", fmt.Sprintf("%sunknown placeholder %q", recPrefix+c.key+": ", c.text))
		})
	}
	// The four launch names and the two prefixed forms are the whole set.
	doc := replaceOnce(t, goodRecipe, `BUS_ROLE = "{role}"`, `BUS_ROLE = "{role}-{team}-{workspace}-{session}"`)
	if _, err := DecodeRecipe(recipeCtx(), []byte(doc)); err != nil {
		t.Fatalf("the closed set must decode: %v", err)
	}
}

func TestRecipeRefusesAParameterThatIsNotDeclared(t *testing.T) {
	doc := replaceOnce(t, goodRecipe, `BUS_SCOPE = "{param:scope}"`, `BUS_SCOPE = "{param:nope}"`)
	wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "16", recPrefix+`env.BUS_SCOPE: parameter "nope" is not declared`)
}

// Row 17: two blocks that can apply together may not name one key.
func TestRecipeRefusesTwoBlocksThatNameOneKey(t *testing.T) {
	withDry := goodRecipe + "\n[params.dry]\ntype = \"bool\"\n"
	cases := []struct{ name, doc, a, b, key string }{
		{"base env and when.cue", replaceOnce(t, goodRecipe, `BUS_CUE = "1"`, `BUS_CUE = "1", BUS_ROLE = "x"`), "env", "when.cue", "BUS_ROLE"},
		{"two when.param blocks", withDry + "\n[when.dry]\nenv = { BUS_CUE = \"2\" }\n", "when.cue", "when.dry", "BUS_CUE"},
		{"a set and an unset", withDry + "\n[when.dry]\nunset = [\"BUS_CUE\"]\n", "when.cue", "when.dry", "BUS_CUE"},
		{"two unsets", replaceOnce(t, withDry, "[when.cue]\n", "[when.cue]\nunset = [\"BUS_X\"]\n") + "\n[when.dry]\nunset = [\"BUS_X\"]\n", "when.cue", "when.dry", "BUS_X"},
		{"holder and a when.param block", replaceOnce(t, goodRecipe, `env = { BUS_GLOBAL_DOMAIN = "{var:bus.global_domain}" }`, `env = { BUS_CUE = "g" }`), "when.cue", "when.global.holder", "BUS_CUE"},
		{"set and unset in one block", replaceOnce(t, goodRecipe, `unset = ["BUS_GLOBAL_DOMAIN", "BUS_GLOBAL_ROLE"]`, "env = { BUS_X = \"1\" }\nunset = [\"BUS_X\"]"), "when.global.non_holder", "when.global.non_holder", "BUS_X"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), c.doc), "17", fmt.Sprintf("%sblocks %s and %s both name %s", recPrefix, c.a, c.b, c.key))
		})
	}
	// The passing case of the spec: holder sets a key that non_holder unsets,
	// and the two never apply together. goodRecipe is that recipe.
	if _, err := DecodeRecipe(recipeCtx(), []byte(goodRecipe)); err != nil {
		t.Fatalf("holder and non_holder naming one key must decode: %v", err)
	}
}

func TestRecipeRefusesAnAdapterThatIsNotRegistered(t *testing.T) {
	doc := replaceOnce(t, goodRecipe, `adapter = "claude"`, `adapter = "nope"`)
	wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "18", recPrefix+`adapter "nope" is not registered`)
	// Control: a registered adapter decodes, and so does one the second adapter names.
	if _, err := DecodeRecipe(recipeCtx(), []byte(replaceOnce(t, goodRecipe, `adapter = "claude"`, `adapter = "codex"`))); err != nil {
		t.Fatalf("a registered adapter must decode: %v", err)
	}
}

func TestRecipeRefusesASchemaThatIsNotItsLibrarys(t *testing.T) {
	ctx := recipeCtx()
	ctx.RecipeSchema = 2
	wantRefusal(t, decodeRecipeRefused(t, ctx, goodRecipe), "19", recPrefix+"schema = 1, but the library's recipe_schema is 2")
}

func TestRecipeRefusesAPreflightTimeoutOverTheCeiling(t *testing.T) {
	doc := replaceOnce(t, goodRecipe, `timeout = "20s"`, `timeout = "10m"`)
	wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "20", recPrefix+"preflight timeout 10m0s is over the ceiling 5m0s")
	// Control: exactly the ceiling decodes, and with no ceiling set nothing is over.
	at := replaceOnce(t, goodRecipe, `timeout = "20s"`, `timeout = "5m"`)
	if _, err := DecodeRecipe(recipeCtx(), []byte(at)); err != nil {
		t.Fatalf("a timeout at the ceiling must decode: %v", err)
	}
	ctx := recipeCtx()
	ctx.Pred.PreflightCeiling = 0
	if _, err := DecodeRecipe(ctx, []byte(doc)); err != nil {
		t.Fatalf("with no ceiling set a timeout must decode: %v", err)
	}
}

// N1 and the credential classes: the five keys marvel refuses a role and any
// key api.BackendBearerEnv judges a literal credential.
func TestRecipeRefusesACredentialKey(t *testing.T) {
	keys := []string{
		"MARVEL_HEARTBEAT_TOKEN", "MARVEL_CREDENTIAL_HELPER", "MARVEL_HEARTBEAT_TOKEN_FILE",
		"DIRECTOR_NATS_USER", "DIRECTOR_NATS_PASS",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AWS_BEARER_TOKEN_BEDROCK", "SOME_VENDOR_AUTH_TOKEN",
	}
	if len(credentialKeys) != 5 {
		t.Fatalf("credentialKeys holds %d keys, want the five roleEnvRefusedKeys", len(credentialKeys))
	}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, "[env]\n", "[env]\n"+k+" = \"v\"\n")
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N1", fmt.Sprintf("%senv: key %s is a credential and may not be named in a recipe", recPrefix, k))
		})
	}
	t.Run("in an unset list", func(t *testing.T) {
		doc := replaceOnce(t, goodRecipe, `"BUS_GLOBAL_ROLE"]`, `"MARVEL_HEARTBEAT_TOKEN"]`)
		wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N1", recPrefix+"when.global.non_holder.unset: key MARVEL_HEARTBEAT_TOKEN is a credential and may not be named in a recipe")
	})
	// Twin: a pointer to a helper is not a literal credential.
	if _, err := DecodeRecipe(recipeCtx(), []byte(replaceOnce(t, goodRecipe, "[env]\n", "[env]\nMARVEL_BACKEND_API_KEY_HELPER = \"helper\"\n"))); err != nil {
		t.Fatalf("a pointer to a helper is not a literal credential and must decode: %v", err)
	}
}

// N1 names the key and never the value.
func TestCredentialKeyRefusalNamesKeyOnly(t *testing.T) {
	const secret = "SECRETVALUE-9f3a"
	doc := replaceOnce(t, goodRecipe, "[env]\n", "[env]\nANTHROPIC_API_KEY = \""+secret+"\"\n")
	err := decodeRecipeRefused(t, recipeCtx(), doc)
	r := refusalOf(t, err)
	if r.Row != "N1" || !strings.Contains(r.Message, "ANTHROPIC_API_KEY") || strings.Contains(r.Message, secret) {
		t.Fatalf("refusal = %+v, want N1 naming the key and not the value", r)
	}
}

// N2: keys marvel sets for a seat. DIRECTOR_CLUSTER is allowed set and unset,
// and DIRECTOR_GLOBAL_ROLE is allowed unset and refused set.
func TestRecipeRefusesAConstructedKey(t *testing.T) {
	for _, k := range append(append([]string{}, constructedKeys...), "DIRECTOR_GLOBAL_ROLE") {
		t.Run("env "+k, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, "[env]\n", "[env]\n"+k+" = \"v\"\n")
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N2", fmt.Sprintf("%senv: key %s is set by marvel and may not be named in a recipe", recPrefix, k))
		})
	}
	t.Run("unset of an identity key", func(t *testing.T) {
		doc := replaceOnce(t, goodRecipe, `"BUS_GLOBAL_ROLE"]`, `"MARVEL_SESSION"]`)
		wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N2", recPrefix+"when.global.non_holder.unset: key MARVEL_SESSION is set by marvel and may not be named in a recipe")
	})
	for _, twin := range []struct{ name, edit string }{
		{"DIRECTOR_CLUSTER set", "[env]\nDIRECTOR_CLUSTER = \"c\"\n"},
		{"a key marvel does not construct", "[env]\nMARVEL_WORKDIR = \"/x\"\n"},
	} {
		t.Run("twin "+twin.name, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, "[env]\n", twin.edit)
			if _, err := DecodeRecipe(recipeCtx(), []byte(doc)); err != nil {
				t.Fatalf("must decode: %v", err)
			}
		})
	}
	for _, k := range []string{"DIRECTOR_CLUSTER", "DIRECTOR_GLOBAL_ROLE"} {
		t.Run("twin unset "+k, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, `"BUS_GLOBAL_ROLE"]`, `"`+k+`"]`)
			if _, err := DecodeRecipe(recipeCtx(), []byte(doc)); err != nil {
				t.Fatalf("an unset of %s must decode: %v", k, err)
			}
		})
	}
}

// N21: an env or unset name is a plain name.
func TestRecipeRefusesAnEnvKeyThatIsNotAPlainName(t *testing.T) {
	const prefix = "company acme-recipes@2026-11-01-b41d0e2: recipe bus-seat: "
	cases := []struct{ name, doc, section, key string }{
		{"dash", replaceOnce(t, goodRecipe, "[env]\n", "[env]\n\"bad-key\" = \"v\"\n"), "env", "bad-key"},
		{"leading digit", replaceOnce(t, goodRecipe, "[env]\n", "[env]\n\"1ABC\" = \"v\"\n"), "env", "1ABC"},
		{"space", replaceOnce(t, goodRecipe, "[env]\n", "[env]\n\"A B\" = \"v\"\n"), "env", `"A B"`},
		{"empty", replaceOnce(t, goodRecipe, "[env]\n", "[env]\n\"\" = \"v\"\n"), "env", `""`},
		{"unset", replaceOnce(t, goodRecipe, `"BUS_GLOBAL_ROLE"]`, `"bad.key"]`), "unset", "bad.key"},
		{"in a when block", replaceOnce(t, goodRecipe, `env = { BUS_CUE = "1" }`, `env = { "BUS CUE" = "1" }`), "env", `"BUS CUE"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), c.doc), "N21", fmt.Sprintf("%s%s key %s is not a plain name", prefix, c.section, c.key))
		})
	}
	if _, err := DecodeRecipe(recipeCtx(), []byte(replaceOnce(t, goodRecipe, "[env]\n", "[env]\n_UNDER_1 = \"v\"\n"))); err != nil {
		t.Fatalf("a plain name must decode: %v", err)
	}
}

// N3: a parameter named global would collide with [when.global.*].
func TestRecipeRefusesAParameterNamedGlobal(t *testing.T) {
	doc := replaceOnce(t, goodRecipe, "[params.cue]\n", "[params.global]\ntype = \"bool\"\n\n[params.cue]\n")
	wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N3", recPrefix+`params.global: parameter name "global" is reserved`)
}

// N4: tool: is valid only as a whole command value or the first element of a
// command list.
func TestRecipeRefusesAToolOutsideAValuePosition(t *testing.T) {
	cases := []struct{ name, old, new, key string }{
		{"in args", `args = ["--x"]`, `args = ["tool:x"]`, "args[0]"},
		{"in an env value", `BUS_ROLE = "{role}"`, `BUS_ROLE = "tool:x"`, "env.BUS_ROLE"},
		{"in mcp args", `args = ["serve"]`, `args = ["tool:x"]`, "mcp.bus.args[0]"},
		{"as a later preflight element", `["tool:stub-preflight", "--check"]`, `["tool:stub-preflight", "tool:x"]`, "preflight.command[1]"},
		{"in a when block", `args = ["--load"]`, `args = ["tool:x"]`, "when.cue.args[0]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, c.old, c.new)
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N4", fmt.Sprintf("%s%s: tool: is valid only as a whole command or the first element of a command list", recPrefix, c.key))
		})
	}
	// Twins: a command, an mcp command and the first element of a command list.
	if _, err := DecodeRecipe(recipeCtx(), []byte(goodRecipe)); err != nil {
		t.Fatalf("tool: in a command position must decode: %v", err)
	}
}

// N24: a recipe value of the wrong form.
func TestRecipeRefusesAValueOfTheWrongForm(t *testing.T) {
	cases := []struct{ name, old, new, key, form string }{
		{"param type", `type = "bool"`, `type = "int"`, "params.cue.type", `"string" or "bool"`},
		{"timeout", `timeout = "20s"`, `timeout = "soon"`, "preflight.timeout", "a duration"},
		{"args", `args = ["--x"]`, `args = "--x"`, "args", "a list of strings"},
		{"adapter", `adapter = "claude"`, `adapter = ""`, "adapter", "a non-empty string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := replaceOnce(t, goodRecipe, c.old, c.new)
			wantRefusal(t, decodeRecipeRefused(t, recipeCtx(), doc), "N24", fmt.Sprintf("%s%s: must be %s", recPrefix, c.key, c.form))
		})
	}
}

// N26: a recipe that does not parse names the line and nothing it said.
func TestRecipeThatDoesNotParseNamesOnlyTheLine(t *testing.T) {
	doc := goodRecipe + "not toml SECRET-FILE-CONTENT\n"
	r := refusalOf(t, decodeRecipeRefused(t, recipeCtx(), doc))
	if r.Row != "N26" || !strings.HasPrefix(r.Message, "recipe bus-seat: recipes/bus-seat.toml does not parse (line ") || strings.Contains(r.Message, "SECRET-FILE-CONTENT") {
		t.Fatalf("refusal = %+v, want N26 naming a line and no content", r)
	}
}
