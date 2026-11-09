package company

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/arcavenae/marvel/internal/buildversion"
)

// readSchemas are the definition_schema and recipe_schema values this marvel
// reads.
var (
	readDefinitionSchemas = []int{1}
	readRecipeSchemas     = []int{1}
)

// Request is one launch's inputs to Check: the team and role it is for, the
// definition the role names, the two resolved libraries, the recipe name, the
// keys the role's runtime.env names, the role's parameters, and whether the
// role holds the global tier. A Recipes library with no name means the team
// pins no recipes.
//
// Owner reading, proposed for review before merge: the field set and the names
// Request, Result and Check.
type Request struct {
	Team, Role    string
	Definition    string
	AllowProposal bool
	GlobalHolder  bool
	Company       Library
	Recipes       Library
	Recipe        string
	RoleEnv       []string
	Params        map[string]string
	Pred          Predicates
}

// Result is what Check read and proved. RequiresMarvel is "ok" when the
// running marvel was ordered against each library's requires_marvel, and
// "unchecked: dev" when the running version has no order (a dev build), in
// which case the requirement passes unchecked. The once-per-run warning and the
// session record field belong to the launch record, not to this package.
type Result struct {
	Company        *Company
	Recipes        *Company
	CompanyHash    string
	RecipesHash    string
	Definition     Definition
	Recipe         *Recipe
	RequiresMarvel string
}

// Check runs the checks a launch makes after Resolve, steps 3 to 10 of
// company-recipe-schema.md "Checks at launch, in order", and stops at the first
// refusal. Step 3 computes each library's tree hash and records it on the
// Result; the settled refusals for it (a writable tree, a hash that differs from
// the one first seen) need a recorded hash this package is not given, so it
// refuses nothing there.
func Check(env Env, req Request) (*Result, error) {
	res := &Result{RequiresMarvel: "ok"}
	hasRecipes := req.Recipes.Name != ""

	// 3. E0: the tree hash of each version.
	var err error
	if res.CompanyHash, err = TreeHash(req.Company.Dir); err != nil {
		return nil, fmt.Errorf("hash %s@%s: %w", req.Company.Name, req.Company.Version, err)
	}
	if hasRecipes {
		if res.RecipesHash, err = TreeHash(req.Recipes.Dir); err != nil {
			return nil, fmt.Errorf("hash %s@%s: %w", req.Recipes.Name, req.Recipes.Version, err)
		}
	}

	// 4. kind.
	if err := checkKind(req.Team, "company", req.Company, "definitions"); err != nil {
		return nil, err
	}
	if hasRecipes {
		if err := checkKind(req.Team, "recipes", req.Recipes, "recipes"); err != nil {
			return nil, err
		}
	}

	// 5. The index: each entry's identity agrees with its id, and the file reads.
	if res.Company, err = DecodeCompany(req.Company.Ident, req.Company.Data); err != nil {
		return nil, err
	}
	if hasRecipes {
		if res.Recipes, err = DecodeCompany(req.Recipes.Ident, req.Recipes.Data); err != nil {
			return nil, err
		}
	}

	// 6. requires_marvel.
	running := env.MarvelVersion()
	for _, c := range []*Company{res.Company, res.Recipes} {
		if c == nil {
			continue
		}
		older, known := buildversion.VersionOlder(running, c.RequiresMarvel)
		switch {
		case !known && !orderable(running):
			res.RequiresMarvel = "unchecked: dev"
		case !known || older:
			return nil, refuse("5", c.Name, c.Version, c.RequiresMarvel, running)
		}
	}

	// 7. Each library's own schema, and the recipe file's.
	if !slices.Contains(readDefinitionSchemas, res.Company.DefinitionSchema) {
		return nil, refuse("3", res.Company.Name, res.Company.Version, "definition_schema", strconv.Itoa(res.Company.DefinitionSchema), joinInts(readDefinitionSchemas))
	}
	if hasRecipes {
		if !slices.Contains(readRecipeSchemas, res.Recipes.RecipeSchema) {
			return nil, refuse("3", res.Recipes.Name, res.Recipes.Version, "recipe_schema", strconv.Itoa(res.Recipes.RecipeSchema), joinInts(readRecipeSchemas))
		}
		data, err := os.ReadFile(filepath.Join(req.Recipes.Dir, "recipes", req.Recipe+".toml"))
		if err != nil {
			return nil, fmt.Errorf("read recipe %s of %s@%s: %w", req.Recipe, req.Recipes.Name, req.Recipes.Version, err)
		}
		rctx := RecipeContext{Library: req.Recipes.Ident, Name: req.Recipe, RecipeSchema: res.Recipes.RecipeSchema, Pred: req.Pred}
		if res.Recipe, err = DecodeRecipe(rctx, data); err != nil {
			return nil, err
		}
	}

	// 8. The pair.
	if hasRecipes && res.Recipes.Requires.DefinitionSchema != res.Company.DefinitionSchema {
		return nil, refuse("4", res.Recipes.Name, res.Recipes.Version, strconv.Itoa(res.Recipes.Requires.DefinitionSchema),
			res.Company.Name, res.Company.Version, strconv.Itoa(res.Company.DefinitionSchema))
	}

	// 9. Read the definition and the recipe.
	if err := checkDefinition(env, req, res); err != nil {
		return nil, err
	}

	// 10. Merge the environment: no key is named by the recipe and the role.
	if res.Recipe != nil {
		if k, ok := conflictingKey(res.Recipe, req); ok {
			return nil, refuse("9", req.Team, req.Role, req.Recipe, k)
		}
	}
	return res, nil
}

