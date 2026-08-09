package usage

import (
	"log"
	"strings"
	"sync"
)

// LimitSource records which rung of the resolution ladder produced a
// session's context window. Graded rather than boolean so a consumer can
// refuse on low confidence without refusing on measurement.
type LimitSource string

const (
	// LimitUnresolved means no rung produced a window. The reading's
	// Percent is meaningless and must be rendered as absence.
	LimitUnresolved LimitSource = "unresolved"
	// LimitFromStream means the harness declared the window in this
	// session's own stream. Authoritative.
	LimitFromStream LimitSource = "stream"
	// LimitLearned means a prior session on this daemon declared it.
	LimitLearned LimitSource = "learned"
	// LimitFromManifest means an operator set runtime.context_window.
	LimitFromManifest LimitSource = "manifest"
	// LimitFromFeed means the harness declared the window on a cooperative
	// side channel rather than in its own stream: today the statusline
	// feed's context_window.context_window_size. Ranked BELOW the manifest
	// on purpose; see the asymmetry note below.
	LimitFromFeed LimitSource = "feed"
	// LimitFromTable means an exact hit in the shipped model table.
	LimitFromTable LimitSource = "table"
	// LimitFromTableAlias means a short alias resolved to a table entry.
	// Graded separately because an alias means whatever the harness
	// points it at today: a guess that happens to be right is not a
	// keyed fact.
	LimitFromTableAlias LimitSource = "table-alias"
)

// limitLadder is the total ordering of the resolution ladder, most
// authoritative first. It is the single source of truth for precedence:
// Rank reads it, Resolve's branch order must agree with it, and
// TestLimitLadderIsTotal fails if a LimitSource constant is declared
// without a place on it.
//
// # The stream/feed asymmetry, and why it is deliberate
//
// The same fact (a window the harness itself declares) sits at rung 1
// when it arrives in the harness's own stream and at rung 4, below the
// operator's manifest override, when it arrives on the statusline feed.
// A reader hitting this for the first time will read it as a bug. It is
// the operator ruling of 2026-08-08
// (question-interactive-context-pressure, RESOLUTION LADDER), and the
// reasoning is that transport carries information the number does not:
//
//   - A stream declaration is the harness stating the window it is
//     currently enforcing compaction against, in the same channel as the
//     token counts it is stating it about. Overruling it with a manifest
//     value would make marvel's denominator disagree with the one that
//     actually governs the session's behavior. The override's job is to
//     fill absence, not to overrule measurement.
//   - A feed declaration is a side channel: a cooperative hook the
//     harness invokes for a human-facing status string, whose payload
//     marvel reads opportunistically. It is one harness release away from
//     changing meaning with no version handle on it, and the effective
//     auto-compact window it reports varies on six axes that the operator
//     may know about and the payload does not name (finding-016). So an
//     operator who has written runtime.context_window has stated
//     something the side channel cannot contradict on its own authority.
//
// Put shortly: rung 1 is for the channel that governs the session, and
// the manifest outranks every channel that merely describes it.
var limitLadder = []LimitSource{
	LimitFromStream,
	LimitLearned,
	LimitFromManifest,
	LimitFromFeed,
	LimitFromTable,
	LimitFromTableAlias,
	LimitUnresolved,
}

// Rank returns the source's position on the resolution ladder, 1 being
// the most authoritative and LimitUnresolved the least. An undeclared
// value ranks after every declared one, so a source from a newer writer
// is treated as least authoritative rather than most.
//
// Exported because precedence is a fact consumers need: an admission gate
// that refuses on low confidence has to compare rungs, and comparing the
// string values would encode this ordering a second time.
func (s LimitSource) Rank() int {
	for i, l := range limitLadder {
		if l == s {
			return i + 1
		}
	}
	return len(limitLadder) + 1
}

// Table maps a normalized model id to its context window in tokens.
type Table map[string]int

