package api

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sessionOn(harness string, backend Backend, src BackendCredentialSource, home, private string) Session {
	return Session{
		Runtime:                 Runtime{Name: harness},
		BackendResolved:         backend,
		BackendCredentialSource: src,
		AccountHome:             home,
		HarnessHome:             private,
	}
}

// Test 17: the private home differs for every session; the login it links
// from is the same, so the account is the same.
func TestAccountKeyIgnoresThePrivateHome(t *testing.T) {
	t.Parallel()
	a := sessionOn("codex", BackendDefaultName, "", "/home/op/.codex", "/state/homes/aaa")
	b := sessionOn("codex", BackendDefaultName, "", "/home/op/.codex", "/state/homes/bbb")
	if AccountKeyOf(a) != AccountKeyOf(b) {
		t.Fatalf("two codex sessions on one login got different keys: %v and %v", AccountKeyOf(a), AccountKeyOf(b))
	}
	if AccountKeyOf(a).ConfigHome != "/home/op/.codex" {
		t.Fatalf("config home = %q, want the source home", AccountKeyOf(a).ConfigHome)
	}
}

func TestAccountKeyDiffersWhereTheLimitWould(t *testing.T) {
	t.Parallel()
	base := sessionOn("claude", BackendDefaultName, "", "", "")
	for name, other := range map[string]Session{
		"harness":           sessionOn("codex", BackendDefaultName, "", "", ""),
		"backend":           sessionOn("claude", BackendBedrock, "", "", ""),
		"credential source": sessionOn("claude", BackendDefaultName, BackendCredentialSource("profile:work"), "", ""),
		"config home":       sessionOn("claude", BackendDefaultName, "", "/home/op/.claude-work", ""),
	} {
		if AccountKeyOf(base) == AccountKeyOf(other) {
			t.Errorf("%s: keys equal, want different", name)
		}
	}
}

// A record that predates the backend fields resolves to the same account as
// one classified as the default backend: subscription resolves to default.
func TestAccountKeyUnclassifiedBackendIsDefault(t *testing.T) {
	t.Parallel()
	a := sessionOn("claude", "", "", "", "")
	b := sessionOn("claude", BackendDefaultName, "", "", "")
	if AccountKeyOf(a) != AccountKeyOf(b) {
		t.Fatalf("unclassified %v != default %v", AccountKeyOf(a), AccountKeyOf(b))
	}
}

func TestAccountKeyStringNamesEveryPart(t *testing.T) {
	t.Parallel()
	s := AccountKeyOf(sessionOn("codex", BackendDefaultName, "", "/home/op/.codex", "")).String()
	for _, want := range []string{"codex", "default", "/home/op/.codex"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q does not name %q", s, want)
		}
	}
	if AccountKeyOf(sessionOn("claude", BackendDefaultName, "", "", "")).String() == "" {
		t.Error("the key of a default-home session renders empty")
	}
}

func pct(v float64) *float64 { return &v }

func TestAccountReadingStates(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	w := []AccountWindow{{Name: "seven_day", UsedPercent: pct(87), ResetsAt: t0.Add(48 * time.Hour)}}

	r := NewAccountReadings()
	if _, st := r.Reading(key, t0); st != ReadingNone {
		t.Fatalf("empty store reads %q, want none", st)
	}
	if !r.Record(key, w, "ws/seat-a", t0) {
		t.Fatal("a usable reading was not stored")
	}
	for _, tc := range []struct {
		age  time.Duration
		want ReadingState
	}{
		{0, ReadingFresh},
		{ReadingMaxAge, ReadingFresh},
		{ReadingMaxAge + time.Second, ReadingStale},
		{16 * time.Minute, ReadingStale},
		{72 * time.Hour, ReadingStale},
	} {
		got, st := r.Reading(key, t0.Add(tc.age))
		if st != tc.want {
			t.Errorf("age %v: state %q, want %q", tc.age, st, tc.want)
		}
		if len(got.Windows) != 1 || got.Windows[0].Name != "seven_day" || got.Session != "ws/seat-a" {
			t.Errorf("age %v: reading %+v lost its windows or provenance", tc.age, got)
		}
	}
}

