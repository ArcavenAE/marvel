package company

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	defsRef = "company acme-roles@2026-11-01-ecc28ac"
	recsRef = "company acme-recipes@2026-11-01-b41d0e2"
)

// launch is one good launch laid out on disk, with each document held as text
// so a test breaks one thing in it. build writes the libraries into a temp root,
// resolves both pins through Resolve, and returns the Env and the Request.
type launch struct {
	t       *testing.T
	defs    string
	recs    string
	recipe  string
	role    string
	cast    string
	env     *fakeEnv
	req     Request
	roleRel string
	noRecs  bool
}

func newLaunch(t *testing.T) *launch {
	t.Helper()
	return &launch{
		t:       t,
		defs:    goodDefs,
		recs:    goodRecs,
		recipe:  goodRecipe,
		role:    "role text\n",
		cast:    "cast\n",
		roleRel: "roles/builder--steady.md",
		env: &fakeEnv{
			version: "0.9.0",
			tools:   map[string]string{"stub-shim": "/x/shim", "stub-preflight": "/x/pre"},
			vars:    map[string]string{"bus.global_domain": "d"},
		},
		req: Request{
			Team: "core", Role: "builder", Definition: "role/builder--steady", Recipe: "bus-seat",
			Params: map[string]string{"scope": "alpha"}, Pred: testPred(),
		},
	}
}

func (l *launch) sum() string {
	h := sha256.Sum256([]byte(l.role))
	return hex.EncodeToString(h[:])
}

func (l *launch) editDefs(old, new string)   { l.defs = replaceOnce(l.t, l.defs, old, new) }
func (l *launch) editRecs(old, new string)   { l.recs = replaceOnce(l.t, l.recs, old, new) }
func (l *launch) editRecipe(old, new string) { l.recipe = replaceOnce(l.t, l.recipe, old, new) }

// roleOnly makes the entry a role on its own: no identity, no pair file.
func (l *launch) roleOnly() {
	l.editDefs(`id = "role/builder--steady"`, `id = "role/builder"`)
	l.editDefs("identity = \"steady@1.0.0\"\n", "")
	l.editDefs("identity_status = \"ratified\"\n", "")
	l.roleRel = "roles/builder.md"
	l.req.Definition = "role/builder"
}

func (l *launch) build() (*fakeEnv, Request) {
	l.t.Helper()
	root := l.t.TempDir()
	l.env.roots = []string{root}
	defs := strings.Replace(l.defs, hex64, l.sum(), 1)
	installVersion(l.t, root, libName, libVer, map[string]string{
		"company.toml":       defs,
		l.roleRel:            l.role,
		"templates/cast.txt": l.cast,
	})
	installVersion(l.t, root, recName, recVer, map[string]string{
		"company.toml":          l.recs,
		"recipes/bus-seat.toml": l.recipe,
	})
	cl, err := Resolve(l.env, Pin{Team: "core", Key: "company", Name: libName, Version: libVer})
	if err != nil {
		l.t.Fatalf("resolve company: %v", err)
	}
	l.req.Company = cl
	if !l.noRecs {
		rl, err := Resolve(l.env, Pin{Team: "core", Key: "recipes", Name: recName, Version: recVer})
		if err != nil {
			l.t.Fatalf("resolve recipes: %v", err)
		}
		l.req.Recipes = rl
	}
	return l.env, l.req
}

func (l *launch) check() (*Result, error) {
	l.t.Helper()
	env, req := l.build()
	return Check(env, req)
}

func (l *launch) wantRefused(row, msg string) {
	l.t.Helper()
	res, err := l.check()
	if err == nil {
		l.t.Fatalf("Check passed (%+v), want refusal %s %q", res, row, msg)
	}
	wantRefusal(l.t, err, row, msg)
	if res != nil {
		l.t.Errorf("a refused Check returned %+v, want nil", res)
	}
}

func (l *launch) wantPasses() *Result {
	l.t.Helper()
	res, err := l.check()
	if err != nil {
		l.t.Fatalf("Check refused a good launch: %v", err)
	}
	return res
}

func TestCheckPassesAGoodLaunch(t *testing.T) {
	l := newLaunch(t)
	res := l.wantPasses()
	if res.Company == nil || res.Company.Name != libName || res.Recipes == nil || res.Recipes.Name != recName {
		t.Fatalf("result libraries = %+v / %+v", res.Company, res.Recipes)
	}
	if res.Definition.ID != "role/builder--steady" || res.Recipe == nil || res.Recipe.Adapter != "claude" {
		t.Fatalf("result definition %+v, recipe %+v", res.Definition, res.Recipe)
	}
	if res.RequiresMarvel != "ok" {
		t.Errorf("RequiresMarvel = %q, want ok", res.RequiresMarvel)
	}
	want, err := TreeHash(l.req.Company.Dir)
	if err != nil || res.CompanyHash != want {
		t.Errorf("CompanyHash = %q, want %q (%v)", res.CompanyHash, want, err)
	}
	if want, _ := TreeHash(l.req.Recipes.Dir); res.RecipesHash != want {
		t.Errorf("RecipesHash = %q, want %q", res.RecipesHash, want)
	}
}

