# claudecode → event vocabulary — mapping and divergences

Verified against real fixtures from claude 2.1.201 on 2026-07-05
(see `testdata/hello.ndjson` and `testdata/tool_call.ndjson`). Compares
what claude actually emits with the sketch in
`aae-orc/docs/design/director-envelope-and-adapter-events.md` §3.3.

## Verified mappings

| Vendor line | Content shape | Emits |
|---|---|---|
| `type:system, subtype:init` | top-level | `session.started` |
| `type:assistant, message.content:[text]` | one block per event | `message.completed{role:assistant}` |
| `type:assistant, message.content:[tool_use]` | one block per event | `tool.call` |
| `type:user, message.content:[tool_result]` | one block per event | `tool.result` |
| `type:result` | top-level (subtype:success or error_*) | `session.ended` |

`session_id` is present on **every** vendor line and is the
`harness_session_id` propagated onto every emitted event.

## Divergences from the design sketch

1. **No `content_block_delta` observed.** The design sketch says
   `assistant/content_block_delta → message.delta`. With
   `--output-format stream-json --verbose` (no
   `--include-partial-messages`), assistant messages arrive as
   complete `content: [...]` arrays in one line. `message.delta`
   therefore has no source and is never emitted by this parser
   today. Adding `--include-partial-messages` support is follow-on.

2. **`thinking` content blocks exist and are not in v1 vocabulary.**
   Assistant messages can carry `content: [{type:"thinking", ...}]`.
   The v1 event kinds don't include a thinking event. Per §3.2's
   "MUST emit error{kind:unmapped} with raw" rule, this parser emits
   `error{kind:"unmapped"}` for every thinking block, preserving
   the vendor line in `raw`. Follow-on: consider promoting to a
   `message.thinking` first-class kind.

3. **`turn.started` / `turn.completed` are not emitted.** The design
   sketch says "message boundaries → turn.*", but in `-p` mode the
   turn envelope is degenerate: one turn per invocation, delimited
   by the outer system/init and result lines. We emit
   session.started/ended for that scope and reserve turn.* for the
   multi-turn interactive case (`--resume` sessions, or future
   session-server usage).

4. **`init` carries far more fields than the spec calls out.** The
   spec names model/cwd/resumed; claude also ships tools (a big
   list), mcp_servers, slash_commands, agents, skills, plugins,
   memory_paths, permissionMode, output_style, claude_code_version,
   fast_mode_state, apiKeySource, uuid, analytics_disabled,
   product_feedback_disabled. We surface `tools` in
   `SessionStartedData` (useful for downstream capability inspection)
   and drop the rest — they're reachable via raw if needed later.

5. **`result` carries far more fields than the spec calls out.**
   RESOLVED: metering is now the point, so the accounting fields are
   promoted rather than dropped. `SessionEndedData.Metering` (additive,
   nil when a harness reports none of it) carries duration_ms,
   duration_api_ms, ttft_ms, ttft_stream_ms, num_turns, the two
   prompt-cache token counts off `usage`, the per-model `modelUsage`
   breakdown, and `permission_denials`. Cache counts live on Metering
   rather than Usage because Usage's three fields are spec-fixed and
   shared with turn events.

   Still dropped: `result` (the final assistant text, already carried by
   the preceding message.completed event), `terminal_reason`,
   `api_error_status`, `time_to_request_ms`, and the `usage` sub-objects
   (`server_tool_use`, `cache_creation`, `service_tier`, `iterations`).
   All remain reachable via `raw`.

   `permission_denials` entry shape is unverified — every fixture we
   have carries an empty array, so the length is authoritative and
   tool_name/tool_use_id are best-effort.

6. **No timestamp field on vendor lines.** Claude Code stream-json
   does not carry per-event timestamps. The parser stamps `ts` at
   consumption time using the injected clock — good enough for
   monotonic ordering; unsuitable for wall-clock correlation with
   external timelines. Follow-on: if `--include-partial-messages`
   ships timestamps, prefer them over `time.Now()`.

7. **`permission.requested` and `auth.required` not exercised.**
   The `-p --output-format stream-json` mode with `--allowed-tools`
   or `permissionMode:auto` never produced permission prompts in
   our fixtures. permission.requested likely appears in interactive
   sessions; auth.required is derived from stderr, not the primary
   NDJSON stream. Both remain unemitted by this slice — tracking
   the gap.

8. **`is_error` on `result`.** The design sketch says
   `session.ended{exit_code}` but doesn't specify how to derive it
   from claude's stream. We map `result.is_error` → `exit_code`
   (0 or 1). Fine granularity (e.g. non-zero for max-turns aborts)
   isn't available.

9. **Multi-block assistant lines.** The design sketch treats an
   assistant event as one message; the vendor emits arrays of
   blocks (thinking + tool_use + text, in principle — though our
   fixtures show one block per line). The parser emits one event
   per block; if a future line carries multiple blocks, callers
   receive multiple events at the same seq boundary + the same
   session_id.
