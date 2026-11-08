package api

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Goose postures, the values of runtime.goose.posture. They are goose's own
// GOOSE_MODE names for the three modes marvel allows a seat (docs/design/
// goose-adapter-v1.md section 4, v1.1 C1). An absent posture is not a value:
// the adapter refuses to launch a seat that declares none.
const (
	GoosePostureApprove      = "approve"
	GoosePostureSmartApprove = "smart_approve"
	GoosePostureAuto         = "auto"
)

// GooseRuntime is the runtime.goose table of a role on the goose adapter. It is
// read only by that adapter, and only these four keys exist; an unknown key
// inside the table is refused at apply, while the manifest as a whole stays
// lenient about keys it does not know.
type GooseRuntime struct {
	// Posture is one of the three Goose* constants, or empty for none declared.
	Posture string `toml:"posture,omitempty" yaml:"posture,omitempty"`
	// ToolsAllow are the tools the seat may run without asking; they go to
	// permission.yaml's always_allow.
	ToolsAllow []string `toml:"tools_allow,omitempty" yaml:"tools_allow,omitempty"`
	// ToolsDeny are the tools the seat may never run; they go to never_allow,
	// beside the manage_extensions entry the adapter always adds.
	ToolsDeny []string `toml:"tools_deny,omitempty" yaml:"tools_deny,omitempty"`
	// Extensions are the extensions the seat loads; they go to config.yaml and
	// to --with-builtin.
	Extensions []string `toml:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// gooseKeys are the keys a runtime.goose table may carry.
var gooseKeys = []string{"posture", "tools_allow", "tools_deny", "extensions"}

// unknownGooseKeyError names a key the goose table does not take, without
// echoing a value.
func unknownGooseKeyError(key string) error {
	return fmt.Errorf("runtime.goose: unknown key %q (valid: %s)", key, strings.Join(gooseKeys, ", "))
}

// UnmarshalYAML refuses a key outside the four the table takes. Unlike the TOML
// path, which reads the decoder's undecoded keys, the table decodes itself, so
// the check sits with the type.
func (g *GooseRuntime) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if key := n.Content[i].Value; !slices.Contains(gooseKeys, key) {
				return unknownGooseKeyError(key)
			}
		}
	}
	type plain GooseRuntime
	return n.Decode((*plain)(g))
}

// Validate refuses a posture outside the three, and a list entry that is blank.
// An absent posture is valid here: the adapter refuses the launch, so apply
// does not (a new adapter, no running flow to break).
func (g *GooseRuntime) Validate() error {
	if g == nil {
		return nil
	}
	switch g.Posture {
	case "", GoosePostureApprove, GoosePostureSmartApprove, GoosePostureAuto:
	default:
		return fmt.Errorf("runtime.goose.posture %q is not valid (valid: %s, %s, %s)",
			g.Posture, GoosePostureApprove, GoosePostureSmartApprove, GoosePostureAuto)
	}
	for _, list := range []struct {
		name  string
		items []string
	}{{"tools_allow", g.ToolsAllow}, {"tools_deny", g.ToolsDeny}, {"extensions", g.Extensions}} {
		for _, it := range list.items {
			if strings.TrimSpace(it) == "" {
				return fmt.Errorf("runtime.goose.%s has an empty entry", list.name)
			}
		}
	}
	return nil
}

// clone returns a copy that shares no slice with g.
func (g *GooseRuntime) clone() *GooseRuntime {
	if g == nil {
		return nil
	}
	out := *g
	out.ToolsAllow = slices.Clone(g.ToolsAllow)
	out.ToolsDeny = slices.Clone(g.ToolsDeny)
	out.Extensions = slices.Clone(g.Extensions)
	return &out
}

// equal reports whether two tables declare the same thing. A nil table and an
// empty one are equal, and the lists compare in order.
func (g *GooseRuntime) equal(o *GooseRuntime) bool {
	var a, b GooseRuntime
	if g != nil {
		a = *g
	}
	if o != nil {
		b = *o
	}
	return a.Posture == b.Posture &&
		slices.Equal(a.ToolsAllow, b.ToolsAllow) &&
		slices.Equal(a.ToolsDeny, b.ToolsDeny) &&
		slices.Equal(a.Extensions, b.Extensions)
}

// validateGooseHeadlessPrompt refuses a headless role on the goose adapter that
// has no prompt. Launch refuses the same role with the no-prompt stub; this is
// the earlier refusal, so the operator hears it at apply and not from a seat that
// never starts (operator ruling on goose empty prompts). It is scoped to goose:
// the check for headless roles on other adapters is its own item. The adapter is
// named the way GooseAdvisories names it, so a goose role whose command is a path
// reads as not goose here.
func (r ManifestRuntime) validateGooseHeadlessPrompt() error {
	if r.AdapterName() != "goose" || r.Mode != RuntimeModeHeadless || r.Prompt != "" {
		return nil
	}
	return fmt.Errorf("runtime.prompt is required for a headless goose role")
}

// GooseAdvisories lists every role that declares runtime.goose on a runtime
// that is not goose. It is a note and never a refusal: an unhonoured
// context_feed is advisory the same way, the adapter name matches the image and
// then the whole command exactly (so a goose role whose command is a path reads
// as not goose here), and a refusal would change apply for other adapters.
func (m *Manifest) GooseAdvisories() []string {
	var out []string
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			if r.Runtime.Goose == nil {
				continue
			}
			if name := r.Runtime.AdapterName(); name != "goose" {
				out = append(out, fmt.Sprintf("team %s role %s: runtime %q is not goose; runtime.goose is ignored", t.Name, r.Name, name))
			}
		}
	}
	return out
}