// Row 14, step 4: each pin names the kind its key needs.
func TestCheckRefusesAPinOfTheWrongKind(t *testing.T) {
	l := newLaunch(t)
	env, req := l.build()
	req.Company, req.Recipes = req.Recipes, req.Company
	_, err := Check(env, req)
	wantRefusal(t, err, "14", "team core: company pin acme-recipes@2026-11-01-b41d0e2 is a library of kind recipes, but company needs kind definitions")
	// The recipes pin alone.
	env2, req2 := newLaunch(t).build()
	req2.Recipes = req2.Company
	_, err = Check(env2, req2)
	wantRefusal(t, err, "14", "team core: recipes pin acme-roles@2026-11-01-ecc28ac is a library of kind definitions, but recipes needs kind recipes")
}

// Row 3, step 7: a schema value the marvel does not read, for each library.
func TestCheckRefusesASchemaMarvelDoesNotRead(t *testing.T) {
	t.Run("definition_schema", func(t *testing.T) {
		l := newLaunch(t)
		l.editDefs("definition_schema = 1", "definition_schema = 2")
		l.wantRefused("3", defsRef+": definition_schema = 2 is not read by this marvel (reads 1)")
	})
	t.Run("recipe_schema", func(t *testing.T) {
		l := newLaunch(t)
		l.editRecs("recipe_schema = 1", "recipe_schema = 2")
		l.wantRefused("3", recsRef+": recipe_schema = 2 is not read by this marvel (reads 1)")
	})
}

// Under a dev build the version order is unknown, but the schema list is not:
// check 7 still refuses.
func TestSchemaListStillRefusesUnderDev(t *testing.T) {
	l := newLaunch(t)
	l.env.version = "dev"
	l.editDefs("definition_schema = 1", "definition_schema = 2")
	l.wantRefused("3", defsRef+": definition_schema = 2 is not read by this marvel (reads 1)")
}

// Row 4, step 8: the recipes library's requires must equal the definitions
// library's definition_schema.
func TestCheckRefusesAPairThatDisagrees(t *testing.T) {
	l := newLaunch(t)
	l.editRecs("requires = { definition_schema = 1 }", "requires = { definition_schema = 2 }")
	l.wantRefused("4", "recipes acme-recipes@2026-11-01-b41d0e2 requires definition_schema = 2, but company acme-roles@2026-11-01-ecc28ac has definition_schema = 1")
}

// Row 5, step 6, and the hazard cases: the marvel's alphas order by the date
// and time in them, and a prerelease is below its release.
func TestRequiresMarvelOrdersAlphas(t *testing.T) {
	const alphaEarly = "0.1.0-alpha.20261002.231214.b533ca5"
	const alphaLate = "0.1.0-alpha.20261008.044910.0d33f29"
	cases := []struct {
		name, requires, running string
		refused                 bool
	}{
		{"running alpha is earlier", alphaLate, alphaEarly, true},
		{"running alpha is later", alphaEarly, alphaLate, false},
		{"running alpha is the same", alphaLate, alphaLate, false},
		{"running prerelease is below the release it needs", "0.2.0", "0.2.0-alpha.1", true},
		{"running release is above a prerelease", "0.2.0-alpha.1", "0.2.0", false},
		{"numeric identifiers compare as numbers", "0.1.0-alpha.10", "0.1.0-alpha.9", true},
		{"running minor is lower", "0.10.0", "0.9.0", true},
		{"running minor is higher", "0.9.0", "0.10.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newLaunch(t)
			l.env.version = c.running
			l.editRecs(`requires_marvel = "0.9.0"`, `requires_marvel = "0.0.1"`)
			l.editDefs(`requires_marvel = "0.9.0"`, fmt.Sprintf("requires_marvel = %q", c.requires))
			if c.refused {
				l.wantRefused("5", fmt.Sprintf("%s requires marvel %s or newer; this is %s", defsRef, c.requires, c.running))
				return
			}
			if res := l.wantPasses(); res.RequiresMarvel != "ok" {
				t.Errorf("RequiresMarvel = %q, want ok", res.RequiresMarvel)
			}
		})
	}
	t.Run("the recipes library is held to its own requires_marvel", func(t *testing.T) {
		l := newLaunch(t)
		l.editRecs(`requires_marvel = "0.9.0"`, `requires_marvel = "0.10.0"`)
		l.wantRefused("5", recsRef+" requires marvel 0.10.0 or newer; this is 0.9.0")
	})
}

