package company

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

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

// credentialKeys are the five keys a role may not override, the credentials
// marvel or the broker checks to decide whether a session is who it says it
// is. A recipe may not name them either. They are the values of
// runtime.roleEnvRefusedKeys, which this package cannot import.
var credentialKeys = []string{
	api.HeartbeatTokenEnv, api.CredentialHelperEnv, api.HeartbeatTokenFileEnv,
	"DIRECTOR_NATS_USER", "DIRECTOR_NATS_PASS",
}

func isCredentialKey(k string) bool {
	return slices.Contains(credentialKeys, k) || api.BackendBearerEnv(k)
}

var (
	plainNameRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	placeholderRE = regexp.MustCompile(`\{[^{}]*\}`)
)

func recipeAllowed(p []string) bool {
	switch p[0] {
	case "schema", "adapter", "command", "prompt", "args":
		return len(p) == 1
	case "env":
		return len(p) <= 2
	case "mcp":
		return len(p) <= 2 || (len(p) == 3 && (p[2] == "command" || p[2] == "args" || p[2] == "strict"))
	case "preflight":
		return len(p) == 1 || (len(p) == 2 && (p[1] == "command" || p[1] == "timeout"))
	case "params":
		return len(p) <= 2 || (len(p) == 3 && (p[2] == "type" || p[2] == "required" || p[2] == "pattern"))
	case "when":
		return whenAllowed(p[1:])
	}
	return false
}

func whenAllowed(p []string) bool {
	if len(p) == 0 {
		return true
	}
	if p[0] == "global" {
		if len(p) <= 2 {
			return len(p) == 1 || p[1] == "holder" || p[1] == "non_holder"
		}
		return p[1] != "" && (p[1] == "holder" || p[1] == "non_holder") && whenBlockAllowed(p[2:])
	}
	return whenBlockAllowed(p[1:])
}

// whenBlockAllowed accepts the keys under one when block, given what follows
// the block's own name.
func whenBlockAllowed(p []string) bool {
	if len(p) == 0 {
		return true
	}
	switch p[0] {
	case "env":
		return len(p) <= 2
	case "args", "unset":
		return len(p) == 1
	}
	return false
}