// Test 20: windows with no percentage are none, never 0%.
func TestAccountReadingWithNoPercentageIsNone(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	r := NewAccountReadings()
	if r.Record(key, []AccountWindow{{Name: "five_hour"}}, "ws/a", t0) {
		t.Fatal("a window with no percentage was stored")
	}
	if _, st := r.Reading(key, t0); st != ReadingNone {
		t.Fatalf("state %q, want none", st)
	}
	if len(r.Keys()) != 0 {
		t.Fatalf("keys = %v, want none", r.Keys())
	}
	// An unusable payload does not erase a good earlier reading.
	r.Record(key, []AccountWindow{{Name: "five_hour", UsedPercent: pct(40)}}, "ws/a", t0)
	r.Record(key, []AccountWindow{{Name: "five_hour"}}, "ws/b", t0.Add(time.Minute))
	got, st := r.Reading(key, t0.Add(2*time.Minute))
	if st != ReadingFresh || got.Session != "ws/a" || *got.Windows[0].UsedPercent != 40 {
		t.Fatalf("earlier reading lost: %q %+v", st, got)
	}
}

func TestAccountReadingDropsUnusableWindowsAndKeepsTheNewest(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	r := NewAccountReadings()
	r.Record(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(10)}, {Name: "five_hour"}}, "ws/a", t0)
	got, _ := r.Reading(key, t0)
	if len(got.Windows) != 1 || got.Windows[0].Name != "seven_day" {
		t.Fatalf("windows = %+v, want only the usable one", got.Windows)
	}
	r.Record(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(55)}}, "ws/b", t0.Add(time.Minute))
	got, _ = r.Reading(key, t0.Add(time.Minute))
	if *got.Windows[0].UsedPercent != 55 || got.Session != "ws/b" {
		t.Fatalf("newest reading not kept: %+v", got)
	}
}

// The store hands out copies: a caller cannot move a stored percentage.
func TestAccountReadingReturnsACopy(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	in := []AccountWindow{{Name: "seven_day", UsedPercent: pct(10)}}
	r := NewAccountReadings()
	r.Record(key, in, "ws/a", t0)
	*in[0].UsedPercent = 99
	got, _ := r.Reading(key, t0)
	*got.Windows[0].UsedPercent = 98
	again, _ := r.Reading(key, t0)
	if *again.Windows[0].UsedPercent != 10 {
		t.Fatalf("stored percentage moved to %v", *again.Windows[0].UsedPercent)
	}
}

func TestAccountKeysAreStable(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	r := NewAccountReadings()
	for _, h := range []string{"codex", "claude", "forestage"} {
		r.Record(AccountKey{Harness: h, Backend: BackendDefaultName}, []AccountWindow{{Name: "w", UsedPercent: pct(1)}}, "ws/a", t0)
	}
	a, b := r.Keys(), r.Keys()
	if len(a) != 3 || !reflect.DeepEqual(a, b) || a[0].Harness != "claude" {
		t.Fatalf("keys = %v then %v, want 3 in a stable sorted order", a, b)
	}
}

// Test 9: the heartbeat RPC still carries no account fields. The reading is
// the account's, not the session's; the two are separate RPCs.
func TestHeartbeatRequestCarriesNoAccountFields(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(reflect.Zero(reflect.TypeOf(HeartbeatRequest{})).Interface())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		for _, banned := range []string{"account", "rate_limit", "window", "used_percent", "resets_at"} {
			if strings.Contains(strings.ToLower(name), banned) {
				t.Errorf("heartbeat field %q looks like an account field", name)
			}
		}
	}
	typ := reflect.TypeOf(HeartbeatRequest{})
	for i := 0; i < typ.NumField(); i++ {
		tag := strings.ToLower(typ.Field(i).Tag.Get("json") + typ.Field(i).Name)
		for _, banned := range []string{"account", "ratelimit", "rate_limit", "usedpercent", "used_percent", "resetsat"} {
			if strings.Contains(tag, banned) {
				t.Errorf("heartbeat field %s looks like an account field", typ.Field(i).Name)
			}
		}
	}
}

// An old client decodes a session record that carries the new field.
func TestSessionWithAccountHomeDecodesInAnOldClient(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Session{Name: "x", AccountHome: "/home/op/.codex"})
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &old); err != nil || old.Name != "x" {
		t.Fatalf("old client decode: %v %+v", err, old)
	}
	var empty map[string]any
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(Session{Name: "x"})
	if strings.Contains(string(plain), "account_home") {
		t.Fatalf("an empty AccountHome is on the wire: %s", plain)
	}
}