// DefaultTable is the shipped fallback rung of the ladder.
//
// It lives in Go rather than a config file for the reason
// api.canonicalPermissionModes gives for the same shape: this is a third
// party's fact that silently breaks a session when wrong, so it belongs
// where it is diff-reviewable and versioned with the binary. The
// counter-pressure (model windows move on the vendor's cadence, not
// marvel's) is answered by the rung above it: an operator sets
// runtime.context_window without waiting for a marvel release. The
// learned rung helps only where a harness declares its own window, which
// today is Claude alone (see Learn).
func DefaultTable() Table {
	return Table{
		// The first two are fixture-verified in this repo (the
		// modelUsage.contextWindow values in
		// internal/runtime/claudecode/testdata/*.ndjson). The rest were
		// verified 2026-07-03 against the vendor model-config docs and
		// platform pricing (orc finding-057, node
		// elem-autocompact-window-posture).
		"claude-haiku-4-5":      200_000,   // fixture-verified, max output 32000
		"claude-fable-5[1m]":    1_000_000, // fixture-verified, max output 64000
		"claude-sonnet-4-6":     200_000,
		"claude-sonnet-4-6[1m]": 1_000_000,
		"claude-sonnet-5":       1_000_000, // no 200k variant on the API
		"claude-opus-4-7":       200_000,
		"claude-opus-4-7[1m]":   1_000_000,
		"claude-opus-4-8":       200_000,
		"claude-opus-4-8[1m]":   1_000_000,

		// opus-5 has no 200k variant: 1M is both the default and the
		// maximum, so the two spellings below carry the same number and
		// neither can be the wrong denominator for the other. The
		// suffixed key still earns its line, because Claude Code stamps
		// [1m] on the init model and the modelUsage key even for a model
		// whose only window is 1M (claude-fable-5[1m] above is the
		// fixture-verified instance of that), and NormalizeModel keeps
		// the suffix, so a bare key alone would miss. Verified 2026-08-08
		// against the vendor model catalog.
		"claude-opus-5":     1_000_000,
		"claude-opus-5[1m]": 1_000_000,

		// codex: DELIBERATELY EMPTY, and the earlier reason was the wrong
		// one. The missing piece was never the model name. Codex names
		// its model in the rollout (turn_context.model) and on 10 of its
		// 11 hook payloads; only the exec stream withholds it. Naming the
		// model would not have keyed anything.
		//
		// What is missing is evidence that the window VARIES by model.
		// Measured over 209 rollout files, 2097 per-request records and
		// 369 turn starts: every declaration is 258400, for all three
		// models that appear (gpt-5.6-sol, gpt-5.6-terra, gpt-5.6-luna).
		// The catalog agrees, listing context_window 272000 and
		// effective_context_window_percent 95 for all eight models, and
		// 272000 x 0.95 is exactly 258400. So "the window is keyed by
		// model" and "the window is one number for this account" predict
		// identical data here, and the corpus cannot separate them. A
		// table entry would be a per-account plan limit wearing a model
		// name.
		//
		// One model already refuses the key: gpt-5.4 carries
		// max_context_window 1000000 against context_window 272000.
		//
		// The rung this belongs on is not the table. Codex declares the
		// window beside the level in its own record, so the resolution
		// should come from that feed, not from a shipped constant.
		// Whoever wires it: read model_context_window from the same
		// record as the level, and do NOT multiply by
		// effective_context_window_percent. The declared number is
		// already the effective one; multiplying again lands on 245480,
		// runs 5% pessimistic, and fires shifts early.
		//
		// Until that feed exists, codex CTX% renders "-" (its exec-stream
		// samples are cumulative and produce no level at all; see
		// profiles.go) and an operator's runtime.context_window is the
		// only denominator.

		// opencode: EMPTY, and equally not self-filling. No window
		// measurement exists for any opencode model, and opencode declares
		// none in its stream either. OpenCode carries a model database out
		// of band, which is worth one look before this section ships
		// non-empty.
	}
}

// Lookup returns the window for an already-normalized model id.
func (t Table) Lookup(model string) (int, bool) {
	w, ok := t[model]
	return w, ok
}

// aliases map the short forms an operator passes to --model onto table
// keys. A hit here resolves as LimitFromTableAlias, never LimitFromTable.
var aliases = map[string]string{
	"haiku":     "claude-haiku-4-5",
	"sonnet":    "claude-sonnet-4-6",
	"opus":      "claude-opus-4-8",
	"fable[1m]": "claude-fable-5[1m]",
}

