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
// safe characters and quoted when it is not; a slot named form is fixed text.
type row struct {
	id  string
	doc string
}

// rows is the registry of refusals this package can raise. The doc text is
// the spec's refusal table text, so one place says what each refusal reads.
var rows = []row{
	{"1", `company <name>@<version>: <file>: unknown key "<key>"`},
	{"12", `company <name>@<version>: <file>: definition <id>: identity <identity> does not match the id`},
	{"15", `recipe <name>: <key>: unknown placeholder "<text>"`},
	{"16", `recipe <name>: <key>: parameter "<name>" is not declared`},
	{"17", `recipe <name>: blocks <block> and <block> both name <KEY>`},
	{"18", `recipe <name>: adapter "<adapter>" is not registered`},
	{"19", `recipe <name>: schema = <n>, but the library's recipe_schema is <m>`},
	{"20", `recipe <name>: preflight timeout <timeout> is over the ceiling <ceiling>`},
	{"N1", `recipe <name>: <block>: key <KEY> is a credential and may not be named in a recipe`},
	{"N2", `recipe <name>: <block>: key <KEY> is set by marvel and may not be named in a recipe`},
	{"N3", `recipe <name>: params.global: parameter name "global" is reserved`},
	{"N4", `recipe <name>: <key>: tool: is valid only as a whole command or the first element of a command list`},
	{"N5", `company <name>@<version>: company.toml: <which> "<value>" does not equal its directory "<dir>"`},
	{"N15", `company <name>@<version>: <file>: definition <id>: <field> is not well formed`},
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
			case slot == "<form>":
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
