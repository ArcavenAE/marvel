// Package usage is marvel's context-window and token accountant: the
// single producer of the CTX% column for sessions whose harness marvel
// can observe.
//
// # Occupancy is a level, not a sum
//
// Occupancy is the token count the model must re-read on the next API
// request: the whole prompt, including everything served from the prompt
// cache. It is a LEVEL, taken per API request, latest-wins. It is never a
// sum over requests.
//
// This is the correction the accountant exists to encode. Claude Code's
// terminal `result` line reports session-CUMULATIVE totals, not the final
// request's level. Measured on
// internal/runtime/claudecode/testdata/tool_call.ndjson: the three
// per-request occupancies are 33377, 33481, and 34136, while the result
// line's classes sum to 100994. A numerator taken from the result line
// therefore reads 10.1% against a 1M window where the truth is 3.4%, and
// the error grows linearly with request count, so the longest sessions
// are the most wrong. Sample.Terminal marks such a sample and the fold
// excludes it structurally; Stats.CumulationViolations counts any
// accumulation that exceeds a terminal total, which is the arithmetic
// signature of a cumulative series read as levels.
//
// Per harness, occupancy is composed differently, and the composition
// rides the event payload as events.Layout rather than living in a
// lookup table here. Call events.RequestUsage.Occupancy(); never sum the
// classes by hand.
//
// Two kinds of request are billed but kept out of the level, because they
// do not fill the session's window: a request to a model other than the
// session's primary (a routing side-call), and a subagent request made
// inside a tool call, which fills a window of its own. Either one folded
// in would collapse the level and register as a compaction on the way in
// and out. Stats counts both.
//
// # The denominator is reported absent, never defaulted
//
// LimitSource grades every reading. When no rung of the resolution
// ladder produces a window, Occupancy.Limit is 0 and Occupancy.Percent
// is meaningless. Rendering a plausible percentage against a guessed
// window is worse than rendering absence: a wrong denominator misreports
// silently, and an admission gate that reads unresolved as 0% admits
// everything. There is deliberately no fleet-wide default-window knob.
//
// The ladder, most authoritative first, is limitLadder in limits.go:
// stream, learned, manifest, feed, table, table-alias, then unresolved.
// LimitSource.Rank is the comparison; do not re-derive precedence by
// comparing the string values.
//
// One rung pair on that ladder will look wrong at first reading. A window
// the harness declares in its own STREAM sits at rung 1, above the
// operator's runtime.context_window. The same window declared by the same
// harness on the statusline FEED sits at rung 4, below it. Transport, not
// content, is what differs, and it is the difference that matters: the
// stream is the channel the harness is enforcing compaction against, so
// overruling it would make marvel's denominator disagree with the one
// governing the session, whereas the feed is a side channel read
// opportunistically off a human-facing status hook, with no version
// handle and no statement of which of the six effective-window axes
// (finding-016) it reflects. An operator who wrote a window into the
// manifest outranks a channel that only describes the session. Ruled
// 2026-08-08; the full reasoning is at limitLadder.
//
// # Raw occupancy, not the harness's displayed figure
//
// The reported percentage is raw occupancy against the model's context
// window. What Claude Code DISPLAYS is a different quantity: percent
// until auto-compaction, normalized to an operator-tunable threshold
// plus a reserved buffer of roughly 33-45k tokens. Neither the threshold
// override nor the effective-window override appears in the stream, so
// marvel cannot reproduce the displayed number and does not try. An
// operator can legitimately see "10% until auto-compact" inside the
// harness while marvel reports about 64%. Both are correct about
// different things.
//
// # Scope
//
// Headless only. All three stream-capable adapters gate stream support
// on api.RuntimeModeHeadless, so an interactive session produces no
// adapter events and its CTX% stays absent. That gap is tracked
// separately (question-interactive-context-pressure); native harness
// OTEL is the first plausible path to it.
//
// Crush and Gemini CLI are out of scope, and for Crush the earlier
// reason recorded here was measured false. Crush v0.88.1 publishes a
// structured SSE stream carrying a per-request occupancy level, a REST
// route carrying the window, and two documented JSON CLI surfaces
// besides (finding-019). What it lacks is a marvel runtime adapter to
// declare the channel and construct the environment, and a transport:
// the feed is HTTP and SSE over a unix socket rather than the harness
// stdout internal/runtime is built around. A profile here with no
// producer would be a claim about a design that does not exist. Gemini
// was not available to measure.
//
// # Feed-agnostic by construction
//
// The arithmetic lives in Sample and in Accountant.fold. A feed's only
// job is to build Sample values. sampleFromEvent is the stream feed's
// normalization boundary; an OTEL feed adds a sibling constructor and
// one caller, and changes nothing in the fold. Field names differ across
// feeds for identical quantities (the stream says
// cache_read_input_tokens where Claude's OTEL says cache_read_tokens),
// which is precisely why the boundary is one function.
//
// # Measurement provenance
//
// Every number in this package's tables and comments was measured on
// macOS arm64 against Claude Code 2.1.220, codex-cli 0.146.0, and
// OpenCode 1.18.5. Per marvel B13, stream behavior can differ by
// platform, and a harness version bump that changes a usage schema
// changes the read. The fixtures are captured output, so such a change
// surfaces as a parse error, a layout mismatch, or an absent column
// rather than as a silently wrong number.
package usage
