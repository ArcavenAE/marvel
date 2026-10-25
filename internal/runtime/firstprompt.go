package runtime

// composeFirstPrompt returns the first message an interactive seat is launched
// with, and which parts it holds (docs/design/interactive-first-prompt.md D1 to
// D3). The text is built only from values marvel holds.
func composeFirstPrompt(ctx *LaunchContext) (text string, successor, role bool) {
	return ctx.Session.Runtime.Prompt, false, ctx.Session.Runtime.Prompt != ""
}