func storeWithSession(t *testing.T, hash string) *Store {
	t.Helper()
	st := NewStore()
	sess := Session{Workspace: "ws", Name: "seat", Runtime: Runtime{Name: "claude"}, HeartbeatTokenHash: hash}
	if err := st.CreateSession(&sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	return st
}

func TestAuthenticateAccountReport(t *testing.T) {
	t.Parallel()
	token, hash, err := NewHeartbeatToken()
	if err != nil {
		t.Fatal(err)
	}
	st := storeWithSession(t, hash)
	got, err := st.AuthenticateAccountReport("ws/seat", token)
	if err != nil || got.Key() != "ws/seat" {
		t.Fatalf("matching token: %v %v", got.Key(), err)
	}
	tests := []struct {
		name, key, token string
		want             error
	}{
		{"wrong token", "ws/seat", "nope", ErrHeartbeatUnauthorized},
		{"empty token", "ws/seat", "", ErrHeartbeatUnauthorized},
		{"unknown session", "ws/ghost", token, ErrNotFound},
	}
	for _, tc := range tests {
		if _, err := st.AuthenticateAccountReport(tc.key, tc.token); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	// A record with no hash is refused, even for an empty token, unlike the
	// heartbeat which admits it.
	legacy := storeWithSession(t, "")
	for _, tok := range []string{"", token} {
		if _, err := legacy.AuthenticateAccountReport("ws/seat", tok); !errors.Is(err, ErrAccountReportUnbound) {
			t.Errorf("unbound record, token %q: err = %v, want ErrAccountReportUnbound", tok, err)
		}
	}
}

// Percentages outside what a window can hold are not readings.
func TestAccountRecordRefusesNegativeAndNonFinitePercentages(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for name, v := range map[string]float64{
		"negative":      -0.1,
		"minus one":     -1,
		"not a number":  math.NaN(),
		"infinity":      math.Inf(1),
		"minus infinty": math.Inf(-1),
	} {
		r := NewAccountReadings()
		if r.Record(key, []AccountWindow{{Name: "w", UsedPercent: &v}}, "ws/a", t0) {
			t.Errorf("%s was stored", name)
		}
		if _, st := r.Reading(key, t0); st != ReadingNone {
			t.Errorf("%s: state %q, want none", name, st)
		}
	}
	zero := 0.0
	r := NewAccountReadings()
	if !r.Record(key, []AccountWindow{{Name: "w", UsedPercent: &zero}}, "ws/a", t0) {
		t.Error("zero percent is a reading and was refused")
	}
}

// Naming the harness's default home and naming nothing are one login.
func TestCanonicalConfigHome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, harness, home, user, want string
	}{
		{"empty stays empty", "claude", "", "/h", ""},
		{"claude's default dir", "claude", "/h/.claude", "/h", ""},
		{"with a trailing slash", "claude", "/h/.claude/", "/h", ""},
		{"codex's default dir", "codex", "/h/.codex", "/h", ""},
		{"another harness's default is not this one's", "claude", "/h/.codex", "/h", "/h/.codex"},
		{"a different directory is a different login", "claude", "/h/.claude-work", "/h", "/h/.claude-work"},
		{"a harness with no known default keeps the path", "forestage", "/h/.forestage", "/h", "/h/.forestage"},
		{"no user home known keeps the cleaned path", "claude", "/h/.claude/", "", "/h/.claude"},
	}
	for _, tc := range tests {
		if got := canonicalConfigHomeIn(tc.harness, tc.home, tc.user); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// No t.Parallel: it sets HOME for the process.
func TestAccountKeyTreatsTheExplicitDefaultHomeAsTheDefault(t *testing.T) {
	t.Setenv("HOME", "/home/acct-test")
	shared := sessionOn("codex", BackendDefaultName, "", "", "")
	named := sessionOn("codex", BackendDefaultName, "", "/home/acct-test/.codex", "/state/homes/aaa")
	if AccountKeyOf(shared) != AccountKeyOf(named) {
		t.Fatalf("one login, two keys: %v and %v", AccountKeyOf(shared), AccountKeyOf(named))
	}
}

// A reading older than the one held, by observation time, never replaces it:
// a sender re-posting an hour-old figure cannot overwrite a fresh full window.
func TestAccountRecordKeepsTheNewestObservation(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "codex", Backend: BackendDefaultName}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	r := NewAccountReadings()
	if !r.Record(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(100)}}, "ws/a", t0) {
		t.Fatal("first reading refused")
	}
	if r.Record(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(40)}}, "ws/b", t0.Add(-time.Hour)) {
		t.Error("an older observation was stored")
	}
	got, _ := r.Reading(key, t0)
	if *got.Windows[0].UsedPercent != 100 || got.Session != "ws/a" || !got.At.Equal(t0) {
		t.Fatalf("older observation replaced the newer: %+v", got)
	}
	if !r.Record(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(30)}}, "ws/b", t0) {
		t.Error("an equal-time reading was refused")
	}
	if !r.Record(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(20)}}, "ws/b", t0.Add(time.Minute)) {
		t.Error("a newer reading was refused")
	}
}