// NormalizeModel canonicalizes a vendor model id for WINDOW lookup. Each
// rule is required by an observed id form:
//
//   - KEEP a [1m] suffix. It changes the window 5x (200k against 1M), so
//     dropping it collapses two different models onto one key. See orc
//     finding-055 and finding-057.
//   - STRIP a Bedrock region prefix (us., eu., apac.) and a leading
//     "anthropic.". Pricing differs by region; the window does not.
//   - TOLERATE a dated suffix. This repo's own fixture carries the
//     modelUsage key claude-haiku-4-5-20251001, which must resolve as
//     claude-haiku-4-5.
func NormalizeModel(model string) string {
	m := strings.TrimSpace(model)
	for _, p := range []string{"us.", "eu.", "apac."} {
		m = strings.TrimPrefix(m, p)
	}
	m = strings.TrimPrefix(m, "anthropic.")
	return trimDateSuffix(m)
}

// trimDateSuffix drops a trailing -YYYYMMDD segment. The [1m] forms are
// unaffected: their final segment is "5[1m]", not eight digits.
func trimDateSuffix(m string) string {
	i := strings.LastIndex(m, "-")
	if i < 0 || len(m)-i-1 != 8 {
		return m
	}
	for _, r := range m[i+1:] {
		if r < '0' || r > '9' {
			return m
		}
	}
	return m[:i]
}

// IdentityKey canonicalizes for SERIES IDENTITY, which additionally
// strips [1m]. Claude's system/init model and its modelUsage key are
// "claude-fable-5[1m]" while per-request message.model is
// "claude-fable-5" (both fixture-verified). The two must compare equal or
// every request looks like a model switch and the occupancy series never
// accumulates.
//
// Deliberately NOT the key for a window: see NormalizeModel.
func IdentityKey(model string) string {
	return strings.TrimSuffix(NormalizeModel(model), "[1m]")
}

// ModelFromArgs extracts a model id from launch args, for harnesses that
// name none in-stream. Marvel injects no model flag, so this reads what
// the operator wrote. Returns "" when absent, which resolves to
// LimitUnresolved and never to a default.
func ModelFromArgs(args []string) string {
	for i, a := range args {
		switch {
		case a == "-m" || a == "--model":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--model="):
			return strings.TrimPrefix(a, "--model=")
		case strings.HasPrefix(a, "-m="):
			return strings.TrimPrefix(a, "-m=")
		}
	}
	return ""
}

// Request is one denominator resolution.
type Request struct {
	Harness string
	// StreamModel is the model the harness named in-stream, "" when it
	// named none.
	StreamModel string
	// RuntimeArgs is the role's launch args, searched for a model flag
	// when StreamModel is empty.
	RuntimeArgs []string
	// ManifestLimit is runtime.context_window, 0 when unset.
	ManifestLimit int
	// SampleLimit is a window declared in the harness's own stream
	// alongside the sample, 0 when the stream carried none. Resolves as
	// LimitFromStream, the top rung.
	//
	// No production call site populates it today: the stream-declared
	// window reaches the accountant as Sample.DeclaredLimit and is stamped
	// inside the fold, not through Resolve. The field is the eventual
	// route for a feed that declares its window with the sample (a codex
	// per-request record, an OTEL metric), and is exercised by tests.
	SampleLimit int
	// FeedLimit is a window declared on a cooperative side channel rather
	// than in the stream, 0 when none arrived. Resolves as LimitFromFeed,
	// which ranks BELOW ManifestLimit; SampleLimit ranks above it. That is
	// the deliberate asymmetry documented at limitLadder.
	FeedLimit int
}

// Resolver walks the denominator ladder and caches windows a harness has
// declared. Safe for concurrent use.
type Resolver struct {
	mu      sync.RWMutex
	table   Table
	learned map[string]int
	warned  map[string]struct{}
}

// NewResolver builds a resolver over t. A nil table falls back to
// DefaultTable.
func NewResolver(t Table) *Resolver {
	if t == nil {
		t = DefaultTable()
	}
	return &Resolver{
		table:   t,
		learned: make(map[string]int),
		warned:  make(map[string]struct{}),
	}
}

