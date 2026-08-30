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
			r.Learn(c.learn, c.learnValue, api.BackendDefault)
		}
		// Precedence is exercised on the vendor default, so the keyed rungs
		// resolve rather than being refused by the backend guard.
		c.req.Redirection = api.BackendDefault
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
		Redirection: api.BackendDefault,
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
			r.Learn("claude-haiku-4-5", c.learnValue, api.BackendDefault)
			limit, src, _ := r.Resolve(Request{
				Harness:       "claude",
				StreamModel:   "claude-haiku-4-5",
				ManifestLimit: c.manifestLimit,
				Redirection:   api.BackendDefault,
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
	r.Learn("claude-sonnet-4-6[1m]", 1_000_000, api.BackendDefault)

	if w, ok := r.Learned("claude-sonnet-4-6[1m]", api.BackendDefault); !ok || w != 1_000_000 {
		t.Errorf("learned[1m] = %d (%v), want 1000000", w, ok)
	}
	// The 200k sibling must not inherit the 1M window.
	if _, ok := r.Learned("claude-sonnet-4-6", api.BackendDefault); ok {
		t.Error("the 200k sibling inherited the 1M window; the learned key must keep [1m]")
	}
}

// The learned cache is segregated by backend verdict (D-key, aae-orc-bv7m):
// a window measured under one backend is invisible under another, so a
// value learned on the vendor default is never served to a redirected
// session and vice versa.
func TestLearnedIsSegregatedByBackend(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{})
	r.Learn("claude-haiku-4-5", 200_000, api.BackendDefault)
	r.Learn("claude-haiku-4-5", 180_000, api.BackendRedirected)

	if w, ok := r.Learned("claude-haiku-4-5", api.BackendDefault); !ok || w != 200_000 {
		t.Errorf("default-backend learned = %d (%v), want 200000", w, ok)
	}
	if w, ok := r.Learned("claude-haiku-4-5", api.BackendRedirected); !ok || w != 180_000 {
		t.Errorf("redirected-backend learned = %d (%v), want 180000", w, ok)
	}
	// A verdict nobody learned under sees nothing, even for a known model.
	if _, ok := r.Learned("claude-haiku-4-5", api.BackendUnknown); ok {
		t.Error("a window leaked across backend verdicts; the learned key must carry the verdict")
	}
}

func TestLearnIgnoresNonsense(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{})
	r.Learn("", 200_000, api.BackendDefault)
	r.Learn("claude-haiku-4-5", 0, api.BackendDefault)
	r.Learn("claude-haiku-4-5", -1, api.BackendDefault)
	if _, ok := r.Learned("claude-haiku-4-5", api.BackendDefault); ok {
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
		limit, src, _ := r.Resolve(Request{StreamModel: key, Redirection: api.BackendDefault})
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
		limit, src, _ := r.Resolve(Request{StreamModel: model, Redirection: api.BackendDefault})
		if limit != 1_000_000 {
			t.Errorf("%s: limit = %d, want 1000000", model, limit)
		}
		if src != LimitFromTable {
			t.Errorf("%s: source = %q, want %q", model, src, LimitFromTable)
		}
	}
	// A dated snapshot must land on the bare key, not fall off the table.
	if limit, src, _ := r.Resolve(Request{StreamModel: "claude-opus-5-20260801", Redirection: api.BackendDefault}); limit != 1_000_000 || src != LimitFromTable {
		t.Errorf("dated snapshot: limit = %d, source = %q; want 1000000 from %q", limit, src, LimitFromTable)
	}
}

