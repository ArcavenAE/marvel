// Package codex will implement the marvel runtime adapter parse layer
// for the Codex CLI invoked with
//
//	codex exec --json
//
// Not implemented in this slice — this file exists so the intended
// shape is visible in the package tree. Design mapping (per
// docs/design/director-envelope-and-adapter-events.md §3.3):
//
//   - task/turn items          → turn.started / turn.completed
//   - exec command begin/end   → tool.call / tool.result
//   - final message            → session.ended (usage attached)
//   - --output-last-message    → cross-check target for session.ended
//
// The finding-065 caveat applies — verify against a real fixture at
// implementation time; the sketch above may drift.
//
// TODO(aae-orc-pw1l follow-on): implement Parser mirroring the
// claudecode package shape.
package codex
