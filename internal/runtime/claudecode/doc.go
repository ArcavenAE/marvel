// Package claudecode implements the marvel runtime adapter parse layer
// for Claude Code invoked with
//
//	claude -p --output-format stream-json --verbose
//
// This slice ships the parser only — the io.Reader-driven translation
// from vendor NDJSON to the normalized event vocabulary in
// internal/runtime/events. Process spawning, tmux attachment, stdin
// injection, and the full Instance implementation are follow-on work.
//
// The mapping sketch in aae-orc/docs/design/director-envelope-and-adapter-events.md
// §3.3 was verified against real fixtures from claude 2.1.201 (see
// testdata/); divergences from that sketch are documented in
// mapping.md alongside this package.
package claudecode
