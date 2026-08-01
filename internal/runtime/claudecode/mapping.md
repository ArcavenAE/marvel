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

3. **`turn.completed` IS emitted; `turn.started` is not.** REVISED. This
   parser previously emitted neither, on the grounds that the `-p` turn
   envelope is degenerate (one invocation, delimited by system/init and
   result). That reasoning does not survive contact with context
   accounting: every `assistant` line carries a `message.usage` object
   with the three prompt-token classes for the request that produced it,
   and that is the only LIVE context-occupancy signal this harness gives.
   The result line's totals arrive after the session is over.

   So each assistant line's usage now rides a `turn.completed` carrying
   `TurnData.Request`. The harness itself agrees an API request is a
   turn: `result.num_turns` is 3 on `testdata/tool_call.ndjson`, against
   three distinct `message.id` values.

   Emission is deduped on `message.id`, not per line: one API response is
   split into one vendor line per content block, so the six assistant
   lines in the tool-call fixture repeat three ids and produce three
   events. `usage` is decoded as a pointer because `user` and
   `tool_result` lines carry `"usage": null`; emitting a zero for those
   would land in the occupancy series and read as a compaction on every
   tool result.

   `turn.started` still has no source. Rejected alternatives: attaching
   usage to `MessageData` (usage is not message content, it repeats per
   block, and a thinking-only line maps to `error{unmapped}`, so the
   usage would ride an error event); adding a thirteenth event kind.

   **Subagent lines are marked, not merged.** Every assistant line carries
   a top-level `parent_tool_use_id`, null for the main agent in both
   fixtures and set to the `Task` tool's id for a subagent turn. A
   subagent fills its own context window with a much smaller prompt, so
   folding its usage into the session's occupancy level would drop the
   reading for the length of the tool call and read as a compaction on the
   way in and out. The field rides `Request.ParentToolUseID` rather than
   suppressing the event, because the subagent's tokens are real spend:
   the accountant bills them and excludes them from occupancy, the same
   split it already applies to a non-primary model. Not measured here (no
   captured fixture carries a subagent turn); the null field in both
   fixtures is what names the case.

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

   `modelUsage` entries also carry `contextWindow` and `maxOutputTokens`,
   both now surfaced on `events.ModelUsage`. `contextWindow` is the
   authoritative denominator for context occupancy, and it must be
   selected by the init model (`system/init.model`), never by ranging the
   map: a session that routes across models carries one entry per model
   with different windows (200000 for haiku and 1000000 for
   claude-fable-5[1m] in both fixtures), and Go map iteration order is
   randomized. Note that `system/init.model` and the `modelUsage` key
   carry the `[1m]` suffix while per-request `message.model` does not, so
   the two names are not interchangeable for a window lookup.

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
