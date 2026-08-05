# finding-011: statusline JSON side-channel carries interactive CTX% — both feeds, with the denominator

- **Date:** 2026-08-05
- **Status:** captured (empirical probe complete; integration questions remain in aae-orc-7hzb)
- **Probe:** live capture rig against Claude Code v2.1.222 (interactive TUI, haiku, tmux pane)
- **bd:** aae-orc-7hzb (probe), aae-orc-dc1j (the interactive-CTX% question this bears on)

## Summary

Claude Code exposes TWO configurable statusline hooks, and together they
solve the interactive/TUI context-pressure gap (aae-orc-dc1j) more
completely than either capture-pane scraping or the OTEL path:

1. `statusLine` — per-session, event-driven. The command receives a JSON
   payload on stdin whose `context_window` object carries BOTH the
   numerator and the denominator, plus Claude's own percentage.
2. `subagentStatusLine` — one JSON object per refresh tick containing a
   `tasks` array: per-subagent `tokenCount`, `contextWindowSize`, model,
   status, startTime, and a `tokenSamples` time series. No other channel
   (FIFO stream, OTEL, capture-pane) breaks out subagent context at all.

Both were live-verified with a capture rig (`cat`-to-jsonl statusline
commands in a scratch settings file) on 2026-08-05.

## The main feed (`statusLine`)

Measured payload (v2.1.222, mid-session, trimmed to the relevant keys):

```json
{
  "session_id": "68999042-...",
  "model": {"id": "claude-haiku-4-5", "display_name": "Haiku 4.5"},
  "version": "2.1.222",
  "cost": {"total_cost_usd": 0.0424, "total_duration_ms": 38410,
            "total_api_duration_ms": 3996,
            "total_lines_added": 0, "total_lines_removed": 0},
  "context_window": {
    "total_input_tokens": 33296,
    "total_output_tokens": 41,
    "context_window_size": 200000,
    "current_usage": {
      "input_tokens": 10,
      "output_tokens": 41,
      "cache_creation_input_tokens": 33286,
      "cache_read_input_tokens": 0
    },
    "used_percentage": 17,
    "remaining_percentage": 83
  },
  "exceeds_200k_tokens": false
}
```

Three properties matter to marvel:

- **The denominator is exported.** `context_window_size` was the missing
  piece in the OTEL path (finding-008: no harness exports occupancy; the
  denominator had to come from a maintained model table). Here the
  harness states its own window.
- **The figure is raw occupancy, not the normalized until-auto-compact
  readout.** 17% of 200k ≈ 33.3k matches total_input + output exactly, so
  this is the real level, not the TUI's threshold-adjusted indicator that
  capture-pane scraping would have yielded (the finding-005 fidelity
  caveat does not apply).
- **`current_usage` is the accountant's exact input shape.** input +
  cache_creation + cache_read per request is the finding-007 formula, so
  the usage accountant can run unchanged on this feed; alternatively
  `used_percentage` maps 1:1 onto the existing heartbeat RPC contract
  (`context_percent`).

Also present: `session_id`, `transcript_path`, `prompt_id`,
`session_name`, `workspace`, `output_style`, `fast_mode`, `thinking`.
Cost accumulates across the session (`total_cost_usd` grew 0 → 0.0898
over three prompts including a subagent run).

## The subagent feed (`subagentStatusLine`)

Measured payload (two concurrent Explore subagents):

```json
{
  "session_id": "7f8a63b8-...",
  "prompt_id": "a61e15ba-...",
  "columns": 196,
  "tasks": [
    {"id": "ab0bef27d5e77be9f", "type": "local_agent",
     "status": "completed",
     "description": "Count files in statusline-probe directory",
     "startTime": 1785953921872, "model": "claude-haiku-4-5",
     "contextWindowSize": 200000, "tokenCount": 11238,
     "tokenSamples": [0, 0, 11043, 11238, ...],
     "cwd": "/private/tmp/statusline-probe"},
    {"id": "a8843e99a5103f8b5", "...": "second task, same shape"}
  ]
}
```

- **Attribution is built in:** the payload carries the PARENT session's
  `session_id`; tasks are the subagents. The dc1j question "how does a
  subagent payload identify its parent" answers itself.
- `tokenSamples` is a small time series — trend data the events ring
  could never reconstruct.
- Per-task `effort` requires ≥ v2.1.214 and is absent when inherited.
- The main `statusLine` hook does NOT fire per subagent (verified: seven
  payloads across a subagent run, one session_id throughout); the two
  feeds are disjoint by design.

## Cadence and lifecycle (docs, code.claude.com/docs/en/statusline)

- Event-driven, debounced at 300ms; a new trigger cancels the in-flight
  script. Optional `refreshInterval` (min 1s) re-runs on a timer — needed
  if the main session idles while background subagents work.
- Same trust and `disableAllHooks` gates as other hooks.

## What this means for marvel (recommendation)

The claude adapter already constructs the session environment and writes
a projected settings file (finding-024). Adding `statusLine` +
`subagentStatusLine` entries pointing at a marvel-shipped forwarder
gives interactive sessions live CTX% through the EXISTING cooperative
heartbeat path:

- The forwarder inherits `MARVEL_SESSION` from the harness process env
  (adapters set it at spawn), so session attribution costs nothing.
- `used_percentage` → heartbeat `context_percent` directly; or forward
  `current_usage` raw and let the usage accountant derive, keeping one
  derivation path for stream and statusline feeds.
- Subagent rows are new capability, not parity: nothing else meters
  subagent context. Worth a design pass on where they land (per-session
  detail vs a new event kind).

Weigh against OTEL in the aae-orc-mqgf decision brief: statusline is
per-session push with identity built in and no receiver topology to
ratify; OTEL covers Codex/Gemini too. They are complementary — statusline
for claude-interactive CTX% now, OTEL for the cross-harness telemetry
spine.

## Remaining open (tracked in aae-orc-7hzb)

- Composition/precedence when the user has their own statusLine
  configured (projected settings file vs user settings.json merge
  behavior) — the probe used `--settings` which replaces, not merges.
- Which other harnesses have an equivalent hook (codex/opencode/crush) —
  opencode/Crush interactive CTX% remains unsolved per dc1j.
- Forwarder transport: what client surface the heartbeat RPC actually
  offers a shell script (the simulator calls it in-process).

## Cross-references

- aae-orc-dc1j (interactive CTX% question — this narrows it)
- finding-007 (headless accountant formula), finding-008 (OTEL channel,
  live-verified), finding-005 (capture-pane fidelity tier)
- orc finding-024 (policy projection — the injection mechanism)
- Claude Code docs: statusline (fetched 2026-08-05, v2.1.222 behavior)