func asStrings(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

func asStringTable(v any) (map[string]string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(m))
	for k, e := range m {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[k] = s
	}
	return out, true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// recipeDecoder carries the context of one DecodeRecipe call.
type recipeDecoder struct {
	ctx    RecipeContext
	file   string
	params map[string]Param
}

func (d *recipeDecoder) lib(id string, f ...string) error {
	return refuse(id, append([]string{d.ctx.Library.Name, d.ctx.Library.Version}, f...)...)
}

func (d *recipeDecoder) rec(id string, f ...string) error {
	return refuse(id, append([]string{d.ctx.Name}, f...)...)
}

func (d *recipeDecoder) form(key, form string) error { return d.rec("N24", key, form) }

// DecodeRecipe decodes the bytes of one recipe file. It is strict in every
// table, and a refusal is a *Refusal with no Recipe returned.
func DecodeRecipe(ctx RecipeContext, data []byte) (*Recipe, error) {
	d := &recipeDecoder{ctx: ctx, file: "recipes/" + ctx.Name + ".toml", params: map[string]Param{}}
	tree, keys, line, err := parseTOML(data)
	if err != nil {
		return nil, d.rec("N26", ctx.Name, fmt.Sprint(line))
	}
	if key, bad := firstUnknown(keys, recipeAllowed); bad {
		return nil, d.lib("1", d.file, key)
	}
	for _, k := range []string{"schema", "adapter"} {
		if _, ok := tree[k]; !ok {
			return nil, d.lib("N19", d.file, k)
		}
	}
	rec := &Recipe{Env: map[string]string{}}
	if err := d.top(tree, rec); err != nil {
		return nil, err
	}
	if err := d.params_(tree, rec); err != nil {
		return nil, err
	}
	if err := d.env(tree, rec); err != nil {
		return nil, err
	}
	if err := d.mcp(tree, rec); err != nil {
		return nil, err
	}
	if err := d.preflight(tree, rec); err != nil {
		return nil, err
	}
	if err := d.when(tree, rec); err != nil {
		return nil, err
	}
	if err := d.values(rec); err != nil {
		return nil, err
	}
	if !d.ctx.Pred.registered(rec.Adapter) {
		return nil, d.rec("18", rec.Adapter)
	}
	if rec.Schema != ctx.RecipeSchema {
		return nil, d.rec("19", fmt.Sprint(rec.Schema), fmt.Sprint(ctx.RecipeSchema))
	}
	if rec.Preflight != nil && ctx.Pred.PreflightCeiling > 0 && rec.Preflight.Timeout > ctx.Pred.PreflightCeiling {
		return nil, d.rec("20", rec.Preflight.Timeout.String(), ctx.Pred.PreflightCeiling.String())
	}
	if err := d.conflicts(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (p Predicates) registered(name string) bool {
	return p.AdapterRegistered != nil && p.AdapterRegistered(name)
}

func (d *recipeDecoder) top(tree map[string]any, rec *Recipe) error {
	n, ok := tree["schema"].(int64)
	if !ok {
		return d.form("schema", "an integer")
	}
	rec.Schema = int(n)
	if s, ok := tree["adapter"].(string); !ok || s == "" {
		return d.form("adapter", "a non-empty string")
	} else {
		rec.Adapter = s
	}
	if v, ok := tree["command"]; ok {
		if s, ok := v.(string); !ok || s == "" {
			return d.form("command", "a non-empty string")
		} else {
			rec.Command = s
		}
	}
	for _, f := range []struct {
		key string
		dst *[]string
	}{{"prompt", &rec.Prompt}, {"args", &rec.Args}} {
		if v, ok := tree[f.key]; ok {
			l, ok := asStrings(v)
			if !ok {
				return d.form(f.key, "a list of strings")
			}
			*f.dst = l
		}
	}
	return nil
}

func (d *recipeDecoder) params_(tree map[string]any, rec *Recipe) error {
	v, ok := tree["params"]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return d.form("params", "a table")
	}
	rec.Params = map[string]Param{}
	for _, name := range sortedKeys(m) {
		label := "params." + name
		t, ok := m[name].(map[string]any)
		if !ok {
			return d.form(label, "a table")
		}
		if name == "global" {
			return d.rec("N3")
		}
		tv, ok := t["type"]
		if !ok {
			return d.lib("N19", d.file, label+".type")
		}
		var p Param
		if p.Type, ok = tv.(string); !ok || (p.Type != "string" && p.Type != "bool") {
			return d.form(label+".type", `"string" or "bool"`)
		}
		if rv, ok := t["required"]; ok {
			if p.Required, ok = rv.(bool); !ok {
				return d.form(label+".required", "a boolean")
			}
		}
		if pv, ok := t["pattern"]; ok {
			if p.Pattern, ok = pv.(string); !ok {
				return d.form(label+".pattern", "a regular expression")
			}
			if _, err := regexp.Compile(p.Pattern); err != nil {
				return d.form(label+".pattern", "a regular expression")
			}
		}
		rec.Params[name] = p
		d.params[name] = p
	}
	return nil
}

// names checks the plain-name form and the two refused classes for the keys of
// one env table or unset list. set is true for an env table, where
// DIRECTOR_GLOBAL_ROLE is refused; as an unset it is allowed.
func (d *recipeDecoder) names(block, section string, keys []string, set bool) error {
	for _, k := range keys {
		if !plainNameRE.MatchString(k) {
			return d.lib("N21", d.ctx.Name, section, k)
		}
		if isCredentialKey(k) {
			return d.rec("N1", block, k)
		}
		if k == "DIRECTOR_CLUSTER" || (k == "DIRECTOR_GLOBAL_ROLE" && !set) {
			continue
		}
		if (k == "DIRECTOR_GLOBAL_ROLE" && set) || (d.ctx.Pred.Constructed != nil && d.ctx.Pred.Constructed(k)) {
			return d.rec("N2", block, k)
		}
	}
	return nil
}

func (d *recipeDecoder) envTable(block string, v any) (map[string]string, error) {
	t, ok := asStringTable(v)
	if !ok {
		return nil, d.form(block, "a table of strings")
	}
	if err := d.names(block, "env", sortedKeys(t), true); err != nil {
		return nil, err
	}
	return t, nil
}

func (d *recipeDecoder) env(tree map[string]any, rec *Recipe) error {
	v, ok := tree["env"]
	if !ok {
		return nil
	}
	t, err := d.envTable("env", v)
	if err != nil {
		return err
	}
	rec.Env = t
	return nil
}

func (d *recipeDecoder) mcp(tree map[string]any, rec *Recipe) error {
	v, ok := tree["mcp"]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return d.form("mcp", "a table")
	}
	rec.MCP = map[string]MCPServer{}
	for _, name := range sortedKeys(m) {
		label := "mcp." + name
		t, ok := m[name].(map[string]any)
		if !ok {
			return d.form(label, "a table")
		}
		var s MCPServer
		if cv, ok := t["command"]; ok {
			if s.Command, ok = cv.(string); !ok || s.Command == "" {
				return d.form(label+".command", "a non-empty string")
			}
		}
		if av, ok := t["args"]; ok {
			if s.Args, ok = asStrings(av); !ok {
				return d.form(label+".args", "a list of strings")
			}
		}
		if sv, ok := t["strict"]; ok {
			if s.Strict, ok = sv.(bool); !ok {
				return d.form(label+".strict", "a boolean")
			}
		}
		rec.MCP[name] = s
	}
	return nil
}

func (d *recipeDecoder) preflight(tree map[string]any, rec *Recipe) error {
	v, ok := tree["preflight"]
	if !ok {
		return nil
	}
	t, ok := v.(map[string]any)
	if !ok {
		return d.form("preflight", "a table")
	}
	pf := &Preflight{}
	if cv, ok := t["command"]; ok {
		if pf.Command, ok = asStrings(cv); !ok || len(pf.Command) == 0 {
			return d.form("preflight.command", "a non-empty list of strings")
		}
	}
	if tv, ok := t["timeout"]; ok {
		s, ok := tv.(string)
		var err error
		if ok {
			pf.Timeout, err = time.ParseDuration(s)
		}
		if !ok || err != nil || pf.Timeout <= 0 {
			return d.form("preflight.timeout", "a duration")
		}
	}
	rec.Preflight = pf
	return nil
}

func (d *recipeDecoder) whenBlock(label string, v any) (*When, error) {
	t, ok := v.(map[string]any)
	if !ok {
		return nil, d.form(label, "a table")
	}
	w := &When{}
	var err error
	if ev, ok := t["env"]; ok {
		if w.Env, err = d.envTable(label+".env", ev); err != nil {
			return nil, err
		}
	}
	if av, ok := t["args"]; ok {
		if w.Args, ok = asStrings(av); !ok {
			return nil, d.form(label+".args", "a list of strings")
		}
	}
	if uv, ok := t["unset"]; ok {
		if w.Unset, ok = asStrings(uv); !ok {
			return nil, d.form(label+".unset", "a list of strings")
		}
		if err := d.names(label+".unset", "unset", w.Unset, false); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func (d *recipeDecoder) when(tree map[string]any, rec *Recipe) error {
	v, ok := tree["when"]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return d.form("when", "a table")
	}
	rec.When = map[string]When{}
	for _, name := range sortedKeys(m) {
		label := "when." + name
		if name == "global" {
			g, ok := m[name].(map[string]any)
			if !ok {
				return d.form(label, "a table")
			}
			for _, branch := range []string{"holder", "non_holder"} {
				bv, ok := g[branch]
				if !ok {
					continue
				}
				w, err := d.whenBlock(label+"."+branch, bv)
				if err != nil {
					return err
				}
				if branch == "holder" {
					rec.Holder = w
				} else {
					rec.NonHolder = w
				}
			}
			continue
		}
		p, declared := d.params[name]
		if !declared {
			return d.rec("16", label, name)
		}
		if p.Type != "bool" {
			return d.form(label, "a bool parameter")
		}
		w, err := d.whenBlock(label, m[name])
		if err != nil {
			return err
		}
		rec.When[name] = *w
	}
	return nil
}

// value is one string a recipe carries, with the key it is read at and whether
// a tool: name is allowed there.
type value struct {
	key    string
	s      string
	toolOK bool
}

func (d *recipeDecoder) values(rec *Recipe) error {
	var vs []value
	if rec.Command != "" {
		vs = append(vs, value{"command", rec.Command, true})
	}
	list := func(prefix string, l []string, firstTool bool) {
		for i, s := range l {
			vs = append(vs, value{fmt.Sprintf("%s[%d]", prefix, i), s, firstTool && i == 0})
		}
	}
	list("args", rec.Args, false)
	envs := func(prefix string, m map[string]string) {
		for _, k := range sortedKeys(m) {
			vs = append(vs, value{prefix + "." + k, m[k], false})
		}
	}
	envs("env", rec.Env)
	for _, n := range sortedKeys(rec.MCP) {
		m := rec.MCP[n]
		if m.Command != "" {
			vs = append(vs, value{"mcp." + n + ".command", m.Command, true})
		}
		list("mcp."+n+".args", m.Args, false)
	}
	if rec.Preflight != nil {
		list("preflight.command", rec.Preflight.Command, true)
	}
	block := func(label string, w *When) {
		envs(label+".env", w.Env)
		list(label+".args", w.Args, false)
	}
	for _, n := range sortedKeys(rec.When) {
		w := rec.When[n]
		block("when."+n, &w)
	}
	if rec.Holder != nil {
		block("when.global.holder", rec.Holder)
	}
	if rec.NonHolder != nil {
		block("when.global.non_holder", rec.NonHolder)
	}
	for _, v := range vs {
		if strings.HasPrefix(v.s, "tool:") && !v.toolOK {
			return d.rec("N4", v.key)
		}
		for _, ph := range placeholderRE.FindAllString(v.s, -1) {
			inner := ph[1 : len(ph)-1]
			switch {
			case inner == "role", inner == "team", inner == "workspace", inner == "session":
			case strings.HasPrefix(inner, "var:") && len(inner) > len("var:"):
			case strings.HasPrefix(inner, "param:") && len(inner) > len("param:"):
				name := strings.TrimPrefix(inner, "param:")
				if _, ok := d.params[name]; !ok {
					return d.rec("16", v.key, name)
				}
			default:
				return d.rec("15", v.key, ph)
			}
		}
	}
	return nil
}

// conflicts refuses two blocks that can apply to one launch and name one key.
// A block names a key by setting it in env or listing it in unset.
func (d *recipeDecoder) conflicts(rec *Recipe) error {
	type blk struct {
		name string
		keys []string
		set  []string
		un   []string
	}
	mk := func(name string, env map[string]string, unset []string) blk {
		b := blk{name: name, set: sortedKeys(env), un: unset}
		b.keys = append(append([]string{}, b.set...), unset...)
		return b
	}
	blocks := []blk{mk("env", rec.Env, nil)}
	for _, n := range sortedKeys(rec.When) {
		w := rec.When[n]
		blocks = append(blocks, mk("when."+n, w.Env, w.Unset))
	}
	if rec.Holder != nil {
		blocks = append(blocks, mk("when.global.holder", rec.Holder.Env, rec.Holder.Unset))
	}
	if rec.NonHolder != nil {
		blocks = append(blocks, mk("when.global.non_holder", rec.NonHolder.Env, rec.NonHolder.Unset))
	}
	for _, b := range blocks {
		for _, k := range b.set {
			if slices.Contains(b.un, k) {
				return d.rec("17", b.name, b.name, k)
			}
		}
	}
	for i := range blocks {
		for j := i + 1; j < len(blocks); j++ {
			a, b := blocks[i], blocks[j]
			if a.name == "when.global.holder" && b.name == "when.global.non_holder" {
				continue
			}
			for _, k := range slices.Sorted(slices.Values(a.keys)) {
				if slices.Contains(b.keys, k) {
					return d.rec("17", a.name, b.name, k)
				}
			}
		}
	}
	return nil
}