// Resolve walks the ladder and returns the window, the rung that
// produced it, and the resolved model id ("" when none could be found).
// A zero window means unresolved; callers must report absence rather than
// substituting a default.
//
// The branch order below must match limitLadder, which carries the
// reasoning and is asserted against this function in
// TestResolveAgreesWithTheLadder. In short: an in-STREAM declaration
// outranks the operator override because it is what enforces compaction,
// while a declaration on the statusline FEED ranks under it because a
// side channel describes the session rather than governing it. Either one
// contradicting the manifest is logged once, naming both and naming which
// won.
func (r *Resolver) Resolve(req Request) (int, LimitSource, string) {
	model := req.StreamModel
	if model == "" {
		model = ModelFromArgs(req.RuntimeArgs)
	}

	if req.SampleLimit > 0 {
		if req.ManifestLimit > 0 && req.ManifestLimit != req.SampleLimit {
			r.warnOnce("override:"+model, "context window: %s declared %d for %q, overriding the manifest's %d",
				req.Harness, req.SampleLimit, model, req.ManifestLimit)
		}
		return req.SampleLimit, LimitFromStream, model
	}

	if model != "" {
		r.mu.RLock()
		w, ok := r.learned[NormalizeModel(model)]
		r.mu.RUnlock()
		if ok {
			return w, LimitLearned, model
		}
	}

	if req.ManifestLimit > 0 {
		if req.FeedLimit > 0 && req.FeedLimit != req.ManifestLimit {
			r.warnOnce("feed-override:"+model, "context window: %s feed declared %d for %q; the manifest's %d wins, because a side channel does not outrank an operator override",
				req.Harness, req.FeedLimit, model, req.ManifestLimit)
		}
		return req.ManifestLimit, LimitFromManifest, model
	}

	if req.FeedLimit > 0 {
		return req.FeedLimit, LimitFromFeed, model
	}

	if model != "" {
		n := NormalizeModel(model)
		r.mu.RLock()
		w, ok := r.table.Lookup(n)
		var aw int
		var aok bool
		if !ok {
			if canon, has := aliases[n]; has {
				aw, aok = r.table.Lookup(canon)
			}
		}
		r.mu.RUnlock()
		if ok {
			return w, LimitFromTable, model
		}
		if aok {
			return aw, LimitFromTableAlias, model
		}
	}

	return 0, LimitUnresolved, model
}

// Learn records a window a harness declared, keyed on NormalizeModel so
// the [1m] suffix survives (see NormalizeModel). This is how the
// authoritative rung becomes available DURING a later session for a
// harness that only declares its window at the end, which is Claude's
// shape: contextWindow rides the terminal result line.
//
// SCOPE: reached only for a feed that declares a window, so today only
// Claude ever calls it. Codex and opencode declare none in the streams
// marvel reads, so their empty table sections do not fill themselves; they
// need runtime.context_window per role until a feed that carries a window
// exists.
//
// "Anywhere in their streams" was too strong for codex. Its exec stream
// declares none, but its own per-request record declares one on every
// turn start and every request, which makes a codex feed a better route
// than the OTEL metrics (whose conversation_starts carries the window but
// no session identifier, so it cannot be attributed per session).
//
// A declaration that contradicts the shipped table logs once per model:
// that is the drift detector for a vendor window change.
func (r *Resolver) Learn(model string, window int) {
	if model == "" || window <= 0 {
		return
	}
	key := NormalizeModel(model)

	r.mu.Lock()
	prev, had := r.learned[key]
	r.learned[key] = window
	shipped, inTable := r.table.Lookup(key)
	r.mu.Unlock()

	if inTable && shipped != window {
		r.warnOnce("drift:"+key, "context window: harness declares %d for %q; the shipped table says %d, so the table is stale",
			window, key, shipped)
	}
	if had && prev != window {
		r.warnOnce("changed:"+key, "context window for %q changed from %d to %d", key, prev, window)
	}
}

// Learned reports the cached window for a model, for tests and
// diagnostics.
func (r *Resolver) Learned(model string) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.learned[NormalizeModel(model)]
	return w, ok
}

func (r *Resolver) warnOnce(key, format string, args ...any) {
	r.mu.Lock()
	_, seen := r.warned[key]
	if !seen {
		r.warned[key] = struct{}{}
	}
	r.mu.Unlock()
	if !seen {
		log.Printf(format, args...)
	}
}
