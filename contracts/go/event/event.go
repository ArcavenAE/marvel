// Package event holds the validator for the marvel adapter event vocabulary
// wire twin (bd aae-orc-b69n).
//
// Unlike the envelope contract, there is NO generated Go type here. The Go
// source of truth for the event vocabulary is internal/runtime/events
// (events.go), which carries behavior a schema cannot express (Occupancy and
// the additive/subsumptive Layout, TotalMismatch, AdditiveConfirmed, the
// SeqAssigner, and the 64 KiB Truncate discipline). This package embeds the
// JSON Schema twin of that vocabulary and exposes Validate, so a Rust consumer
// (beadle) generates types from the same schema and cannot drift from the Go
// emitter. Per the architect ruling 2026-09-13 the source of truth flips to
// schema-first only on a second producer of these events, never a consumer.
//
// schema.gen.json is a package-local copy of the canonical schema at
// contracts/schema/director-event.schema.json (go:embed cannot reach
// ../../schema); `just contracts-gen` refreshes it and a test guards it
// against drift.
package event
