// Package opencode will implement the marvel runtime adapter for
// OpenCode's `serve` mode (HTTP/SSE via the Go SDK).
//
// Not implemented in this slice — this file exists so the intended
// shape is visible in the package tree. Design mapping (per
// docs/design/director-envelope-and-adapter-events.md §3.3):
//
//   - session lifecycle endpoints  → session.started / session.ended
//   - SSE message parts            → message.delta / message.completed
//   - SSE tool parts               → tool.call / tool.result
//   - permission asks              → permission.requested (blocking!)
//
// The finding-065 caveat applies — verify against a real event stream
// at implementation time; the sketch above may drift.
//
// TODO(aae-orc-pw1l follow-on): implement Parser + SSE consumer.
package opencode
