package runtime

import (
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// composeFirstPrompt returns the first message an interactive seat is launched
// with, and which parts it holds: the fixed successor message, the role's own
// runtime.prompt, or both (docs/design/interactive-first-prompt.md D1 to D3).
//
// The successor message is written only from values marvel holds: the
// predecessor's session key, the handoff path the role's manifest declared with
// the predecessor's name filled in, and two fixed sentences. It never carries
// text from a file, a board or a message another seat can write. It comes
// before the role's prompt. A first spawn has no predecessor, and a role that
// declares successor_prompt = false opts out, so either gets the role prompt
// alone.
func composeFirstPrompt(ctx *LaunchContext) (text string, successor, role bool) {
	rolePrompt := ctx.Session.Runtime.Prompt
	role = rolePrompt != ""
	predecessor := ctx.Session.Predecessor
	if predecessor == "" || !ctx.Session.Runtime.SuccessorPromptOn() {
		return rolePrompt, false, role
	}

	parts := []string{"You are a successor to " + predecessor + "."}
	parts = append(parts, handoffLine(ctx, predecessor))
	// marvel stays independent of director: the inbox line appears only when
	// marvel itself attached the seat to a bus.
	if ctx.BusURL != "" {
		parts = append(parts, "Then drain your inbox.")
	}
	if role {
		parts = append(parts, rolePrompt)
	}
	return strings.Join(parts, " "), true, role
}

// handoffLine names the predecessor's handoff. A role that declares a handoff
// path gets it, resolved for the predecessor. A name that is not one path
// element cannot steer the path, so it, like a role with no path, gets the
// generic line.
func handoffLine(ctx *LaunchContext, predecessor string) string {
	const generic = "Read its handoff first, if your process names one."
	if ctx.Role == nil || ctx.Role.Shift == nil || ctx.Role.Shift.Handoff == "" {
		return generic
	}
	name := predecessor
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	path, err := api.ExpandHandoffPath(ctx.Role.Shift.Handoff, name)
	if err != nil {
		return generic
	}
	return "Read its handoff at " + path + " first."
}

// firstPromptSource names where a composed message came from (design D7).
func firstPromptSource(successor, role bool) string {
	switch {
	case successor && role:
		return FirstPromptSuccessorRole
	case successor:
		return FirstPromptSuccessor
	case role:
		return FirstPromptRole
	}
	return FirstPromptNone
}
