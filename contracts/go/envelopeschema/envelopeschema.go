// Package envelopeschema holds the director envelope JSON Schema as bytes and
// nothing else. A reader that needs one value from the schema imports this
// leaf instead of contracts/go/envelope, whose validator compiles the whole
// schema with a JSON Schema library at init.
//
// schema.gen.json is a generated copy of
// contracts/schema/director-envelope.schema.json, written by
// `just contracts-gen`; the envelope package's tests guard it against drift.
package envelopeschema

import _ "embed"

// JSON is the director envelope JSON Schema.
//
//go:embed schema.gen.json
var JSON []byte
