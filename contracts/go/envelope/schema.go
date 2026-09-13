package envelope

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SchemaJSON is the director envelope JSON Schema, embedded so the validator
// needs no filesystem at runtime. It is a generated copy of
// contracts/schema/director-envelope.schema.json (kept in sync by
// `just contracts-gen`; a test guards against drift).
//
//go:embed schema.gen.json
var SchemaJSON []byte

// schemaID matches the $id in the schema, so the compiler resolves the document
// against its own identifier rather than fetching the URL.
const schemaID = "https://schema.arcaven.com/director/envelope/v1"

var compiled = mustCompile()

func mustCompile() *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(SchemaJSON))
	if err != nil {
		panic(fmt.Sprintf("envelope: embedded schema is not valid JSON: %v", err))
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaID, doc); err != nil {
		panic(fmt.Sprintf("envelope: add schema resource: %v", err))
	}
	sch, err := c.Compile(schemaID)
	if err != nil {
		panic(fmt.Sprintf("envelope: compile schema: %v", err))
	}
	return sch
}

// Validate reports whether raw JSON is a valid director envelope. The schema
// enforces the identity and authority shape by pattern and enum, so format
// checks hold without a format-assertion mode (the reason the schema carries
// patterns beside its format annotations).
func Validate(data []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("envelope: decode instance: %w", err)
	}
	if err := compiled.Validate(inst); err != nil {
		return err
	}
	return nil
}
