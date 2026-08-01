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
	// LimitFromTable means an exact hit in the shipped model table.
	LimitFromTable LimitSource = "table"
	// LimitFromTableAlias means a short alias resolved to a table entry.
	// Graded separately because an alias means whatever the harness
	// points it at today: a guess that happens to be right is not a
	// keyed fact.
	LimitFromTableAlias LimitSource = "table-alias"
)

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

		// codex: DELIBERATELY EMPTY. A window of 258400 was measured on
		// codex 0.146.0 (the rollout file's
		// token_count.info.model_context_window), but the model NAME was
		// not captured, because thread.started carries only thread_id.
		// The number therefore has no key. One `codex exec --model <m>`
		// turn, or a read of the default in the user's codex config, keys
		// it and this section gains a line. Nothing fills it by itself:
		// codex declares no window in its exec stream, so Learn is never
		// reached for it (see Learn). Until then codex CTX% renders `?`
		// unless an operator sets runtime.context_window. Shipping 258400
		// under a guessed key would be an invisible limitation; an empty
		// section is a visible one with a one-line fix.

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
	// SampleLimit is a window declared by the feed alongside the sample,
	// 0 when the feed carried none.
	SampleLimit int
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
// Rung order puts the in-feed declaration above the operator override on
// purpose: the harness's own belief about the window is what enforces
// compaction, so it is ground truth for behavior. The override's job is
// to fill absence, not to overrule measurement. A manifest value later
// contradicted by an in-feed declaration is logged once, naming both.
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
		return req.ManifestLimit, LimitFromManifest, model
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
// Claude ever calls it. Codex and opencode declare none anywhere in their
// streams, so their empty table sections do not fill themselves; they need
// a keyed measurement in DefaultTable or runtime.context_window per role.
// A codex OTEL feed (conversation_starts carries the model) is the first
// plausible route to changing that.
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