// sonnet-5 is the same case as opus-5: no 200k variant, so Claude Code's
// [1m] stamp on the init model and modelUsage key is a spelling split, not
// a default/max split. Both spellings must resolve to the one 1M window;
// before the [1m] key was added, a real claude-sonnet-5[1m] session fell
// off the table and rendered "?".
func TestSonnet5ResolvesUnderBothSpellings(t *testing.T) {
	t.Parallel()
	r := NewResolver(DefaultTable())
	for _, model := range []string{"claude-sonnet-5", "claude-sonnet-5[1m]"} {
		limit, src, _ := r.Resolve(Request{StreamModel: model, Redirection: api.BackendDefault})
		if limit != 1_000_000 {
			t.Errorf("%s: limit = %d, want 1000000", model, limit)
		}
		if src != LimitFromTable {
			t.Errorf("%s: source = %q, want %q", model, src, LimitFromTable)
		}
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
		"claude-sonnet-5":       KeyExact, // 1M under both spellings: spelling split, not default/max
		"claude-sonnet-5[1m]":   KeyExact,
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
			r.Learn(c.learn, c.learnValue, api.BackendDefault)
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

// TestResolveGradedBackendVerdictOnKeyedRungs pins the cases the verdict does
// NOT refuse (the refuse itself is TestResolveRefusesKeyedRungOnBackendDeparture):
// a default-backend table hit keeps its entitlement grade, a learned hit under
// a matching backend is trusted (KeyExact — the D-key made the lookup
// backend-matched), and a directly-declared window is right on any backend.
func TestResolveGradedBackendVerdictOnKeyedRungs(t *testing.T) {
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
			name:        "default backend keeps an exact table hit exact",
			req:         Request{StreamModel: "claude-haiku-4-5"},
			redirection: api.BackendDefault,
			wantSource:  LimitFromTable,
			wantKey:     KeyExact,
		},
		{
			name:        "default backend keeps a split table hit narrow",
			req:         Request{StreamModel: "claude-opus-4-8"},
			redirection: api.BackendDefault,
			wantSource:  LimitFromTable,
			wantKey:     KeyNarrow,
		},
		{
			// The learned rung is backend-matched by its key, so a redirected
			// session's learned hit is trusted rather than refused.
			name:        "learned hit under a matching redirected backend is exact",
			req:         Request{StreamModel: "claude-haiku-4-5"},
			learn:       "claude-haiku-4-5",
			learnValue:  333_333,
			redirection: api.BackendRedirected,
			wantSource:  LimitLearned,
			wantKey:     KeyExact,
		},
		{
			// A directly-declared window is right on any backend.
			name:        "redirected backend does not touch a manifest window",
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
				// Learn under the same verdict the request carries, so the
				// D-key lookup matches and the case exercises a learned hit.
				r.Learn(c.learn, c.learnValue, c.redirection)
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

// The refuse-guard (aae-orc-bv7m): a table or alias hit under a redirected or
// unobserved backend resolves to absence rather than the direct-API window.
// It refuses WITHIN the rung (never falling through to a lower one) and keeps
// the LimitUnresolved ⇒ KeyUndeterminable invariant. The refuse propagates
// through Resolve too, so the legacy signature and the heartbeat path that
// calls it are covered without plumbing the grade.
func TestResolveRefusesKeyedRungOnBackendDeparture(t *testing.T) {
	t.Parallel()
	table := Table{"claude-haiku-4-5": 200_000}
	cases := []struct {
		name        string
		req         Request
		redirection api.BackendRedirection
	}{
		{"redirected table hit refuses", Request{StreamModel: "claude-haiku-4-5"}, api.BackendRedirected},
		{"cannot-tell table hit refuses", Request{StreamModel: "claude-haiku-4-5"}, api.BackendUnknown},
		{"redirected alias hit refuses", Request{RuntimeArgs: []string{"--model", "haiku"}}, api.BackendRedirected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := NewResolver(table)
			c.req.Redirection = c.redirection
			limit, src, key, _ := r.ResolveGraded(c.req)
			if limit != 0 || src != LimitUnresolved {
				t.Fatalf("got (%d, %q), want an unresolved resolution", limit, src)
			}
			if key != KeyUndeterminable {
				t.Errorf("key confidence = %q, want KeyUndeterminable", key)
			}
			// Resolve must agree — the refuse is what the heartbeat path sees.
			if l, s, _ := r.Resolve(c.req); l != 0 || s != LimitUnresolved {
				t.Errorf("Resolve = (%d, %q), want unresolved", l, s)
			}
		})
	}
}

// A directly-declared window is correct on any backend, so the guard leaves
// it alone: a redirected session with a manifest override still resolves it.
func TestResolveDoesNotRefuseDeclaredWindowsUnderRedirection(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-haiku-4-5": 200_000})
	limit, src, _ := r.Resolve(Request{
		StreamModel:   "claude-haiku-4-5",
		ManifestLimit: 500_000,
		Redirection:   api.BackendRedirected,
	})
	if limit != 500_000 || src != LimitFromManifest {
		t.Errorf("got (%d, %q), want 500000/manifest — a stated window is right on any backend", limit, src)
	}
}

// The finding's "one session teaches the window" behavior: a redirected
// session refuses the table at cold start, but once a session has taught the
// window under that backend, the learned rung answers and is trusted.
func TestRedirectedSessionResolvesOnceLearned(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-haiku-4-5": 200_000})
	req := Request{StreamModel: "claude-haiku-4-5", Redirection: api.BackendRedirected}

	// Cold start: the table refuses, so CTX% renders absent.
	if limit, src, _ := r.Resolve(req); limit != 0 || src != LimitUnresolved {
		t.Fatalf("cold start = (%d, %q), want unresolved", limit, src)
	}

	// The harness declares its window under this backend.
	r.Learn("claude-haiku-4-5", 180_000, api.BackendRedirected)

	limit, src, key, _ := r.ResolveGraded(req)
	if limit != 180_000 || src != LimitLearned {
		t.Fatalf("after learning = (%d, %q), want 180000/learned", limit, src)
	}
	if key != KeyExact {
		t.Errorf("learned key confidence = %q, want KeyExact (backend-matched by the key)", key)
	}
	// A default-backend session must NOT see the redirected learned window.
	if limit, src, _ := r.Resolve(Request{StreamModel: "claude-haiku-4-5", Redirection: api.BackendDefault}); limit != 200_000 || src != LimitFromTable {
		t.Errorf("default session = (%d, %q), want the table's 200000, not the redirected 180000", limit, src)
	}
}

// An unresolved resolution always carries KeyUndeterminable: marvel cannot
// vouch for a window it did not find. This is the invariant the refuse-guard
// (aae-orc-bv7m) preserves when it resolves LimitUnresolved for a redirected
// or unobserved keyed hit.
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
		req.Redirection = api.BackendDefault // Resolve/ResolveGraded must agree on any backend; default keeps the keyed rungs resolving
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
