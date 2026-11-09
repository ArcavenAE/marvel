package company

import "time"

// Predicates are the checks the package cannot make itself, because they need
// something outside it: internal/runtime for the keys marvel constructs and
// the registered adapters, and the build's measured pre-flight ceiling.
type Predicates struct {
	// Constructed reports a key that marvel sets for a seat itself (the
	// identity keys and the three address keys). Nil means none.
	Constructed func(key string) bool
	// AdapterRegistered reports a registered adapter name. Nil means none.
	AdapterRegistered func(name string) bool
	// PreflightCeiling is the largest pre-flight timeout a recipe may name.
	// Zero means no ceiling.
	PreflightCeiling time.Duration
}

// RecipeContext is what a recipe is read against.
type RecipeContext struct {
	Library      Ident
	Name         string
	RecipeSchema int
	Pred         Predicates
}

// MCPServer is one [mcp.<name>] table.
type MCPServer struct {
	Command string
	Args    []string
	Strict  bool
}

// Preflight is the [preflight] table.
type Preflight struct {
	Command []string
	Timeout time.Duration
}

// Param is one [params.<name>] table.
type Param struct {
	Type     string
	Required bool
	Pattern  string
}

// When is a [when.<param>] or [when.global.<branch>] table.
type When struct {
	Env   map[string]string
	Args  []string
	Unset []string
}

// Recipe is a decoded recipe file.
type Recipe struct {
	Schema    int
	Adapter   string
	Command   string
	Prompt    []string
	Args      []string
	Env       map[string]string
	MCP       map[string]MCPServer
	Preflight *Preflight
	Params    map[string]Param
	When      map[string]When
	Holder    *When
	NonHolder *When
}

// DecodeRecipe decodes the bytes of one recipe file. STUB: accepts everything.
func DecodeRecipe(ctx RecipeContext, data []byte) (*Recipe, error) { return &Recipe{}, nil }

// credentialKeys are the five keys a role may not override, the credentials
// marvel or the broker checks to decide whether a session is who it says it
// is. A recipe may not name them either. STUB: empty.
var credentialKeys = []string{}
