package company

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Ident names a library version the way its directory does.
type Ident struct{ Name, Version string }

// Source is where a library was built from.
type Source struct {
	URL          string
	Commit       string
	ContentsTree string
}

// Requires is the definitions shape a recipes library expects.
type Requires struct{ DefinitionSchema int }

// Definition is one entry of a definitions library's index.
type Definition struct {
	ID             string
	Version        string
	Identity       string
	Status         string
	IdentityStatus string
	SHA256         string
	Slice          string
	Border         string
}

// Company is a decoded company.toml.
type Company struct {
	Name             string
	Version          string
	Kind             string
	Schema           int
	RequiresMarvel   string
	Source           Source
	DefinitionSchema int
	RecipeSchema     int
	Requires         Requires
	Definitions      []Definition
}

// parseTOML reads data as TOML into a generic table and returns the keys in
// document order. A syntax error is returned as the line it is on and nothing
// of the file's text, since the file may hold anything.
func parseTOML(data []byte) (map[string]any, []toml.Key, int, error) {
	var tree map[string]any
	md, err := toml.Decode(string(data), &tree)
	if err != nil {
		line := 0
		var pe toml.ParseError
		if errors.As(err, &pe) {
			line = pe.Position.Line
		}
		var pp *toml.ParseError
		if errors.As(err, &pp) && pp != nil {
			line = pp.Position.Line
		}
		return nil, nil, line, err
	}
	return tree, md.Keys(), 0, nil
}

// firstUnknown returns the dotted path of the first key, in document order,
// that allowed does not accept.
func firstUnknown(keys []toml.Key, allowed func(path []string) bool) (string, bool) {
	for _, k := range keys {
		if !allowed(k) {
			return strings.Join(k, "."), true
		}
	}
	return "", false
}

var (
	companyTop    = []string{"name", "version", "kind", "schema", "requires_marvel", "source"}
	definitionKey = []string{"id", "version", "identity", "status", "identity_status", "sha256", "slice", "border"}
)

