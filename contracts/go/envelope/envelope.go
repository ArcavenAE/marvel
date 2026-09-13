// Package envelope holds the generated Go types and the validator for the
// director envelope contract (bd aae-orc-b69n).
//
// The single source of truth is the JSON Schema at
// contracts/schema/director-envelope.schema.json. Both envelope.gen.go (the
// types) and schema.gen.json (the embedded copy the validator compiles) are
// generated from it by `just contracts-gen`; do not edit them by hand. The Rust
// consumer (beadle) generates from the same schema, so the two cannot drift.
package envelope

// Envelope is the director envelope. It aliases the generated root type so
// callers write envelope.Envelope rather than the generator's derived name. The
// alias is the one hand-written type name here; everything else is generated.
type Envelope = DirectorEnvelopeSchemaJson
