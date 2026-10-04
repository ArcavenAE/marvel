package api

import (
	"encoding/json"
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