func companyAllowed(kind string) func([]string) bool {
	defs := kind != "recipes"
	recs := kind != "definitions"
	in := func(s string, list []string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	return func(p []string) bool {
		switch p[0] {
		case "definition_schema", "definitions":
			if !defs {
				return false
			}
		case "recipe_schema", "requires":
			if !recs {
				return false
			}
		default:
			if !in(p[0], companyTop) {
				return false
			}
		}
		switch len(p) {
		case 1:
			return true
		case 2:
			switch p[0] {
			case "source":
				return p[1] == "url" || p[1] == "commit" || (p[1] == "contents_tree" && defs)
			case "requires":
				return p[1] == "definition_schema"
			case "definitions":
				return in(p[1], definitionKey)
			}
		}
		return false
	}
}

var (
	hexRE      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sha256ColE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	versionRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	nameRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	identityRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*@[A-Za-z0-9][A-Za-z0-9._+-]*$`)
)

// idParts splits a definition id into its role and, for a pair, its identity
// name. ok is false when the id is not well formed.
func idParts(id string) (role, ident string, pair, ok bool) {
	rest, found := strings.CutPrefix(id, "role/")
	if !found || rest == "" {
		return "", "", false, false
	}
	role, ident, pair = strings.Cut(rest, "--")
	if !nameRE.MatchString(role) || strings.HasSuffix(role, "-") {
		return "", "", false, false
	}
	if pair && (!nameRE.MatchString(ident) || strings.HasSuffix(ident, "-") || strings.Contains(ident, "--")) {
		return "", "", false, false
	}
	return role, ident, pair, true
}

// DecodeCompany decodes the bytes of the company.toml found in the version
// directory named by dir. It is strict: an unknown key is refused, a required
// key must be present, a key must have its form, and name and version must
// equal the directory's. A refusal is a *Refusal and no Company is returned.
func DecodeCompany(dir Ident, data []byte) (*Company, error) {
	const file = "company.toml"
	r := func(id string, f ...string) error {
		return refuse(id, append([]string{dir.Name, dir.Version}, f...)...)
	}
	tree, keys, line, err := parseTOML(data)
	if err != nil {
		return nil, r("N25", fmt.Sprint(line))
	}
	kind, _ := tree["kind"].(string)
	if key, bad := firstUnknown(keys, companyAllowed(kind)); bad {
		return nil, r("1", file, key)
	}
	for _, k := range companyTop {
		if _, ok := tree[k]; !ok {
			return nil, r("N19", file, k)
		}
	}
	if kind != "definitions" && kind != "recipes" {
		return nil, r("N23", "kind", `"definitions" or "recipes"`)
	}
	src, ok := tree["source"].(map[string]any)
	if !ok {
		return nil, r("N23", "source", "a table")
	}
	if _, ok := src["url"]; !ok {
		return nil, r("N19", file, "source.url")
	}
	c := &Company{Kind: kind}
	if kind == "definitions" {
		for _, k := range []string{"definition_schema", "definitions"} {
			if _, ok := tree[k]; !ok {
				return nil, r("N19", file, k)
			}
		}
		if _, ok := src["contents_tree"]; !ok {
			return nil, r("N19", file, "source.contents_tree")
		}
	} else {
		for _, k := range []string{"recipe_schema", "requires"} {
			if _, ok := tree[k]; !ok {
				return nil, r("N19", file, k)
			}
		}
		if _, ok := src["commit"]; !ok {
			return nil, r("N19", file, "source.commit")
		}
		req, ok := tree["requires"].(map[string]any)
		if !ok {
			return nil, r("N23", "requires", "a table")
		}
		if _, ok := req["definition_schema"]; !ok {
			return nil, r("N19", file, "requires.definition_schema")
		}
	}

	str := func(m map[string]any, path, k string, dst *string) error {
		v, ok := m[k]
		if !ok {
			return nil
		}
		s, ok := v.(string)
		if !ok {
			return r("N23", path+k, "a string")
		}
		*dst = s
		return nil
	}
	num := func(m map[string]any, path, k string, dst *int) error {
		v, ok := m[k]
		if !ok {
			return nil
		}
		n, ok := v.(int64)
		if !ok {
			return r("N23", path+k, "an integer")
		}
		*dst = int(n)
		return nil
	}
	for _, f := range []error{
		str(tree, "", "name", &c.Name), str(tree, "", "version", &c.Version),
		str(tree, "", "requires_marvel", &c.RequiresMarvel),
		num(tree, "", "schema", &c.Schema),
		num(tree, "", "definition_schema", &c.DefinitionSchema),
		num(tree, "", "recipe_schema", &c.RecipeSchema),
		str(src, "source.", "url", &c.Source.URL), str(src, "source.", "commit", &c.Source.Commit),
		str(src, "source.", "contents_tree", &c.Source.ContentsTree),
	} {
		if f != nil {
			return nil, f
		}
	}
	if req, ok := tree["requires"].(map[string]any); ok {
		if err := num(req, "requires.", "definition_schema", &c.Requires.DefinitionSchema); err != nil {
			return nil, err
		}
	}
	if c.Name != dir.Name {
		return nil, r("N5", "name", c.Name, dir.Name)
	}
	if c.Version != dir.Version {
		return nil, r("N5", "version", c.Version, dir.Version)
	}
	if kind == "recipes" {
		return c, nil
	}
	entries, ok := tree["definitions"].([]map[string]any)
	if !ok || len(entries) == 0 {
		if _, isArr := tree["definitions"].([]map[string]any); !isArr {
			return nil, r("N23", "definitions", "an array of tables")
		}
		return nil, r("N19", file, "definitions")
	}
	for i, e := range entries {
		label := func(k string) string { return fmt.Sprintf("definitions[%d].%s", i, k) }
		for _, k := range []string{"id", "version", "status", "sha256", "slice", "border"} {
			if _, ok := e[k]; !ok {
				return nil, r("N19", file, label(k))
			}
		}
		var d Definition
		fields := map[string]*string{
			"id": &d.ID, "version": &d.Version, "identity": &d.Identity, "status": &d.Status,
			"identity_status": &d.IdentityStatus, "sha256": &d.SHA256, "slice": &d.Slice, "border": &d.Border,
		}
		for _, k := range definitionKey {
			if err := str(e, label(""), k, fields[k]); err != nil {
				return nil, err
			}
		}
		bad := func(field string) error { return r("N15", file, d.ID, field) }
		_, identName, pair, idOK := idParts(d.ID)
		_, hasIdentity := e["identity"]
		_, hasIdentStatus := e["identity_status"]
		switch {
		case !idOK:
			return nil, bad("id")
		case !versionRE.MatchString(d.Version):
			return nil, bad("version")
		case hasIdentity && !identityRE.MatchString(d.Identity):
			return nil, bad("identity")
		case !validStatus(d.Status):
			return nil, bad("status")
		case hasIdentStatus && !validStatus(d.IdentityStatus):
			return nil, bad("identity_status")
		case !hexRE.MatchString(d.SHA256):
			return nil, bad("sha256")
		case !sha256ColE.MatchString(d.Slice):
			return nil, bad("slice")
		case !sha256ColE.MatchString(d.Border):
			return nil, bad("border")
		}
		name, _, _ := strings.Cut(d.Identity, "@")
		if pair != hasIdentity || (pair && name != identName) {
			return nil, r("12", file, d.ID, d.Identity)
		}
		if hasIdentStatus && !pair {
			return nil, bad("identity_status")
		}
		c.Definitions = append(c.Definitions, d)
	}
	return c, nil
}

func validStatus(s string) bool { return s == "ratified" || s == "proposal" || s == "retired" }
