package company

import (
	"regexp"
	"strings"
)

// Refusal is one refused read: the row of the spec's refusal table it comes
// from and the message that row fixes, with its fields filled in.
type Refusal struct {
	Row     string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// row is one entry of the registry: its id and the spec table's text for it,
// with <slot> marking each field. A slot written "<x>" inside double quotes is
// always printed quoted; any other slot prints bare when it is made only of
// safe characters and quoted when it is not. The slots form, roots and list
// are text the caller built, and value and path are hints printed as written.
type row struct {
	id  string
	doc string
}

// rows is the registry of refusals this package can raise. The doc text is
// the spec's refusal table text, so one place says what each refusal reads.
var rows = []row{
	{"1", `company <name>@<version>: <file>: unknown key "<key>"`},
	{"2", `team <team>: <key> pin <name>@<version> is not installed in any root; marvel does not fall back to current (roots read: <roots>)`},
	{"3", `company <name>@<version>: <definition_schema or recipe_schema> = <n> is not read by this marvel (reads <list>)`},
	{"4", `recipes <name>@<version> requires definition_schema = <n>, but company <name>@<version> has definition_schema = <m>`},
	{"5", `company <name>@<version> requires marvel <v> or newer; this is <w>`},
	{"6", `team <team> role <role>: parameter <name> does not match its pattern`},
	{"N27", `team <team> role <role>: required parameter <name> is not set`},
	{"7", `recipe <name>: tool "<tool>" is not set; run marvel config tools set <tool> <path>`},
	{"8", `recipe <name>: prompt lists identity, but definition <id> already carries identity <identity>`},
	{"9", `team <team> role <role>: recipe <name> and runtime.env both name <KEY>`},
	{"10", `team <team> role <role>: definition <id> is retired`},
	{"11", `team <team> role <role>: definition <id> has identity <identity> marked retired`},
	{"12", `company <name>@<version>: <file>: definition <id>: identity <identity> does not match the id`},
	{"13", `company <name> is in two roots: <root> and <root>`},
	{"14", `team <team>: <key> pin <name>@<version> is a library of kind <kind>, but <key> needs kind <kind>`},
	{"15", `recipe <name>: <key>: unknown placeholder "<text>"`},
	{"16", `recipe <name>: <key>: parameter "<name>" is not declared`},
	{"17", `recipe <name>: blocks <block> and <block> both name <KEY>`},
	{"18", `recipe <name>: adapter "<adapter>" is not registered`},
	{"19", `recipe <name>: schema = <n>, but the library's recipe_schema is <m>`},
	{"21", `team <team> role <role>: definition <id> is a proposal (<status or identity_status>) and the role does not set allow_proposal`},
	{"20", `recipe <name>: preflight timeout <timeout> is over the ceiling <ceiling>`},
	{"N1", `recipe <name>: <block>: key <KEY> is a credential and may not be named in a recipe`},
	{"N2", `recipe <name>: <block>: key <KEY> is set by marvel and may not be named in a recipe`},
	{"N3", `recipe <name>: params.global: parameter name "global" is reserved`},
	{"N4", `recipe <name>: <key>: tool: is valid only as a whole command or the first element of a command list`},
	{"N5", `company <name>@<version>: company.toml: <which> "<value>" does not equal its directory "<dir>"`},
	{"N6", `recipe <name>: prompt lists template:<template>, but company <name>@<version> has no template <template>`},
	{"N7", `recipe <name>: <key>: var "<var>" is not set; run marvel config vars set <var> <value>`},
	{"N8", `team <team>: <key> pin <name> is not installed in any root; marvel does not fall back (roots read: <roots>)`},
	{"N9a", `team <team>: <key> pin <name> follows current <version>, which is not installed in any root; marvel does not fall back`},
	{"N9b", `team <team>: <key> pin <name> has no readable current in <root>`},
	{"N10", `company <name> in <root>: current points outside <name>`},
	{"N15", `company <name>@<version>: <file>: definition <id>: <field> is not well formed`},
	{"N16", `company <name>@<version>: <file>: sha256 does not match the index entry <id>`},
	{"N19", `company <name>@<version>: <file>: required key "<key>" is missing`},
	{"N21", `company <name>@<version>: recipe <recipe>: <section> key <KEY> is not a plain name`},
	{"N23", `company <name>@<version>: company.toml: <key> must be <form>`},
	{"N24", `recipe <name>: <key>: must be <form>`},
	{"N25", `company <name>@<version>: company.toml does not parse (line <n>)`},
	{"N26", `recipe <name>: recipes/<name>.toml does not parse (line <n>)`},
}

// fieldSlotRE finds one slot of a row's doc text, with the double quotes around it
// when it has them.
var fieldSlotRE = regexp.MustCompile(`"<[^<>]+>"|<[^<>]+>`)

// refuse builds the refusal for a row from its fields, in the order the row's
// slots appear. A slot written "<x>" prints through quoteField always quoted,
// any other slot prints bare when it is made of safe characters and quoted
// when it is not, and the slot <form> is fixed text the caller supplies.
func refuse(id string, fields ...string) *Refusal {
	for _, r := range rows {
		if r.id != id {
			continue
		}
		next := 0
		msg := fieldSlotRE.ReplaceAllStringFunc(r.doc, func(slot string) string {
			var f string
			if next < len(fields) {
				f = fields[next]
			}
			next++
			switch {
			case slot == "<value>" || slot == "<path>":
				// A placeholder hint the message prints as written; it takes no field.
				next--
				return slot
			case slot == "<form>" || slot == "<roots>" || slot == "<list>":
				if hasControl(f) {
					return quoteField(f, true)
				}
				return f
			case strings.HasPrefix(slot, `"`):
				return quoteField(f, true)
			}
			return quoteField(f, false)
		})
		return &Refusal{Row: id, Message: msg}
	}
	return &Refusal{Row: id, Message: "company: internal error: no refusal row " + quoteField(id, true)}
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
