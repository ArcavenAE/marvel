package company

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

// DecodeCompany decodes the bytes of the company.toml found in the version
// directory named by dir. STUB: accepts everything.
func DecodeCompany(dir Ident, data []byte) (*Company, error) { return &Company{}, nil }