// A running build with no order, a dev build, passes the requirement and says
// that it did not check.
func TestRequiresMarvelPassesAnUnorderedVersion(t *testing.T) {
	l := newLaunch(t)
	l.env.version = "dev"
	l.editDefs(`requires_marvel = "0.9.0"`, `requires_marvel = "99.0.0"`)
	res := l.wantPasses()
	if res.RequiresMarvel != "unchecked: dev" {
		t.Fatalf("RequiresMarvel = %q, want \"unchecked: dev\"", res.RequiresMarvel)
	}
}

// Steps run in their written order: with a wrong kind (step 4) and a pair that
// disagrees (step 8), the refusal is the kind's; fixing the kind exposes the pair.
func TestResolveRunsTheChecksInOrder(t *testing.T) {
	l := newLaunch(t)
	l.editRecs("requires = { definition_schema = 1 }", "requires = { definition_schema = 2 }")
	env, req := l.build()
	swapped := req
	swapped.Company, swapped.Recipes = req.Recipes, req.Company
	_, err := Check(env, swapped)
	if r := refusalOf(t, err); r.Row != "14" {
		t.Fatalf("row = %s, want 14 first", r.Row)
	}
	_, err = Check(env, req)
	if r := refusalOf(t, err); r.Row != "4" {
		t.Fatalf("row = %s, want 4 once the kinds are right", r.Row)
	}
}

// N5, step 5: company.toml's version must equal its directory's. The resolver
// reads the bytes; the decoder compares.
func TestCheckRefusesAVersionThatDiffersFromItsDirectory(t *testing.T) {
	l := newLaunch(t)
	l.editDefs(`version = "2026-11-01-ecc28ac"`, `version = "2026-11-02-ecc28ac"`)
	l.wantRefused("N5", defsRef+`: company.toml: version "2026-11-02-ecc28ac" does not equal its directory "2026-11-01-ecc28ac"`)
}

// N16, step 9: the finished file must hash to its index entry.
func TestResolveRefusesAnEntryWhoseSHA256DiffersFromItsFile(t *testing.T) {
	l := newLaunch(t)
	l.role = "role text\n"
	env, req := l.build()
	if _, err := Check(env, req); err != nil {
		t.Fatalf("the matching twin must pass: %v", err)
	}
	// Change the file after the index recorded its hash. The tree changes too,
	// which Check does not compare here (no recorded hash is given to it).
	p := filepath.Join(req.Company.Dir, filepath.FromSlash(l.roleRel))
	if err := os.WriteFile(p, []byte("role text, edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Check(env, req)
	wantRefusal(t, err, "N16", defsRef+": roles/builder--steady.md: sha256 does not match the index entry role/builder--steady")
}

// Rows 10, 11 and 21, step 9.
func TestCheckRefusesARetiredDefinition(t *testing.T) {
	l := newLaunch(t)
	l.editDefs("\nstatus = \"ratified\"", "\nstatus = \"retired\"")
	l.wantRefused("10", "team core role builder: definition role/builder--steady is retired")
}

func TestCheckRefusesARetiredIdentity(t *testing.T) {
	l := newLaunch(t)
	l.editDefs(`identity_status = "ratified"`, `identity_status = "retired"`)
	l.wantRefused("11", "team core role builder: definition role/builder--steady has identity steady@1.0.0 marked retired")
}

func TestCheckRefusesAProposalWithoutAllowProposal(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		l := newLaunch(t)
		l.editDefs("\nstatus = \"ratified\"", "\nstatus = \"proposal\"")
		l.wantRefused("21", "team core role builder: definition role/builder--steady is a proposal (status) and the role does not set allow_proposal")
	})
	t.Run("identity_status", func(t *testing.T) {
		l := newLaunch(t)
		l.editDefs(`identity_status = "ratified"`, `identity_status = "proposal"`)
		l.wantRefused("21", "team core role builder: definition role/builder--steady is a proposal (identity_status) and the role does not set allow_proposal")
	})
	t.Run("allow_proposal passes", func(t *testing.T) {
		l := newLaunch(t)
		l.editDefs("\nstatus = \"ratified\"", "\nstatus = \"proposal\"")
		l.req.AllowProposal = true
		l.wantPasses()
	})
}

// Row 8: the pair's file already holds the identity text.
func TestCheckRefusesAPromptThatListsIdentityOnAPair(t *testing.T) {
	l := newLaunch(t)
	l.editRecipe(`prompt = ["template:cast", "definition", "notes"]`, `prompt = ["template:cast", "definition", "identity"]`)
	l.wantRefused("8", "recipe bus-seat: prompt lists identity, but definition role/builder--steady already carries identity steady@1.0.0")
	// Twin: a role on its own has no identity to repeat.
	t2 := newLaunch(t)
	t2.roleOnly()
	t2.editRecipe(`prompt = ["template:cast", "definition", "notes"]`, `prompt = ["template:cast", "definition", "identity"]`)
	t2.wantPasses()
}