// orderable reports whether v has an order at all: it parses as a version.
func orderable(v string) bool {
	_, known := buildversion.VersionOlder(v, v)
	return known
}

func joinInts(l []int) string {
	s := make([]string, len(l))
	for i, n := range l {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// checkKind reads the kind out of company.toml, ahead of the full decode, so a
// pin of the wrong kind is refused before anything else about the file is. A
// file that does not parse is left to the decode.
func checkKind(team, key string, lib Library, want string) error {
	tree, _, _, err := parseTOML(lib.Data)
	if err != nil {
		return nil
	}
	kind, _ := tree["kind"].(string)
	if kind == "" || kind == want {
		return nil
	}
	return refuse("14", team, key, lib.Name, lib.Version, kind, key, want)
}

// checkDefinition is step 9: the definition's file against its index entry, its
// status, and what the recipe says about it.
func checkDefinition(env Env, req Request, res *Result) error {
	var entry *Definition
	for i := range res.Company.Definitions {
		if res.Company.Definitions[i].ID == req.Definition {
			entry = &res.Company.Definitions[i]
		}
	}
	if entry == nil {
		return fmt.Errorf("definition %s is not in company %s@%s", req.Definition, res.Company.Name, res.Company.Version)
	}
	res.Definition = *entry

	rel := "roles/" + strings.TrimPrefix(entry.ID, "role/") + ".md"
	data, err := os.ReadFile(filepath.Join(req.Company.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read %s of %s@%s: %w", rel, res.Company.Name, res.Company.Version, err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != entry.SHA256 {
		return refuse("N16", res.Company.Name, res.Company.Version, rel, entry.ID)
	}
	switch {
	case entry.Status == "retired":
		return refuse("10", req.Team, req.Role, entry.ID)
	case entry.IdentityStatus == "retired":
		return refuse("11", req.Team, req.Role, entry.ID, entry.Identity)
	case !req.AllowProposal && entry.Status == "proposal":
		return refuse("21", req.Team, req.Role, entry.ID, "status")
	case !req.AllowProposal && entry.IdentityStatus == "proposal":
		return refuse("21", req.Team, req.Role, entry.ID, "identity_status")
	}
	rec := res.Recipe
	if rec == nil {
		return nil
	}
	if entry.Identity != "" && slices.Contains(rec.Prompt, "identity") {
		return refuse("8", req.Recipe, entry.ID, entry.Identity)
	}
	for _, p := range rec.Prompt {
		t, ok := strings.CutPrefix(p, "template:")
		if !ok {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(req.Company.Dir, "templates", t+".txt")); err != nil || !fi.Mode().IsRegular() {
			return refuse("N6", req.Recipe, t, res.Company.Name, res.Company.Version, t)
		}
	}
	for name, p := range rec.Params {
		v, set := req.Params[name]
		if !set || p.Pattern == "" {
			continue
		}
		re, err := regexp.Compile(p.Pattern)
		if err != nil {
			return fmt.Errorf("recipe %s: parameter %s: pattern: %w", req.Recipe, name, err)
		}
		if !re.MatchString(v) {
			return refuse("6", req.Team, req.Role, name)
		}
	}
	for _, v := range rec.valueList() {
		if tool, ok := strings.CutPrefix(v.s, "tool:"); ok && v.toolOK {
			if _, set := env.Tool(tool); !set {
				return refuse("7", req.Recipe, tool, tool)
			}
		}
	}
	for _, v := range rec.valueList() {
		for _, ph := range placeholderRE.FindAllString(v.s, -1) {
			name, ok := strings.CutPrefix(ph[1:len(ph)-1], "var:")
			if !ok || name == "" {
				continue
			}
			if _, set := env.Var(name); !set {
				return refuse("N7", req.Recipe, v.key, name, name)
			}
		}
	}
	return nil
}

// conflictingKey returns the first key, in name order, that the recipe sets or
// unsets in the blocks this launch applies and the role's runtime.env names. A
// when.<param> block applies when the role's parameter is "true", and the
// global holder or non-holder block applies by the role's tier.
func conflictingKey(rec *Recipe, req Request) (string, bool) {
	named := map[string]bool{}
	add := func(env map[string]string, unset []string) {
		for k := range env {
			named[k] = true
		}
		for _, k := range unset {
			named[k] = true
		}
	}
	add(rec.Env, nil)
	for n, w := range rec.When {
		if req.Params[n] == "true" {
			add(w.Env, w.Unset)
		}
	}
	switch {
	case req.GlobalHolder && rec.Holder != nil:
		add(rec.Holder.Env, rec.Holder.Unset)
	case !req.GlobalHolder && rec.NonHolder != nil:
		add(rec.NonHolder.Env, rec.NonHolder.Unset)
	}
	for _, k := range slices.Sorted(slices.Values(req.RoleEnv)) {
		if named[k] {
			return k, true
		}
	}
	return "", false
}
