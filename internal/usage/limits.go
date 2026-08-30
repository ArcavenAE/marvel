package usage

import (
	"log"
	"strings"
	"sync"

	"github.com/arcavenae/marvel/internal/api"
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

// KeyConfidence grades how well the key that resolved a window determines
// the window itself. It rides BESIDE LimitSource, not instead of it: the
// two axes are orthogonal. LimitSource says which rung of the ladder
// produced the number; KeyConfidence says whether the key that rung used
// was as wide as the fact it stored.
//
// The distinction exists because a miss is loud and a wrong hit is silent
// (finding-031). A window that misses renders absence and emits
// context.limit-unresolved; a window resolved on a key too narrow to be
// right renders a number that reads exactly like a correct one. Grading the
// key is how a consumer tells the two apart: a renderer marks a soft value
// low-confidence rather than presenting it as an exact fact (see Soft), and
// an admission gate can refuse on a narrow key without refusing on a
// measurement.
//
// Two axes make a key narrow, and they resolve at different times:
//
//   - ENTITLEMENT (static, graded here today). A bare model id naming the
//     DEFAULT half of a default/max split does not carry the entitlement
//     that decides which half a session gets. See the default-versus-maximum
//     convention on DefaultTable and Table.keyConfidence.
//   - PROVIDER (dynamic, graded via req.Redirection). The same id can name
//     different windows under different backends (finding-031: the catalog
//     assigns claude-opus-5 1000000 under anthropic and 264000 under
//     copilot), and marvel cannot observe which backend served a request —
//     but it observes at spawn whether the backend-selecting environment
//     departs from the vendor default (api.ClassifyBackendRedirection). That
//     verdict rides in on Request.Redirection and downgrades a keyed hit to
//     KeyRedirected or KeyUndeterminable (aae-orc-b2d0p). See applyRedirection
//     and the discriminator note in ResolveGraded.
type KeyConfidence string

const (
	// KeyExact means the key fully determined the window: a value declared
	// directly (stream, learned, manifest, feed — no keyed lookup), or a
	// table hit whose id leaves no entitlement ambiguity. The only grade
	// that is NOT soft.
	KeyExact KeyConfidence = "exact"
	// KeyNarrow means the key was narrower than the fact it stored, so the
	// number may be wrong and must be presented soft. Today it arises two
	// ways: an entitlement-split table entry resolved on its default-half
	// key, and any alias hit (an alias names whatever the harness points it
	// at, so a hit that happens to be right is not a keyed fact).
	KeyNarrow KeyConfidence = "narrow"
	// KeyRedirected means the discriminator found the session redirected off
	// the vendor default (req.Redirection == api.BackendRedirected), so a
	// table or alias value keyed on the direct-API window does not apply.
	// Produced by applyRedirection; the refuse-guard (aae-orc-bv7m) turns it
	// into LimitUnresolved on those rungs.
	KeyRedirected KeyConfidence = "redirected"
	// KeyUndeterminable means marvel cannot vouch for the window. It marks
	// an unresolved resolution (no rung produced a window) and a keyed hit
	// under an unobserved backend (req.Redirection == api.BackendUnknown,
	// "cannot tell"), which the guard treats as departure from default
	// (finding-031). Either way: not a number to trust.
	KeyUndeterminable KeyConfidence = "undeterminable"
)

// Soft reports whether a resolution should be presented as low-confidence
// rather than as an exact fact. Everything but KeyExact is soft, so the
// zero value and any grade a later writer adds default to soft rather than
// silently reading as exact.
//
// Soft is the KEY axis only — whether the key was as wide as the fact. It is
// not the whole trustworthiness of a number: how far the producing CHANNEL
// can be trusted is the other axis, carried by LimitSource.Rank (a feed, for
// instance, is KeyExact here because it declares a number directly, yet ranks
// low because a side channel can change meaning without a version handle). A
// consumer that wants full confidence must consult both.
func (c KeyConfidence) Soft() bool {
	return c != KeyExact
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
//
// # The default-versus-maximum convention
//
// A model with an extended-context beta appears under two keys: the bare id
// carries the DEFAULT window and the [1m]-suffixed id the MAXIMUM. The
// vendor's models-overview page lists a single context-window figure (the
// maximum) for such a model, with no default/max split shown, so the table
// and the page do not disagree about a number — they disagree about what the
// number MEANS. That gap has cost a re-verification round before: reading 1M
// on the page and 200000 in the code looks like staleness (aae-orc-wyza, the
// gh-144 review, 2026-08-08). Stated so it need not be rediscovered:
//
//   - bare = the default window, [1m] = the extended window Claude Code
//     opted into (NormalizeModel keeps the suffix, so the two never collapse);
//   - a model whose only window is 1M carries the same number under both
//     spellings (claude-opus-5) or under [1m] alone (claude-fable-5[1m]),
//     which is a spelling split, not a default/max split.
//
// A bare split key is therefore entitlement-NARROW (see Table.keyConfidence):
// it names a default whose applicability to a given session the id cannot
// confirm. Whether a bare-keyed session actually receives the default is
// empirically open (aae-orc-wyza) and a fixture would settle it, as
// claude-fable-5[1m] settled the suffix convention.
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

// keyConfidence grades an EXACT table hit on the ENTITLEMENT axis (see the
// default-versus-maximum convention on DefaultTable). A bare key whose [1m]
// sibling carries a DIFFERENT window is the default half of a default/max
// split, and a bare-keyed session's true window turns on an entitlement the
// id does not carry, so the hit is KeyNarrow. Every other hit is
// entitlement-exact: a [1m] key names the extended entitlement outright, and
// a single-window model (no sibling, or a sibling carrying the same number,
// as claude-opus-5) has no split to be wrong about.
//
// This grades entitlement only. The provider axis (a redirected backend
// serving a different window under the same id) is not visible here; the
// discriminator downgrades a redirected hit at Resolve time (aae-orc-b2d0p).
// See the seam in ResolveGraded.
//
// normalizedKey must already be NormalizeModel'd, and window is the hit value
// Lookup returned for it. The method takes the value rather than re-reading
// the map so its result depends only on what the caller confirmed. The
// window > 0 guard makes an off-contract call safe: no entry in this system
// carries a 0 window (DefaultTable has none and Learn rejects them), so a
// window of 0 means "not a real hit" and the method reports KeyExact rather
// than misgrading an absent bare key as narrow against a live [1m] sibling.
func (t Table) keyConfidence(normalizedKey string, window int) KeyConfidence {
	if strings.HasSuffix(normalizedKey, "[1m]") {
		return KeyExact
	}
	if sib, ok := t[normalizedKey+"[1m]"]; ok && window > 0 && window != sib {
		return KeyNarrow
	}
	return KeyExact
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
	// Redirection is the spawn-time backend verdict for this session (the
	// discriminator of finding-031). It grades a keyed rung: the table and
	// alias rungs hold the vendor's DIRECT-API windows, so a redirected or
	// unobserved backend downgrades a hit off KeyExact/KeyNarrow. The
	// directly-declared rungs (stream, manifest, feed) are unaffected — a
	// number the harness or operator stated is correct on any backend. The
	// zero value is BackendUnknown ("cannot tell"), treated as departure
	// from default. See ResolveGraded and api.ClassifyBackendRedirection.
	Redirection api.BackendRedirection
}

// learnedKey identifies a learned window by model AND the backend verdict
// the learning session ran under (the D-key of finding-031 §5). One Resolver
// is shared across a daemon's sessions, so keying on the model alone lets a
// window measured under one backend be served to a session on another. The
// verdict segregates the cache: a hit is backend-matched by construction, so
// it is a measurement that applies to this session, not a cross-backend
// guess — which is why the learned rung is trusted (KeyExact) rather than
// downgraded the way the model-only table rung must be.
//
// The segregation is coarse: BackendRedirected does not name WHICH backend,
// so two different redirected backends share a bucket. That residue is
// accepted (finding-031 §5, "smaller than today's, not zero"); putting the
// provider in the key is option B, deferred behind the eooi standing trigger.
type learnedKey struct {
	model   string
	backend api.BackendRedirection
}

// Resolver walks the denominator ladder and caches windows a harness has
// declared. Safe for concurrent use.
type Resolver struct {
	mu      sync.RWMutex
	table   Table
	learned map[learnedKey]int
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
		learned: make(map[learnedKey]int),
		warned:  make(map[string]struct{}),
	}
}

// Resolve walks the ladder and returns the window, the rung that produced
// it, and the resolved model id ("" when none could be found). A zero
// window means unresolved; callers must report absence rather than
// substituting a default.
//
// Resolve is ResolveGraded without the key-confidence grade, kept so
// callers that do not yet plumb the grade are unaffected. A new consumer
// that renders or gates on confidence should call ResolveGraded.
func (r *Resolver) Resolve(req Request) (int, LimitSource, string) {
	limit, src, _, model := r.ResolveGraded(req)
	return limit, src, model
}

// ResolveGraded is Resolve plus a KeyConfidence grade beside the
// LimitSource (see KeyConfidence). The branch order must match limitLadder,
// which carries the reasoning; Resolve delegates here, so the ladder order
// is covered through Resolve by TestResolveAgreesWithTheLadder, and
// TestResolveDelegatesToResolveGraded pins that the two agree. In short: an
// in-STREAM declaration outranks the operator override because it is what
// enforces compaction, while a declaration on the statusline FEED ranks
// under it because a side channel describes the session rather than governing
// it. Any rung that contradicts the manifest is logged once, naming both and
// naming which won.
//
// Grades, today: stream, manifest and feed declare a number directly (no
// keyed lookup narrowed it) and are KeyExact. Learned is KeyExact too — it is
// a harness measurement matched on its own [1m]-preserving key AND on the
// backend verdict (learnedKey), so its old provider narrowness — a value
// learned under one backend served to a session on another — is now closed by
// the key rather than the grade (aae-orc-bv7m D-key). A table hit inherits the
// entry's entitlement grade (Table.keyConfidence) on the vendor default and is
// graded/refused by the backend verdict otherwise (see below); an alias hit is
// always KeyNarrow on the default (an alias names whatever the harness points
// it at, so a hit is not a keyed fact); an unresolved resolution is
// KeyUndeterminable — marvel cannot vouch for a window it did not find.
//
// THE DISCRIMINATOR (aae-orc-b2d0p, landed here): a redirected backend can
// serve a different window under the same id, and marvel cannot see WHICH
// backend served a request (finding-031). What it can see is the spawn-time
// backend-selecting environment, carried in as req.Redirection. It grades
// the KEYED rungs — table and alias — through applyRedirection: a hit under
// a redirected backend becomes KeyRedirected, and one under an unobserved
// backend (the zero value, "cannot tell") becomes KeyUndeterminable. The
// verdict DOMINATES the entitlement grade in this single enum, because a
// direct-API table value does not apply under redirection whether or not
// the entitlement was also ambiguous; the entitlement grade stands only on
// the vendor default. The directly-declared rungs (stream, manifest, feed)
// are NOT graded by the backend — a number the harness or operator stated
// is correct on any backend. The learned rung's provider narrowness is a
// KEY problem, not a grade one, and is fixed by keying it on the same
// verdict (aae-orc-bv7m / finding-031 §5 D-key), sequenced there.
//
// The refuse-guard (aae-orc-bv7m, landed) then turns
// KeyRedirected/KeyUndeterminable on the table and alias rungs into
// LimitUnresolved via refuseKeyed — WITHIN the rung and never reordering the
// ladder (TestResolveAgreesWithTheLadder constrains this) — so a redirected or
// unobserved table window renders absent rather than a confident wrong number.
// The refuse flows through Resolve too, so the heartbeat path that calls the
// ungraded signature refuses identically. On the vendor default nothing is
// refused, which is the common case and unchanged.
func (r *Resolver) ResolveGraded(req Request) (int, LimitSource, KeyConfidence, string) {
	model := req.StreamModel
	if model == "" {
		model = ModelFromArgs(req.RuntimeArgs)
	}

	if req.SampleLimit > 0 {
		if req.ManifestLimit > 0 && req.ManifestLimit != req.SampleLimit {
			r.warnOnce("override:"+model, "context window: %s declared %d for %q, overriding the manifest's %d",
				req.Harness, req.SampleLimit, model, req.ManifestLimit)
		}
		return req.SampleLimit, LimitFromStream, KeyExact, model
	}

	if model != "" {
		r.mu.RLock()
		w, ok := r.learned[learnedKey{NormalizeModel(model), req.Redirection}]
		r.mu.RUnlock()
		if ok {
			if req.ManifestLimit > 0 && req.ManifestLimit != w {
				r.warnOnce("learned-override:"+model, "context window: %s declared %d for %q in a prior session, overriding the manifest's %d",
					req.Harness, w, model, req.ManifestLimit)
			}
			// KeyExact, not graded by the verdict: the learnedKey already
			// carries it, so a hit was measured under this session's backend
			// (aae-orc-bv7m / finding-031 §5 D-key). A cold-start redirected
			// session misses here and falls through to the table, which
			// refuses — until one session teaches the window under this
			// backend, at which point this rung answers.
			return w, LimitLearned, KeyExact, model
		}
	}

	if req.ManifestLimit > 0 {
		if req.FeedLimit > 0 && req.FeedLimit != req.ManifestLimit {
			r.warnOnce("feed-override:"+model, "context window: %s feed declared %d for %q; the manifest's %d wins, because a side channel does not outrank an operator override",
				req.Harness, req.FeedLimit, model, req.ManifestLimit)
		}
		return req.ManifestLimit, LimitFromManifest, KeyExact, model
	}

	if req.FeedLimit > 0 {
		return req.FeedLimit, LimitFromFeed, KeyExact, model
	}

	if model != "" {
		n := NormalizeModel(model)
		r.mu.RLock()
		w, ok := r.table.Lookup(n)
		var key KeyConfidence
		if ok {
			key = r.table.keyConfidence(n, w)
		}
		var aw int
		var aok bool
		if !ok {
			if canon, has := aliases[n]; has {
				aw, aok = r.table.Lookup(canon)
			}
		}
		r.mu.RUnlock()
		if ok {
			grade := applyRedirection(key, req.Redirection)
			if refuseOnBackend(grade) {
				return r.refuseKeyed(model, req.Redirection)
			}
			return w, LimitFromTable, grade, model
		}
		if aok {
			grade := applyRedirection(KeyNarrow, req.Redirection)
			if refuseOnBackend(grade) {
				return r.refuseKeyed(model, req.Redirection)
			}
			return aw, LimitFromTableAlias, grade, model
		}
	}

	return 0, LimitUnresolved, KeyUndeterminable, model
}

// refuseOnBackend reports whether a keyed-rung grade is one the refuse-guard
// withholds: a table value keyed on the vendor's direct-API window cannot be
// vouched for once the backend is redirected or unobserved.
func refuseOnBackend(grade KeyConfidence) bool {
	return grade == KeyRedirected || grade == KeyUndeterminable
}

// refuseKeyed is the guard of aae-orc-bv7m: a table or alias hit under a
// redirected or unobserved backend resolves to absence rather than the
// direct-API window, so CTX% renders "?" (loud) instead of a confident wrong
// number (silent). It refuses WITHIN the rung — it does not fall through to a
// lower one, because a lower rung is a worse guess, not a better one — and it
// returns KeyUndeterminable to keep the ladder invariant that an unresolved
// resolution is always undeterminable (TestUnresolvedIsUndeterminable). The
// warning distinguishes this from a plain table miss: the model IS known, its
// backend is not the default.
func (r *Resolver) refuseKeyed(model string, redir api.BackendRedirection) (int, LimitSource, KeyConfidence, string) {
	r.warnOnce("backend-refuse:"+model, "context window: %q has a shipped table window, but this session's backend is %s, so the direct-API value is withheld and CTX%% renders absent",
		model, redir)
	return 0, LimitUnresolved, KeyUndeterminable, model
}

// applyRedirection folds the spawn-time backend verdict into the entitlement
// grade of a KEYED rung (table or alias — the rungs holding the vendor's
// direct-API windows). The verdict dominates: a redirected backend
// invalidates the direct-API value regardless of entitlement, and an
// unobserved backend cannot be vouched for, so both override KeyExact and
// KeyNarrow alike. On the vendor default the entitlement grade stands.
//
// It is deliberately NOT applied to the directly-declared rungs (a stated
// number is right on any backend) nor to the learned rung (whose provider
// narrowness is fixed by keying it on the verdict — aae-orc-bv7m — not by
// grading it here).
func applyRedirection(entitlement KeyConfidence, r api.BackendRedirection) KeyConfidence {
	switch r {
	case api.BackendRedirected:
		return KeyRedirected
	case api.BackendUnknown:
		return KeyUndeterminable
	default: // api.BackendDefault
		return entitlement
	}
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
//
// backend is the verdict the learning session ran under; it segregates the
// cache so a window is only served back to a session on the same backend
// (aae-orc-bv7m / finding-031 §5 D-key). The drift check against the shipped
// table stays keyed on the model alone — the table is the direct-API window,
// and a redirected declaration disagreeing with it is expected, not drift —
// so drift is only flagged for a default-backend declaration.
func (r *Resolver) Learn(model string, window int, backend api.BackendRedirection) {
	if model == "" || window <= 0 {
		return
	}
	mkey := NormalizeModel(model)
	lk := learnedKey{mkey, backend}

	r.mu.Lock()
	prev, had := r.learned[lk]
	r.learned[lk] = window
	shipped, inTable := r.table.Lookup(mkey)
	r.mu.Unlock()

	if inTable && backend == api.BackendDefault && shipped != window {
		r.warnOnce("drift:"+mkey, "context window: harness declares %d for %q; the shipped table says %d, so the table is stale",
			window, mkey, shipped)
	}
	if had && prev != window {
		r.warnOnce("changed:"+mkey+":"+string(backend), "context window for %q changed from %d to %d", mkey, prev, window)
	}
}

// Learned reports the cached window for a model under a backend verdict, for
// tests and diagnostics. The backend is part of the key (see learnedKey), so
// a window learned under one backend is invisible under another.
func (r *Resolver) Learned(model string, backend api.BackendRedirection) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.learned[learnedKey{NormalizeModel(model), backend}]
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