// N6 (draft wording): a template the prompt lists must exist in the company.
func TestCheckRefusesATemplateTheCompanyLacks(t *testing.T) {
	l := newLaunch(t)
	l.editRecipe(`"template:cast"`, `"template:missing"`)
	l.wantRefused("N6", "recipe bus-seat: prompt lists template:missing, but company acme-roles@2026-11-01-ecc28ac has no template missing")
}

// Row 6: a parameter value that fails its pattern, naming the parameter and
// never the value.
func TestCheckRefusesAParameterThatFailsItsPattern(t *testing.T) {
	l := newLaunch(t)
	l.req.Params = map[string]string{"scope": "Not Valid SECRET-7"}
	env, req := l.build()
	_, err := Check(env, req)
	wantRefusal(t, err, "6", "team core role builder: parameter scope does not match its pattern")
	if strings.Contains(err.Error(), "SECRET-7") {
		t.Error("the refusal echoed the parameter value")
	}
}

// N27, next to row 6: a required parameter the role leaves unset, naming the
// parameter. An optional parameter left unset, and a set one, pass.
func TestCheckRefusesAnUnsetRequiredParameter(t *testing.T) {
	l := newLaunch(t)
	l.req.Params = map[string]string{}
	l.wantRefused("N27", "team core role builder: required parameter scope is not set")
	// Control: the same launch with the parameter set passes, and the optional
	// bool parameter cue is never required.
	ok := newLaunch(t)
	ok.req.Params = map[string]string{"scope": "alpha"}
	ok.wantPasses()
	// A parameter set to the empty string is set; the pattern decides whether it is valid.
	empty := newLaunch(t)
	empty.req.Params = map[string]string{"scope": ""}
	empty.wantRefused("6", "team core role builder: parameter scope does not match its pattern")
}

// Row 7: a tool the recipe names that the host has not set.
func TestUnsetToolRefuses(t *testing.T) {
	l := newLaunch(t)
	delete(l.env.tools, "stub-shim")
	l.wantRefused("7", `recipe bus-seat: tool "stub-shim" is not set; run marvel config tools set stub-shim <path>`)
	// Preflight's tool is read too.
	l2 := newLaunch(t)
	delete(l2.env.tools, "stub-preflight")
	l2.wantRefused("7", `recipe bus-seat: tool "stub-preflight" is not set; run marvel config tools set stub-preflight <path>`)
}

// N7 (draft wording): a {var:} the host has not set, at the key that uses it.
func TestCheckRefusesAnUnsetVar(t *testing.T) {
	l := newLaunch(t)
	delete(l.env.vars, "bus.global_domain")
	l.wantRefused("N7", `recipe bus-seat: when.global.holder.env.BUS_GLOBAL_DOMAIN: var "bus.global_domain" is not set; run marvel config vars set bus.global_domain <value>`)
}

// Row 9, step 10: neither the recipe nor the role wins a key.
func TestCheckRefusesAKeyTheRecipeAndTheRoleBothName(t *testing.T) {
	t.Run("base env", func(t *testing.T) {
		l := newLaunch(t)
		l.req.RoleEnv = []string{"BUS_ROLE"}
		l.wantRefused("9", "team core role builder: recipe bus-seat and runtime.env both name BUS_ROLE")
	})
	t.Run("an applying when block", func(t *testing.T) {
		l := newLaunch(t)
		l.req.Params["cue"] = "true"
		l.req.RoleEnv = []string{"BUS_CUE"}
		l.wantRefused("9", "team core role builder: recipe bus-seat and runtime.env both name BUS_CUE")
	})
	t.Run("a when block that does not apply is not read", func(t *testing.T) {
		l := newLaunch(t)
		l.req.RoleEnv = []string{"BUS_CUE"}
		l.wantPasses()
	})
	t.Run("the unset of the branch the role is on", func(t *testing.T) {
		l := newLaunch(t)
		l.req.RoleEnv = []string{"BUS_GLOBAL_ROLE"}
		l.wantRefused("9", "team core role builder: recipe bus-seat and runtime.env both name BUS_GLOBAL_ROLE")
		h := newLaunch(t)
		h.req.GlobalHolder = true
		h.req.RoleEnv = []string{"BUS_GLOBAL_ROLE"}
		h.wantPasses()
	})
}

// A request with no recipes pin checks the definition and nothing of a recipe.
func TestCheckWithoutARecipesPin(t *testing.T) {
	l := newLaunch(t)
	l.noRecs = true
	l.req.Recipe = ""
	res := l.wantPasses()
	if res.Company == nil || res.Recipes != nil || res.Recipe != nil || res.RecipesHash != "" {
		t.Fatalf("result carries recipes data with no pin: %+v", res)
	}
}
