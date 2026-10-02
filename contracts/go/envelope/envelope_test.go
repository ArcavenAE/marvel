package envelope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canonical paths, relative to this package dir (contracts/go/envelope).
const (
	canonicalSchema = "../../schema/director-envelope.schema.json"
	fixturesDir     = "../../schema/testdata"
)

// The embedded copy the validator compiles must match the canonical schema, so
// a schema edit that skips `just contracts-gen` fails CI rather than shipping a
// stale validator.
func TestEmbeddedSchemaMatchesCanonical(t *testing.T) {
	canonical, err := os.ReadFile(canonicalSchema)
	if err != nil {
		t.Fatalf("read canonical schema: %v", err)
	}
	if string(canonical) != string(SchemaJSON) {
		t.Fatal("schema.gen.json drifted from contracts/schema/director-envelope.schema.json; run `just contracts-gen`")
	}
}

func TestValidFixturesValidate(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixturesDir, "valid-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no valid fixtures found (%v)", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if err := Validate(data); err != nil {
			t.Errorf("%s: expected valid, got: %v", filepath.Base(f), err)
		}
	}
}

func TestInvalidFixturesRejected(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixturesDir, "invalid-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no invalid fixtures found (%v)", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if err := Validate(data); err == nil {
			t.Errorf("%s: expected rejection, but validation passed", filepath.Base(f))
		}
	}
}

// The bad sender.instance fixtures must be refused by the field's own pattern,
// not by the closed sender object refusing an unknown key, or they would prove
// nothing once the field exists (director#196 step 1, marvel#446).
func TestInvalidInstanceRejectedByPattern(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixturesDir, "invalid-instance-*.json"))
	if err != nil || len(files) != 4 {
		t.Fatalf("want 4 invalid-instance fixtures, found %d (%v)", len(files), err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		err = Validate(data)
		if err == nil {
			t.Errorf("%s: expected rejection, but validation passed", filepath.Base(f))
			continue
		}
		if !strings.Contains(err.Error(), "/sender/instance") {
			t.Errorf("%s: rejected, but not at /sender/instance: %v", filepath.Base(f), err)
		}
	}
}

// The global tier addresses its envelopes global://director and
// global://{cluster}/supervisor (marvel#457). A global envelope that is
// otherwise valid must validate, and a malformed global address must be refused
// by recipient.address itself, not by some other field.
func TestGlobalRecipientAddresses(t *testing.T) {
	for _, name := range []string{"valid-global-director.json", "valid-global-cluster-supervisor.json"} {
		data, err := os.ReadFile(filepath.Join(fixturesDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := Validate(data); err != nil {
			t.Errorf("%s: expected valid, got: %v", name, err)
		}
	}
	files, err := filepath.Glob(filepath.Join(fixturesDir, "invalid-global-*.json"))
	if err != nil || len(files) != 6 {
		t.Fatalf("want 6 invalid-global fixtures, found %d (%v)", len(files), err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		err = Validate(data)
		if err == nil {
			t.Errorf("%s: expected rejection, but validation passed", filepath.Base(f))
			continue
		}
		if !strings.Contains(err.Error(), "/recipient/address") {
			t.Errorf("%s: rejected, but not at /recipient/address: %v", filepath.Base(f), err)
		}
	}
}

// The generated types decode a real envelope, the typed enums carry the wire
// values, and a marshal of the result still validates.
func TestRoundTrip(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixturesDir, "valid-principal-null.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal into Envelope: %v", err)
	}
	if e.Performative != PerformativeREQUEST {
		t.Errorf("performative: got %q, want REQUEST", e.Performative)
	}
	if e.Authority.Strength != StrengthDirect {
		t.Errorf("authority.strength: got %q, want direct", e.Authority.Strength)
	}
	if e.Content.Type != TypeTask {
		t.Errorf("content.type: got %q, want task", e.Content.Type)
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal Envelope: %v", err)
	}
	if err := Validate(out); err != nil {
		t.Errorf("re-validate after round-trip: %v", err)
	}
}

// Absent authority means none (director#197, marvel#448). An envelope without
// it decodes, and re-marshals without inventing one: a zero-value block would
// emit {"strength":""}, which the schema refuses.
func TestRoundTripWithoutAuthority(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixturesDir, "valid-authority-absent.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal an envelope with no authority: %v", err)
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal Envelope: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatal(err)
	}
	if a, ok := fields["authority"]; ok {
		t.Errorf("absent authority re-marshals as %s, want it omitted", a)
	}
	if err := Validate(out); err != nil {
		t.Errorf("re-validate after round-trip: %v", err)
	}
}

// A reader treats a missing authority block exactly as strength none, and a
// present one as stated.
func TestEffectiveAuthority(t *testing.T) {
	cases := map[string]Strength{
		"valid-authority-absent.json": StrengthNone,
		"valid-authority-none.json":   StrengthNone,
		"valid-relayed-seat.json":     StrengthRelayed,
	}
	for name, want := range cases {
		data, err := os.ReadFile(filepath.Join(fixturesDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var e Envelope
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		if got := e.EffectiveAuthority().Strength; got != want {
			t.Errorf("%s: EffectiveAuthority().Strength = %q, want %q", name, got, want)
		}
	}
	var absent Envelope
	if a := absent.EffectiveAuthority(); a.Seat != nil {
		t.Errorf("absent authority has seat %v, want none", a.Seat)
	}
}
