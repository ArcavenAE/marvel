package usage

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func TestNormalizeModel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		// The [1m] suffix changes the window 5x and must survive.
		{"claude-fable-5[1m]", "claude-fable-5[1m]"},
		{"claude-sonnet-4-6[1m]", "claude-sonnet-4-6[1m]"},
		// Region prefixes change price, not window.
		{"us.anthropic.claude-opus-4-8[1m]", "claude-opus-4-8[1m]"},
		{"eu.anthropic.claude-haiku-4-5", "claude-haiku-4-5"},
		{"apac.claude-sonnet-5", "claude-sonnet-5"},
		{"anthropic.claude-haiku-4-5", "claude-haiku-4-5"},
		// This repo's own modelUsage key carries a dated suffix.
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5"},
		// A trailing segment that is not eight digits is part of the name.
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"gpt-5-2026", "gpt-5-2026"},
		{"", ""},
		{"  claude-haiku-4-5  ", "claude-haiku-4-5"},
	}
	for _, c := range cases {
		if got := NormalizeModel(c.in); got != c.want {
			t.Errorf("NormalizeModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// An unsuffixed name must MISS rather than resolving to its 1M sibling.
func TestNormalizeModelDoesNotInventTheSuffix(t *testing.T) {
	t.Parallel()
	tbl := DefaultTable()
	if _, ok := tbl.Lookup(NormalizeModel("claude-fable-5")); ok {
		t.Error("claude-fable-5 resolved without its [1m] suffix; only the 1M variant is a table key")
	}
	if w, ok := tbl.Lookup(NormalizeModel("claude-fable-5[1m]")); !ok || w != 1_000_000 {
		t.Errorf("claude-fable-5[1m] = %d (%v), want 1000000", w, ok)
	}
}

func TestIdentityKeyCollapsesTheSuffix(t *testing.T) {
	t.Parallel()
	// system/init and the modelUsage key say claude-fable-5[1m];
	// per-request message.model says claude-fable-5. The two must compare
	// equal or every request looks like a model switch.
	if IdentityKey("claude-fable-5[1m]") != IdentityKey("claude-fable-5") {
		t.Error("the init model and the per-request model do not share an identity key")
	}
	if IdentityKey("claude-haiku-4-5-20251001") != IdentityKey("claude-haiku-4-5") {
		t.Error("the dated modelUsage key and the plain name do not share an identity key")
	}
	if IdentityKey("claude-fable-5") == IdentityKey("claude-haiku-4-5") {
		t.Error("two different models share an identity key")
	}
}

func TestModelFromArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"short flag", []string{"-m", "opencode/deepseek-v4-flash-free"}, "opencode/deepseek-v4-flash-free"},
		{"long flag", []string{"--model", "haiku"}, "haiku"},
		{"long equals", []string{"--model=haiku"}, "haiku"},
		{"short equals", []string{"-m=haiku"}, "haiku"},
		{"among others", []string{"-s", "read-only", "--model", "gpt-x", "--json"}, "gpt-x"},
		{"absent", []string{"--json"}, ""},
		{"nil", nil, ""},
		{"dangling", []string{"--json", "-m"}, ""},
	}
	for _, c := range cases {
		if got := ModelFromArgs(c.args); got != c.want {
			t.Errorf("%s: ModelFromArgs(%v) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

// TestResolveLadderPrecedence walks every rung, asserting the value and the
// LimitSource. The KeyConfidence grade is asserted separately in
// TestResolveGradedGradesEachRung and TestResolveGradedOverDefaultTable,
// which call ResolveGraded; this one exercises the legacy Resolve signature.
func TestResolveLadderPrecedence(t *testing.T) {
	t.Parallel()
	table := Table{"claude-haiku-4-5": 200_000, "claude-opus-4-8": 200_000}

	cases := []struct {
		name       string
		req        Request
		learn      string
		learnValue int
		wantLimit  int
		wantSource LimitSource
	}{
		{
			// Rung 1. The harness's own belief is what enforces
			// compaction, so it outranks the operator override; the
			// override's job is to fill absence, not overrule measurement.
			name:       "stream beats manifest",
			req:        Request{StreamModel: "claude-haiku-4-5", SampleLimit: 999_999, ManifestLimit: 111_111},
			wantLimit:  999_999,
			wantSource: LimitFromStream,
		},
		{
			name:       "learned beats manifest",
			req:        Request{StreamModel: "claude-haiku-4-5", ManifestLimit: 111_111},
			learn:      "claude-haiku-4-5",
			learnValue: 222_222,
			wantLimit:  222_222,
			wantSource: LimitLearned,
		},
		{
			// The asymmetry, stated as a case so it cannot be quietly
			// flipped: the same fact from the same harness loses to the
			// manifest here and beats it above, because a side channel
			// describes the session and the stream governs it.
			name:       "manifest beats feed",
			req:        Request{StreamModel: "claude-haiku-4-5", FeedLimit: 444_444, ManifestLimit: 111_111},
			wantLimit:  111_111,
			wantSource: LimitFromManifest,
		},
		{
			name:       "manifest beats table",
			req:        Request{StreamModel: "claude-haiku-4-5", ManifestLimit: 111_111},
			wantLimit:  111_111,
			wantSource: LimitFromManifest,
		},
		{
			name:       "feed beats table",
			req:        Request{StreamModel: "claude-haiku-4-5", FeedLimit: 444_444},
			wantLimit:  444_444,
			wantSource: LimitFromFeed,
		},
		{
			name:       "table exact hit",
			req:        Request{StreamModel: "claude-haiku-4-5"},
			wantLimit:  200_000,
			wantSource: LimitFromTable,
		},
		{
			name:       "table hit through a dated key",
			req:        Request{StreamModel: "claude-haiku-4-5-20251001"},
			wantLimit:  200_000,
			wantSource: LimitFromTable,
		},
		{
			// An alias means whatever the harness points it at today, so a
			// hit is graded lower than a keyed fact.
			name:       "alias is graded separately",
			req:        Request{RuntimeArgs: []string{"--model", "haiku"}},
			wantLimit:  200_000,
			wantSource: LimitFromTableAlias,
		},
		{
			name:       "unknown model resolves to absence, never a default",
			req:        Request{StreamModel: "some-model-nobody-shipped"},
			wantLimit:  0,
			wantSource: LimitUnresolved,
		},
		{
			name:       "no model at all resolves to absence",
			req:        Request{Harness: "codex"},
			wantLimit:  0,
			wantSource: LimitUnresolved,
		},
	}

	for _, c := range cases {
		r := NewResolver(table)
		if c.learn != "" {
			r.Learn(c.learn, c.learnValue)
		}
		limit, src, _ := r.Resolve(c.req)
		if limit != c.wantLimit {
			t.Errorf("%s: limit = %d, want %d", c.name, limit, c.wantLimit)
		}
		if src != c.wantSource {
			t.Errorf("%s: source = %q, want %q", c.name, src, c.wantSource)
		}
	}
}

func TestResolveReadsModelFromArgsWhenStreamNamesNone(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-haiku-4-5": 200_000})
	limit, src, model := r.Resolve(Request{
		Harness:     "codex",
		RuntimeArgs: []string{"--json", "--model", "claude-haiku-4-5"},
	})
	if model != "claude-haiku-4-5" {
		t.Errorf("model = %q, want claude-haiku-4-5", model)
	}
	if limit != 200_000 || src != LimitFromTable {
		t.Errorf("limit = %d source = %q, want 200000/table", limit, src)
	}
}

// A learned window outranks the operator's manifest (rung 2 over rung 3),
// the same asymmetry the stream rung has at rung 1. The ordering is not in
// dispute here; the SILENCE is. Both neighbouring rungs log when they
// contradict the manifest (stream over manifest, and manifest over feed),
// so the one rung that can silently beat the operator must say so too.
// Regression for aae-orc-yfn2 / marvel#181.
//
// Not parallel: it redirects the standard logger's output, which is global.
// Non-parallel tests run to completion before the parallel ones resume, so
// the redirection is isolated.
func TestResolveWarnsWhenLearnedContradictsManifest(t *testing.T) {
	cases := []struct {
		name          string
		manifestLimit int
		learnValue    int
		wantWarn      bool
	}{
		{"learned overrides manifest → warns", 111_111, 222_222, true},
		{"learned equals manifest → silent", 222_222, 222_222, false},
		{"no manifest → silent", 0, 222_222, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			restore := log.Writer()
			log.SetOutput(&buf)
			defer log.SetOutput(restore)

			r := NewResolver(Table{})
			r.Learn("claude-haiku-4-5", c.learnValue)
			limit, src, _ := r.Resolve(Request{
				Harness:       "claude",
				StreamModel:   "claude-haiku-4-5",
				ManifestLimit: c.manifestLimit,
			})

			// The learned rung still wins — this ticket disputes the
			// silence, not the ordering.
			if limit != c.learnValue || src != LimitLearned {
				t.Fatalf("resolution changed: limit = %d source = %q, want %d/learned", limit, src, c.learnValue)
			}

			got := buf.String()
			if c.wantWarn {
				if got == "" {
					t.Fatal("learned window overrode the manifest silently; expected a warning naming which won")
				}
				// Names the winner (learned) and the manifest as the value
				// being overridden — the role, not just the presence, so an
				// argument swap cannot pass — plus the model.
				for _, want := range []string{"222222", "overriding the manifest's 111111", "claude-haiku-4-5"} {
					if !strings.Contains(got, want) {
						t.Errorf("warning %q does not contain %q", got, want)
					}
				}
			} else if got != "" {
				t.Errorf("expected silence, got warning %q", got)
			}
		})
	}
}

func TestLearnKeepsTheSuffixInItsKey(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{})
	r.Learn("claude-sonnet-4-6[1m]", 1_000_000)

	if w, ok := r.Learned("claude-sonnet-4-6[1m]"); !ok || w != 1_000_000 {
		t.Errorf("learned[1m] = %d (%v), want 1000000", w, ok)
	}
	// The 200k sibling must not inherit the 1M window.
	if _, ok := r.Learned("claude-sonnet-4-6"); ok {
		t.Error("the 200k sibling inherited the 1M window; the learned key must keep [1m]")
	}
}

func TestLearnIgnoresNonsense(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{})
	r.Learn("", 200_000)
	r.Learn("claude-haiku-4-5", 0)
	r.Learn("claude-haiku-4-5", -1)
	if _, ok := r.Learned("claude-haiku-4-5"); ok {
		t.Error("a zero or negative window was cached")
	}
}

// The codex and opencode sections are deliberately empty. A future guessed
// entry has to delete an assertion to land, which is the point.
func TestDefaultTableShipsNoGuessedEntries(t *testing.T) {
	t.Parallel()
	tbl := DefaultTable()
	for key := range tbl {
		if NormalizeModel(key) != key {
			t.Errorf("table key %q is not in normalized form", key)
		}
		if !hasPrefix(key, "claude-") {
			t.Errorf("table key %q is not a Claude model; codex and opencode windows are unmeasured and must stay absent", key)
		}
	}
	// The measured codex window (258400) has no model name attached to it,
	// so it must not ship under a guessed key.
	for key, w := range tbl {
		if w == 258_400 {
			t.Errorf("the unkeyed codex window shipped under %q", key)
		}
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

// Every table entry must round-trip through the resolver, so a typo in a
// key cannot ship as a silently unresolvable model.
func TestEveryDefaultTableEntryResolves(t *testing.T) {
	t.Parallel()
	r := NewResolver(DefaultTable())
	for key, want := range DefaultTable() {
		limit, src, _ := r.Resolve(Request{StreamModel: key})
		if limit != want {
			t.Errorf("%s: limit = %d, want %d", key, limit, want)
		}
		if src != LimitFromTable {
			t.Errorf("%s: source = %q, want %q", key, src, LimitFromTable)
		}
	}
}

// opus-5 is keyed under both spellings because Claude Code stamps [1m]
// on the init model and the modelUsage key, while per-request
// message.model omits it. Both must resolve, and both must resolve to
// the same window: the model has no 200k variant, so a spelling split
// here would be a reporting split, not a real one.
func TestOpus5ResolvesUnderBothSpellings(t *testing.T) {
	t.Parallel()
	r := NewResolver(DefaultTable())
	for _, model := range []string{"claude-opus-5", "claude-opus-5[1m]"} {
		limit, src, _ := r.Resolve(Request{StreamModel: model})
		if limit != 1_000_000 {
			t.Errorf("%s: limit = %d, want 1000000", model, limit)
		}
		if src != LimitFromTable {
			t.Errorf("%s: source = %q, want %q", model, src, LimitFromTable)
		}
	}
	// A dated snapshot must land on the bare key, not fall off the table.
	if limit, src, _ := r.Resolve(Request{StreamModel: "claude-opus-5-20260801"}); limit != 1_000_000 || src != LimitFromTable {
		t.Errorf("dated snapshot: limit = %d, source = %q; want 1000000 from %q", limit, src, LimitFromTable)
	}
}

func TestEveryAliasResolvesToARealEntry(t *testing.T) {
	t.Parallel()
	tbl := DefaultTable()
	for alias, canon := range aliases {
		if _, ok := tbl.Lookup(canon); !ok {
			t.Errorf("alias %q points at %q, which is not a table key", alias, canon)
		}
	}
}

// Only KeyExact is a hard fact; everything else, including the zero value
// and any grade a later writer adds, must read as soft. A consumer that
// forgets to set the grade then defaults to soft rather than silently
// presenting a narrow value as exact.
func TestKeyConfidenceSoft(t *testing.T) {
	t.Parallel()
	cases := []struct {
		c        KeyConfidence
		wantSoft bool
	}{
		{KeyExact, false},
		{KeyNarrow, true},
		{KeyRedirected, true},
		{KeyUndeterminable, true},
		{KeyConfidence(""), true}, // zero value defaults to soft
	}
	for _, c := range cases {
		if got := c.c.Soft(); got != c.wantSoft {
			t.Errorf("KeyConfidence(%q).Soft() = %v, want %v", c.c, got, c.wantSoft)
		}
	}
}

// Every DefaultTable entry's entitlement grade, pinned. This documents the
// default-versus-maximum convention as data (aae-orc-wyza): a bare split key
// is narrow, a [1m] key and a single-window model are exact. A new table
// entry must add its expected grade here, so shipping one is a decision
// about its key confidence, not an accident — the same discipline
// TestDefaultTableShipsNoGuessedEntries enforces for the values.
func TestDefaultTableEntryGrades(t *testing.T) {
	t.Parallel()
	tbl := DefaultTable()
	want := map[string]KeyConfidence{
		"claude-haiku-4-5":      KeyExact,  // no [1m] sibling: single window
		"claude-fable-5[1m]":    KeyExact,  // [1m] key names the entitlement
		"claude-sonnet-4-6":     KeyNarrow, // default half of a 200k/1M split
		"claude-sonnet-4-6[1m]": KeyExact,
		"claude-sonnet-5":       KeyExact, // no 200k variant
		"claude-opus-4-7":       KeyNarrow,
		"claude-opus-4-7[1m]":   KeyExact,
		"claude-opus-4-8":       KeyNarrow,
		"claude-opus-4-8[1m]":   KeyExact,
		"claude-opus-5":         KeyExact, // 1M under both spellings: spelling split, not default/max
		"claude-opus-5[1m]":     KeyExact,
	}
	for key := range tbl {
		if _, named := want[key]; !named {
			t.Errorf("table key %q has no pinned key-confidence grade; add it to this test", key)
		}
	}
	for key, wantKey := range want {
		if _, ok := tbl.Lookup(key); !ok {
			t.Errorf("pinned grade names %q, which is not a table key", key)
			continue
		}
		if got := tbl.keyConfidence(key, tbl[key]); got != wantKey {
			t.Errorf("keyConfidence(%q) = %q, want %q", key, got, wantKey)
		}
	}
}

// keyConfidence must react to the STRUCTURE of the table it is called on,
// not to hard-coded names: a bare key is narrow only when its [1m] sibling
// carries a DIFFERENT window. This is what keeps opus-5 (1M under both
// spellings) exact while sonnet-4-6 (200k/1M) is narrow.
func TestKeyConfidenceReadsTheTableStructure(t *testing.T) {
	t.Parallel()
	// A split the caller invented: bare differs from [1m], so bare is narrow.
	split := Table{"mdl": 200_000, "mdl[1m]": 1_000_000}
	if got := split.keyConfidence("mdl", split["mdl"]); got != KeyNarrow {
		t.Errorf("split bare key: keyConfidence = %q, want KeyNarrow", got)
	}
	if got := split.keyConfidence("mdl[1m]", split["mdl[1m]"]); got != KeyExact {
		t.Errorf("split [1m] key: keyConfidence = %q, want KeyExact", got)
	}
	// Same number under both spellings: a spelling split, not a default/max
	// split, so the bare key is exact.
	same := Table{"mdl": 1_000_000, "mdl[1m]": 1_000_000}
	if got := same.keyConfidence("mdl", same["mdl"]); got != KeyExact {
		t.Errorf("same-value bare key: keyConfidence = %q, want KeyExact", got)
	}
	// No sibling at all: single window, exact.
	solo := Table{"mdl": 200_000}
	if got := solo.keyConfidence("mdl", solo["mdl"]); got != KeyExact {
		t.Errorf("solo key: keyConfidence = %q, want KeyExact", got)
	}
	// Off-contract: a bare key absent from the table, whose [1m] sibling IS
	// present, must not misgrade as narrow. An absent key carries no window,
	// so the caller passes 0, and the window > 0 guard reports KeyExact. This
	// pins the re-read hole closed: without the guard, 0 != sibling would
	// wrongly report KeyNarrow.
	orphanSibling := Table{"mdl[1m]": 1_000_000}
	if got := orphanSibling.keyConfidence("mdl", 0); got != KeyExact {
		t.Errorf("absent bare key with a live sibling: keyConfidence = %q, want KeyExact", got)
	}
}

// TestResolveGradedGradesEachRung walks every rung and asserts the
// KeyConfidence beside the value and source. The grade is orthogonal to the
// source: a directly declared value (stream, learned, manifest, feed) is
// KeyExact whatever its rung, a table hit carries the entry's entitlement
// grade, an alias hit is always narrow, and a miss is undeterminable.
func TestResolveGradedGradesEachRung(t *testing.T) {
	t.Parallel()
	// opus-4-8 has a differently-valued [1m] sibling → its bare key is narrow.
	table := Table{
		"claude-haiku-4-5":    200_000,
		"claude-opus-4-8":     200_000,
		"claude-opus-4-8[1m]": 1_000_000,
	}
	cases := []struct {
		name       string
		req        Request
		learn      string
		learnValue int
		wantLimit  int
		wantSource LimitSource
		wantKey    KeyConfidence
	}{
		{
			name:       "stream is exact",
			req:        Request{StreamModel: "claude-haiku-4-5", SampleLimit: 999_999},
			wantLimit:  999_999,
			wantSource: LimitFromStream,
			wantKey:    KeyExact,
		},
		{
			// No ManifestLimit: a learned hit alone exercises the grade, and
			// avoids the learned-over-manifest warnOnce writing to the global
			// logger under t.Parallel().
			name:       "learned is exact",
			req:        Request{StreamModel: "claude-haiku-4-5"},
			learn:      "claude-haiku-4-5",
			learnValue: 222_222,
			wantLimit:  222_222,
			wantSource: LimitLearned,
			wantKey:    KeyExact,
		},
		{
			name:       "manifest is exact",
			req:        Request{StreamModel: "claude-haiku-4-5", ManifestLimit: 111_111},
			wantLimit:  111_111,
			wantSource: LimitFromManifest,
			wantKey:    KeyExact,
		},
		{
			name:       "feed is exact",
			req:        Request{StreamModel: "claude-haiku-4-5", FeedLimit: 444_444},
			wantLimit:  444_444,
			wantSource: LimitFromFeed,
			wantKey:    KeyExact,
		},
		{
			name:       "table hit on a single-window model is exact",
			req:        Request{StreamModel: "claude-haiku-4-5"},
			wantLimit:  200_000,
			wantSource: LimitFromTable,
			wantKey:    KeyExact,
		},
		{
			name:       "table hit on a split default key is narrow",
			req:        Request{StreamModel: "claude-opus-4-8"},
			wantLimit:  200_000,
			wantSource: LimitFromTable,
			wantKey:    KeyNarrow,
		},
		{
			name:       "table hit on the [1m] key is exact",
			req:        Request{StreamModel: "claude-opus-4-8[1m]"},
			wantLimit:  1_000_000,
			wantSource: LimitFromTable,
			wantKey:    KeyExact,
		},
		{
			name:       "alias hit is narrow even pointing at an exact entry",
			req:        Request{RuntimeArgs: []string{"--model", "haiku"}},
			wantLimit:  200_000,
			wantSource: LimitFromTableAlias,
			wantKey:    KeyNarrow,
		},
		{
			name:       "unknown model is undeterminable",
			req:        Request{StreamModel: "some-model-nobody-shipped"},
			wantLimit:  0,
			wantSource: LimitUnresolved,
			wantKey:    KeyUndeterminable,
		},
		{
			name:       "no model at all is undeterminable",
			req:        Request{Harness: "codex"},
			wantLimit:  0,
			wantSource: LimitUnresolved,
			wantKey:    KeyUndeterminable,
		},
	}
	for _, c := range cases {
		r := NewResolver(table)
		if c.learn != "" {
			r.Learn(c.learn, c.learnValue)
		}
		// These pin the per-rung grade on the vendor default, where the
		// entitlement axis stands alone. The backend axis is exercised
		// separately in TestResolveGradedBackendVerdictGradesKeyedRungs.
		c.req.Redirection = api.BackendDefault
		limit, src, key, _ := r.ResolveGraded(c.req)
		if limit != c.wantLimit {
			t.Errorf("%s: limit = %d, want %d", c.name, limit, c.wantLimit)
		}
		if src != c.wantSource {
			t.Errorf("%s: source = %q, want %q", c.name, src, c.wantSource)
		}
		if key != c.wantKey {
			t.Errorf("%s: key confidence = %q, want %q", c.name, key, c.wantKey)
		}
	}
}

// TestResolveGradedBackendVerdictGradesKeyedRungs is the backend axis of the
// grade, orthogonal to the entitlement axis TestResolveGradedGradesEachRung
// pins. The verdict downgrades ONLY the keyed rungs (table, alias): a
// directly-declared window is correct on any backend, and the learned rung's
// provider fix is its key, not its grade (aae-orc-bv7m). The verdict
// dominates the entitlement grade — a redirected [1m] key (entitlement-exact)
// is still KeyRedirected, because the direct-API window does not apply under
// redirection.
func TestResolveGradedBackendVerdictGradesKeyedRungs(t *testing.T) {
	t.Parallel()
	table := Table{
		"claude-haiku-4-5":    200_000, // single window: entitlement-exact
		"claude-opus-4-8":     200_000, // split default: entitlement-narrow
		"claude-opus-4-8[1m]": 1_000_000,
	}
	cases := []struct {
		name        string
		req         Request
		learn       string
		learnValue  int
		redirection api.BackendRedirection
		wantSource  LimitSource
		wantKey     KeyConfidence
	}{
		{
			name:        "default backend leaves the exact table hit exact",
			req:         Request{StreamModel: "claude-haiku-4-5"},
			redirection: api.BackendDefault,
			wantSource:  LimitFromTable,
			wantKey:     KeyExact,
		},
		{
			name:        "redirected backend downgrades an exact table hit",
			req:         Request{StreamModel: "claude-haiku-4-5"},
			redirection: api.BackendRedirected,
			wantSource:  LimitFromTable,
			wantKey:     KeyRedirected,
		},
		{
			name:        "redirected backend dominates the [1m] entitlement-exact key",
			req:         Request{StreamModel: "claude-opus-4-8[1m]"},
			redirection: api.BackendRedirected,
			wantSource:  LimitFromTable,
			wantKey:     KeyRedirected,
		},
		{
			name:        "cannot-tell backend makes a table hit undeterminable",
			req:         Request{StreamModel: "claude-opus-4-8"},
			redirection: api.BackendUnknown,
			wantSource:  LimitFromTable,
			wantKey:     KeyUndeterminable,
		},
		{
			name:        "redirected backend downgrades an alias hit",
			req:         Request{RuntimeArgs: []string{"--model", "haiku"}},
			redirection: api.BackendRedirected,
			wantSource:  LimitFromTableAlias,
			wantKey:     KeyRedirected,
		},
		{
			// b2d0p does NOT grade the learned rung by the verdict; that is
			// the D-key's job (aae-orc-bv7m). A learned hit stays exact here.
			name:        "learned rung is untouched by the verdict in b2d0p",
			req:         Request{StreamModel: "claude-haiku-4-5"},
			learn:       "claude-haiku-4-5",
			learnValue:  333_333,
			redirection: api.BackendRedirected,
			wantSource:  LimitLearned,
			wantKey:     KeyExact,
		},
		{
			// A directly-declared window is right on any backend.
			name:        "redirected backend does not downgrade a manifest window",
			req:         Request{StreamModel: "claude-haiku-4-5", ManifestLimit: 111_111},
			redirection: api.BackendRedirected,
			wantSource:  LimitFromManifest,
			wantKey:     KeyExact,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := NewResolver(table)
			if c.learn != "" {
				r.Learn(c.learn, c.learnValue)
			}
			c.req.Redirection = c.redirection
			_, src, key, _ := r.ResolveGraded(c.req)
			if src != c.wantSource {
				t.Errorf("source = %q, want %q", src, c.wantSource)
			}
			if key != c.wantKey {
				t.Errorf("key confidence = %q, want %q", key, c.wantKey)
			}
		})
	}
}

// An unresolved resolution always carries KeyUndeterminable: marvel cannot
// vouch for a window it did not find. This is the invariant the refuse-guard
// (aae-orc-bv7m) will preserve when it resolves LimitUnresolved for a narrow
// redirected key.
func TestUnresolvedIsUndeterminable(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-haiku-4-5": 200_000})
	for _, req := range []Request{
		{StreamModel: "some-model-nobody-shipped"},
		{Harness: "codex"},
	} {
		limit, src, key, _ := r.ResolveGraded(req)
		if limit != 0 || src != LimitUnresolved {
			t.Fatalf("expected an unresolved resolution, got %d/%q", limit, src)
		}
		if key != KeyUndeterminable {
			t.Errorf("unresolved key confidence = %q, want KeyUndeterminable", key)
		}
		if !key.Soft() {
			t.Error("an unresolved resolution must be soft")
		}
	}
}

// Resolve is ResolveGraded minus the grade. It must agree on the other
// three returns for every rung, so the legacy signature stays a faithful
// projection and callers that have not adopted the grade are unaffected.
func TestResolveDelegatesToResolveGraded(t *testing.T) {
	t.Parallel()
	r := NewResolver(DefaultTable())
	reqs := []Request{
		{StreamModel: "claude-haiku-4-5", SampleLimit: 999_999},
		{StreamModel: "claude-opus-4-8"},                // narrow table hit
		{StreamModel: "claude-opus-4-8[1m]"},            // exact table hit
		{RuntimeArgs: []string{"--model", "haiku"}},     // alias
		{StreamModel: "some-model-nobody-shipped"},      // unresolved
		{StreamModel: "claude-haiku-4-5", FeedLimit: 5}, // feed
	}
	for _, req := range reqs {
		gLimit, gSrc, _, gModel := r.ResolveGraded(req)
		limit, src, model := r.Resolve(req)
		if limit != gLimit || src != gSrc || model != gModel {
			t.Errorf("Resolve(%+v) = (%d,%q,%q); ResolveGraded = (%d,%q,%q)", req, limit, src, model, gLimit, gSrc, gModel)
		}
	}
}

// The unit tests grade keyConfidence and grade ResolveGraded over ad-hoc
// tables; this one closes the loop by driving ResolveGraded over the SHIPPED
// DefaultTable, so table structure, aliases, and the ladder are asserted
// together against the real data — including that an alias floors to
// KeyNarrow even when it points at an entitlement-exact entry.
func TestResolveGradedOverDefaultTable(t *testing.T) {
	t.Parallel()
	r := NewResolver(DefaultTable())
	cases := []struct {
		name       string
		req        Request
		wantLimit  int
		wantSource LimitSource
		wantKey    KeyConfidence
	}{
		{
			name:       "shipped split default key is narrow",
			req:        Request{StreamModel: "claude-opus-4-8"},
			wantLimit:  200_000,
			wantSource: LimitFromTable,
			wantKey:    KeyNarrow,
		},
		{
			name:       "shipped [1m] key is exact",
			req:        Request{StreamModel: "claude-opus-4-8[1m]"},
			wantLimit:  1_000_000,
			wantSource: LimitFromTable,
			wantKey:    KeyExact,
		},
		{
			name:       "shipped single-window model is exact",
			req:        Request{StreamModel: "claude-opus-5"},
			wantLimit:  1_000_000,
			wantSource: LimitFromTable,
			wantKey:    KeyExact,
		},
		{
			// opus → claude-opus-4-8, a narrow entry: narrow either way.
			name:       "alias onto a narrow entry is narrow",
			req:        Request{RuntimeArgs: []string{"--model", "opus"}},
			wantLimit:  200_000,
			wantSource: LimitFromTableAlias,
			wantKey:    KeyNarrow,
		},
		{
			// haiku → claude-haiku-4-5, an EXACT entry, yet the alias floors
			// it to narrow: an alias is not a keyed fact.
			name:       "alias onto an exact entry still floors to narrow",
			req:        Request{RuntimeArgs: []string{"--model", "haiku"}},
			wantLimit:  200_000,
			wantSource: LimitFromTableAlias,
			wantKey:    KeyNarrow,
		},
	}
	for _, c := range cases {
		c.req.Redirection = api.BackendDefault // grades over the shipped table on the vendor default
		limit, src, key, _ := r.ResolveGraded(c.req)
		if limit != c.wantLimit || src != c.wantSource || key != c.wantKey {
			t.Errorf("%s: got (%d, %q, %q), want (%d, %q, %q)",
				c.name, limit, src, key, c.wantLimit, c.wantSource, c.wantKey)
		}
	}
}
