package envelope

import (
	"encoding/json"
	"os"
	"path/filepath"
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
