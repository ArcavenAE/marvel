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
		// UNVERIFIED against a real multi-turn capture: codex exec
		// one-shot has a single turn, so whether input_tokens is a level
		// or a session total across turns rests on the field layout plus
		// the synthetic resume fixture. `codex exec resume` settles it.
		cumulation: CumulationRequest,
		// thread.started carries only thread_id on 0.146.0; the parser
		// reads a model field that arrives empty, so the model comes from
		// the launch args or nowhere.
		modelFromStream: false,
		limitInStream:   false,
		vendorTotal:     false,
	},
	opencode.Harness: {
		// UNVERIFIED for the same reason as codex.
		cumulation:      CumulationRequest,
		modelFromStream: false,
		limitInStream:   false,
		// step_finish publishes tokens.total, over in + out + reasoning
		// only (measured). It checks the harness's own arithmetic, not the
		// unverified cache layout: see Sample.TotalMismatch and
		// Sample.AdditiveConfirmed.
		vendorTotal: true,
	},
}

func profileFor(harness string) (profile, bool) {
	p, ok := profiles[harness]
	return p, ok
}
