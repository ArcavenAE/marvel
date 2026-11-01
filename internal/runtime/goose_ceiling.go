package runtime

import (
	"fmt"
	"strings"
)

// gooseCeiling is one measured context ceiling of a model as served. Each entry
// names the run that measured it and the serving setting it was measured under:
// a served ceiling belongs to that setting, so a seat served with a larger
// context needs its own run before its entry changes. A new measurement is a
// code change (goose-adapter-v1.1.md section 6b).
type gooseCeiling struct {
	Tokens     int
	MeasuredBy string
}

// gooseCeilings maps a goose model name to its measured ceiling. It stays inside
// this adapter and changes no other.
var gooseCeilings = map[string]gooseCeiling{
	"qwen3:0.6b": {Tokens: 4096, MeasuredBy: "goose arm 2 (L7), at an Ollama serving context of 4096"},
}

// gooseContextLimit resolves the GOOSE_CONTEXT_LIMIT for a launch and an
// optional NOTE for the operator. declared is runtime.context_window (0 when
// unset); model is the role's GOOSE_MODEL ("" when unset). It returns 0 for no
// projection.
//
//   - both set: the lower of the two, with a NOTE when the role declares more
//     than the table entry (a note, never a refusal);
//   - only the table entry set (a role with no context_window on a model with an
//     entry): the entry, which is the case the table was seeded to protect;
//   - only declared set, or neither: declared, or nothing.
func gooseContextLimit(declared int, model string) (limit int, note string) {
	c, ok := gooseCeilings[strings.TrimSpace(model)]
	switch {
	case !ok:
		return declared, ""
	case declared <= 0:
		return c.Tokens, ""
	case declared > c.Tokens:
		return c.Tokens, fmt.Sprintf("goose model %s: context_window %d is above its measured ceiling %d (%s); projecting %d",
			model, declared, c.Tokens, c.MeasuredBy, c.Tokens)
	default:
		return declared, ""
	}
}
