package usage

import (
	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

// profile carries the per-harness facts the event payload cannot. Layout
// deliberately is NOT here: it rides events.RequestUsage so the parser
// that knows its own harness declares it, and a consumer-side table
// cannot drift out of sync with the fields beside it.
type profile struct {
	// cumulation says whether per-request samples are levels or running
	// totals.
	cumulation Cumulation
	// modelFromStream is true when the harness names its model in-stream.
	modelFromStream bool
	// limitInStream is true when the harness declares its own window.
	limitInStream bool
	// limitArrivesAtEnd is true when that declaration rides the terminal
	// line, so it cannot serve a live reading and the table (or the
	// learned cache) is the live path even for this harness.
	limitArrivesAtEnd bool
	// vendorTotal is true when the harness publishes its own token
	// total, which is what makes Sample.TotalMismatch meaningful.
	vendorTotal bool
}

// profiles is keyed on the Harness constant each parser package stamps
// onto its events. Imported rather than duplicated as string literals so
// a rename in a parser breaks the build here instead of silently
// producing an unknown harness.
//
// An unknown harness is ignored, not guessed: a blank CTX% column is a
// visible limitation, a number derived from an assumed layout is not.
var profiles = map[string]profile{
	claudecode.Harness: {
		cumulation:      CumulationRequest,
		modelFromStream: true,
		// contextWindow rides modelUsage on the terminal result line, so
		// for Claude the in-stream declaration reconciles and teaches;
		// it cannot serve the live reading.
		limitInStream:     true,
		limitArrivesAtEnd: true,
		vendorTotal:       false,
	},
	codex.Harness: {
		// MEASURED, and it corrects the earlier reading. turn.completed
		// carries a RUNNING TOTAL, not a per-request level, and the
		// single-turn fixture that looked inconclusive already showed it.
		//
		// tool_call.jsonl reports input_tokens 28110 / cached 24064 /
		// output 76 for thread 019fba87-d036-7ae1-a20e-7187ef8e3329.
		// Codex's own per-request record for that same thread (its
		// rollout file, event_msg/token_count) holds two requests:
		// last_token_usage input 14005 then 14105, and total_token_usage
		// 14005 then 28110 with cached 11008 then 24064. The exec stream
		// matches total_token_usage field for field. The prompt at turn
		// end was 14105; marvel read 28110.
		//
		// One turn was enough because that turn made two model requests
		// (a tool call and an answer). The earlier note looked for the
		// accumulation ACROSS turns and so did not think to check within
		// one, which is why a fixture already in the repo went unread.
		//
		// Not settled: whether the accumulator resets at a turn boundary
		// or runs for the session. Both remain consistent with a
		// single-turn capture, and the fold treats them alike, since
		// neither is a level. A multi-turn authenticated `codex exec
		// resume` decides it.
		cumulation: CumulationSession,
		// thread.started carries only thread_id on 0.146.0 (re-verified
		// live against codex-cli 0.146.0); the parser reads a model field
		// that arrives empty, so the model comes from the launch args or
		// nowhere. Codex names the model elsewhere (turn_context.model in
		// the rollout, and `model` on 10 of its 11 hook payloads), just
		// not in the exec stream.
		modelFromStream: false,
		// The exec stream declares no window. The rollout does, twice
		// over: event_msg/task_started.model_context_window at the start
		// of every turn and event_msg/token_count.info.model_context_window
		// per request. Neither is this feed.
		limitInStream: false,
		vendorTotal:   false,
	},
	opencode.Harness: {
		// Measured against a live two-turn session: input fell from 6018
		// to 27 across turns as the prompt moved into cache, which a
		// running session total cannot do. See the opencode fixture pair
		// caching_first_turn.jsonl and caching.jsonl.
		cumulation:      CumulationRequest,
		modelFromStream: false,
		limitInStream:   false,
		// step_finish publishes tokens.total, over in + out + reasoning +
		// cache.read + cache.write (measured on 215 step_finish rows). It
		// checks the harness's own arithmetic, not the layout: see
		// Sample.TotalMismatch and Sample.AdditiveConfirmed.
		vendorTotal: true,
	},
}

func profileFor(harness string) (profile, bool) {
	p, ok := profiles[harness]
	return p, ok
}