func TestAccountLimitsRequestObservedAtIsAdditive(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(AccountLimitsRequest{Session: "ws/a", ObservedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Session      string
		SessionToken string `json:"session_token"`
		Windows      []AccountWindow
	}
	if err := json.Unmarshal(raw, &old); err != nil || old.Session != "ws/a" {
		t.Fatalf("old decode: %+v %v", old, err)
	}
	var back AccountLimitsRequest
	if err := json.Unmarshal([]byte(`{"session":"ws/a","windows":[]}`), &back); err != nil || !back.ObservedAt.IsZero() {
		t.Fatalf("a request without observed_at: %+v %v", back, err)
	}
}

// A reading with no observation time is unknown: it never displaces a stamped
// one, is stored when nothing stamped is held, and an older sender that never
// stamps still gets each new reading through.
func TestAccountRecordUnstampedNeverDisplacesAStampedReading(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	r := NewAccountReadings()
	if !r.RecordUnstamped(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(10)}}, "ws/a", t0) {
		t.Fatal("first unstamped reading refused")
	}
	if !r.RecordUnstamped(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(20)}}, "ws/a", t0.Add(time.Minute)) {
		t.Fatal("a later unstamped reading was refused: an old sender would be frozen")
	}
	if !r.Record(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(100)}}, "ws/b", t0.Add(-time.Hour)) {
		t.Fatal("a stamped reading was refused by an unstamped one")
	}
	if r.RecordUnstamped(key, []AccountWindow{{Name: "seven_day", UsedPercent: pct(40)}}, "ws/c", t0.Add(2*time.Minute)) {
		t.Fatal("an unstamped reading displaced a stamped one")
	}
	got, _ := r.Reading(key, t0.Add(-time.Hour))
	if *got.Windows[0].UsedPercent != 100 || got.Session != "ws/b" {
		t.Fatalf("reading = %+v", got)
	}
	if r.RecordUnstamped(key, nil, "ws/c", t0) {
		t.Fatal("an unusable unstamped reading was stored")
	}
}

// A stamped reading displaces a held unstamped one only when it is fresh at now
// (marvel#551 r3): a stale stamped 40 must not regress an unstamped 100.
func TestAccountRecordStaleStampedDoesNotDisplaceAnUnstampedReading(t *testing.T) {
	t.Parallel()
	key := AccountKey{Harness: "claude", Backend: BackendDefaultName}
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	r := NewAccountReadings()
	r.RecordUnstamped(key, []AccountWindow{{Name: "five_hour", UsedPercent: pct(100)}}, "ws/a", now)
	stale := now.Add(-ReadingMaxAge - time.Minute)
	if r.RecordObserved(key, []AccountWindow{{Name: "five_hour", UsedPercent: pct(40)}}, "ws/b", stale, now) {
		t.Fatal("a stale stamped reading displaced an unstamped one")
	}
	got, _ := r.Reading(key, now)
	if *got.Windows[0].UsedPercent != 100 {
		t.Fatalf("figure regressed to %v", *got.Windows[0].UsedPercent)
	}
	fresh := now.Add(-ReadingMaxAge + time.Minute)
	if !r.RecordObserved(key, []AccountWindow{{Name: "five_hour", UsedPercent: pct(40)}}, "ws/b", fresh, now) {
		t.Fatal("a fresh stamped reading was refused")
	}
	// With nothing held, a stale stamped reading is still stored (it reads stale).
	empty := NewAccountReadings()
	if !empty.RecordObserved(key, []AccountWindow{{Name: "five_hour", UsedPercent: pct(40)}}, "ws/b", stale, now) {
		t.Fatal("a stale reading into an empty store was refused")
	}
}
