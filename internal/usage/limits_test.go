package usage

import (
	"bytes"
	"log"
	"strings"
	"testing"
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

// TestResolveLadderPrecedence walks every rung, asserting both the value
// and the grade. The grade is load-bearing: a consumer must be able to
// refuse on low confidence without refusing on measurement.
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
				// Names both numbers (winner and loser) and the model, in
				// the shape of the neighbouring stream and feed warnings.
				for _, want := range []string{"222222", "111111", "claude-haiku-4-5"} {
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
