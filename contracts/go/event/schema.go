package event

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SchemaJSON is the adapter-event JSON Schema, embedded so the validator needs
// no filesystem at runtime. It is a generated copy of
// contracts/schema/director-event.schema.json (kept in sync by
// `just contracts-gen`; a test guards against drift).
//
//go:embed schema.gen.json
var SchemaJSON []byte

// schemaID matches the $id in the schema, so the compiler resolves the document
// against its own identifier rather than fetching the URL.
const schemaID = "https://schema.arcaven.com/marvel/adapter-event/v1"

var compiled = mustCompile()

func mustCompile() *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(SchemaJSON))
	if err != nil {
		panic(fmt.Sprintf("event: embedded schema is not valid JSON: %v", err))
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaID, doc); err != nil {
		panic(fmt.Sprintf("event: add schema resource: %v", err))
	}
	sch, err := c.Compile(schemaID)
	if err != nil {
		panic(fmt.Sprintf("event: compile schema: %v", err))
	}
	return sch
}

// Validate reports whether raw JSON is a valid adapter event frame. The kind
// enum, the closed frame, and the per-kind data shape are enforced by the
// schema; the pattern on ts holds without a format-assertion mode.
func Validate(data []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("event: decode instance: %w", err)
	}
	if err := compiled.Validate(inst); err != nil {
		return err
	}
	return nil
}
