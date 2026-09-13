package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

const (
	canonicalSchema = "../../schema/director-event.schema.json"
	fixturesDir     = "../../schema/testdata/event"
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
		t.Fatal("schema.gen.json drifted from contracts/schema/director-event.schema.json; run `just contracts-gen`")
	}
}

// The schema's event enum and the events.AllKinds constants must be the same
// set, both ways: a kind in Go but not the schema, or in the schema but not Go,
// is drift the wire twin exists to prevent. events.go is the source of truth;
// this fails the schema, not Go.
func TestKindEnumMatchesGoConstants(t *testing.T) {
	var doc struct {
		Properties struct {
			Event struct {
				Enum []string `json:"enum"`
			} `json:"event"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(SchemaJSON, &doc); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	schemaKinds := doc.Properties.Event.Enum
	goKinds := make([]string, len(events.AllKinds))
	for i, k := range events.AllKinds {
		goKinds[i] = string(k)
	}
	sort.Strings(schemaKinds)
	sort.Strings(goKinds)
	if len(schemaKinds) != len(goKinds) {
		t.Fatalf("kind count: schema has %d, events.AllKinds has %d", len(schemaKinds), len(goKinds))
	}
	for i := range goKinds {
		if schemaKinds[i] != goKinds[i] {
			t.Errorf("kind set diverges at %d: schema %q vs Go %q", i, schemaKinds[i], goKinds[i])
		}
	}
}

// Every valid fixture validates, decodes into the Go events.Event source type,
// and re-validates after a marshal round-trip. The per-kind fixtures plus the
// 64 KiB truncation-boundary frame exercise the whole vocabulary through the
// same type the adapters emit.
func TestValidFixturesRoundTrip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixturesDir, "valid-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no valid fixtures found (%v)", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		name := filepath.Base(f)
		if err := Validate(data); err != nil {
			t.Errorf("%s: expected valid, got: %v", name, err)
			continue
		}
		var e events.Event
		if err := json.Unmarshal(data, &e); err != nil {
			t.Errorf("%s: unmarshal into events.Event: %v", name, err)
			continue
		}
		out, err := json.Marshal(e)
		if err != nil {
			t.Errorf("%s: marshal events.Event: %v", name, err)
			continue
		}
		if err := Validate(out); err != nil {
			t.Errorf("%s: re-validate after round-trip: %v", name, err)
		}
	}
}

// There must be a valid round-trip fixture for every kind, so the round-trip
// test above actually covers the whole vocabulary rather than a subset.
func TestEveryKindHasAValidFixture(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixturesDir, "valid-*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var frame struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("parse %s: %v", filepath.Base(f), err)
		}
		seen[frame.Event] = true
	}
	for _, k := range events.AllKinds {
		if !seen[string(k)] {
			t.Errorf("no valid fixture for kind %q", k)
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
