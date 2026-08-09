# codex → event vocabulary — mapping and divergences

Verified against real fixtures from codex-cli 0.146.0 on 2026-07-31
(`testdata/hello.jsonl`, `testdata/tool_call.jsonl`, captured live via
`codex exec --json --skip-git-repo-check -s read-only <prompt> </dev/null`).
Compares what codex actually emits with the sketch in
`aae-orc/docs/design/director-envelope-and-adapter-events.md` §3.3.

## Verified mappings

| Vendor line | Content shape | Emits |
|---|---|---|
| `thread.started` | `{thread_id}` | `session.started` (thread_id → harness_session_id) |
| `turn.started` | top-level | `turn.started` |
| `item.completed` type `agent_message` | `{text}` | `message.completed{role:assistant}` |
| `item.started` type `command_execution` | `{id,command,status:in_progress}` | `tool.call` |
| `item.completed` type `command_execution` | `{id,command,aggregated_output,exit_code,status}` | `tool.result` |
| `turn.completed` | `{usage:{input_tokens,output_tokens,...}}` | `turn.completed` (usage_delta) |

`item.id` is the tool-call correlation key: the `item.started` /
`item.completed` pair for one `command_execution` shares it, and it becomes
`tool.call.call_id` / `tool.result.call_id`.

## Divergences from the design sketch

1. **No `session.ended` event.** The design sketch said "final message →
   session.ended (usage attached)". Codex `exec --json` has no thread-end
   line: the stream terminates at `turn.completed` and then EOF. Marvel
   observes the session's end from the closed stream (process exit); the
   accounting rides `turn.completed` as `turn.completed{usage_delta}`, not
   a `session.ended`. Emitting a synthetic `session.ended` was rejected as
   inventing an event the harness never sent (the codebase ties lifecycle
   events to real vendor lifecycle lines — cf. claudecode `system/init` →
   `session.started`).

2. **`turn.*` are first-class here, unlike claudecode.** claudecode drops
   turn events (claude `-p` emits none); codex emits genuine
   `turn.started` / `turn.completed`, so this parser maps them straight
   through. In one-shot `exec` there is exactly one turn.

3. **Codex reports no cost.** `turn.completed.usage` carries token counts
   (`input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`,
   `output_tokens`, `reasoning_output_tokens`) but no cost field.
   `Usage.Cost` therefore stays nil for this harness. Per finding-005,
   codex cost is reachable only from the rollout files
   (`~/.codex/sessions/.../rollout-*.jsonl`), not the JSONL event stream.

4. **Cache/reasoning token counts now ride `TurnData.Request`.** RESOLVED.
   `TurnData.UsageDelta` still carries only the shared `Usage` shape (In,
   Out, Cost), whose three fields are spec-fixed. The additive
   `TurnData.Request` field carries the rest: `cached_input_tokens` as
   CacheReadIn, `cache_write_input_tokens` as CacheCreationIn, and
   `reasoning_output_tokens` as ReasoningOut.

   `Request.Layout` is `subsumptive` for this harness: `input_tokens`
   already contains `cached_input_tokens`, so summing the cache class
   would double-count. Consumers must call `RequestUsage.Occupancy()`
   rather than summing fields.

   The evidence is the window bound, not the 13992-with-11008-cached row
   this section used to cite. That row is the shape an additive harness
   with a large new prompt also produces, which is why
   `AdditiveConfirmed` is one-sided. Across 2081 per-request records in
   codex's own rollout files, against the 258400 window codex declares
   beside them: `input_tokens` alone peaks at 93.8% of the window and
   never exceeds it, while `input + cached + cache_write` exceeds it on
   801 records and reaches 186.6%. A harness cannot hold 186% of its
   context window.

   `turn.completed` publishes no `total_tokens`, so `Request.Total` stays
   0 and the `TotalMismatch` arithmetic check is disabled here. Leave it
   that way: codex's `reasoning_output_tokens` is a SUBSET of
   `output_tokens`, so the shared `In + Out + ReasoningOut` sum would
   double-count reasoning. Measured over the 1665 rollout records with
   nonzero reasoning, `total == input + output` holds on all 1665 and
   `total == input + output + reasoning` on none.

5. **`turn.completed.usage` is a RUNNING TOTAL, not a level.** SETTLED,
   and it corrects the previous entry here.

   `tool_call.jsonl` reports `input_tokens` 28110 / `cached` 24064 /
   `output` 76 for thread `019fba87-d036-7ae1-a20e-7187ef8e3329`. Codex's
   own rollout for that thread holds two model requests:
   `last_token_usage.input_tokens` 14005 then 14105, and
   `total_token_usage` 14005 then 28110 with cached 11008 then 24064. The
   exec stream matches `total_token_usage` field for field. The prompt at
   turn end was 14105.

   One turn was enough because that turn made two model requests, a tool
   call and an answer. The open question was posed as "a level or a total
   ACROSS TURNS", which is why a fixture already in the repo went unread
   for the answer.

   Consequence, enforced in `internal/usage`: codex samples feed spend by
   replacement and produce no occupancy, so a codex session renders CTX%
   as "-" and `runtime.context_window` does not change that. Occupancy
   for codex needs the rollout's own per-request record, which is the
   channel hook payloads point at through `transcript_path`
   (`agent_transcript_path` exists only on SubagentStop and names a
   subagent's file). That reader is now built: see "The rollout file"
   below, `rollout.go`, and `marvel codex-ctx`.

   SETTLED 2026-08-09: the accumulator is SESSION-scoped. The rollout
   carries `total_token_usage` beside every level, and a session
   accumulator can never decrease. Across the corpus: 1890
   consecutive-pair comparisons and 159 turn boundaries with a sample on
   both sides, from 9 multi-turn sessions of up to 45 turns, with ZERO
   decreases and no record where total falls back to last. A per-turn
   accumulator drops at every boundary. The one step left is whether
   `turn.completed` keeps mirroring `total_token_usage` ACROSS a turn
   boundary, which the single-turn fixture cannot show and an
   authenticated multi-turn `codex exec resume` would. The fold treats
   both scopes alike since neither is a level.

   Not read anywhere: the rollout file's
   `rate_limits.primary.used_percent` (observed 95.0) is the WEEKLY PLAN
   budget, not context occupancy. Nothing in the context accountant reads
   it; recorded here so nobody wires it to CTX%.

6. **`reasoning` items are unmapped.** Codex emits
   `item.completed{type:reasoning}` (the thinking analog). There is no v1
   event kind for it, so per the never-drop rule this parser emits
   `error{kind:unmapped}` with the vendor line preserved in `raw`. Not
   observed in the trivial fixtures, but handled and unit-tested.

7. **Non-command tool items are unmapped, not guessed.** `command_execution`
   is the only tool item verified against a live fixture. `mcp_tool_call`,
   `web_search`, `file_change`, and `todo_list` are documented codex item
   types but their field shapes are unverified here, so they emit
   `error{kind:unmapped}` with `raw` rather than a speculative mapping.
   Follow-on: capture fixtures and promote each to `tool.call`/`tool.result`.

8. **No per-event timestamps.** Codex JSONL lines carry no timestamp; the
   parser stamps `ts` at consumption time via the injected clock. Good for
   monotonic ordering, not for wall-clock correlation.

9. **`item.started` is surfaced only for tool items.** `agent_message`
   arrives solely as `item.completed`; a hypothetical `item.started`
   agent_message carries no content and is skipped. Only tool-like items
   have a meaningful start (the in-progress command).

## Launch shape

The adapter (`internal/runtime/codex.go`) launches headless codex as:

```
codex exec --json --skip-git-repo-check <role.args...> '<prompt>' < /dev/null > <fifo>
```

- `--skip-git-repo-check` keeps codex from refusing a non-repo workspace.
- `< /dev/null` is load-bearing: `codex exec` appends piped stdin to its
  prompt and blocks forever on the pane tty otherwise. The redirect is
  applied even when marvel is not observing (no sink), because the hang is
  a correctness bug independent of observation.
- Sandbox (`-s`) and approval policy are the operator's to set through
  `runtime.args`; the adapter injects neither, to avoid widening a
  harness's authority by default.

## The rollout file: the only occupancy source

`rollout.go` reads a second codex artifact, and the reason it exists at
all is that the stream above cannot answer the question. `turn.completed`
carries codex's `total_token_usage` accumulator field for field, so it is
a running total rather than a level (finding-017 §4). The rollout JSONL
codex writes per session carries both, side by side:

| field | what it is | marvel |
|---|---|---|
| `payload.info.last_token_usage.input_tokens` | the prompt for that request | **the level** |
| `payload.info.total_token_usage.*` | running totals over the session | never read |
| `payload.info.model_context_window` | the window, already effective | the denominator |

Records to discard, both measured across 209 files and 2098 samples:

- `payload.info == null`, which codex writes once at a session's first
  `token_count` (1 occurrence).
- `last_token_usage.input_tokens == 0` with a nonzero `total_tokens`: the
  sentinel codex writes at every compaction (16 occurrences). Reading it
  as a level reports 0% at the moment the session is fullest.

The reader takes the file's path from a hook payload's `transcript_path`
and never derives it. It reads a tail that grows on a miss (64KB, 256KB,
1MB, 4MB) rather than a fixed window, because the largest single record
is 1,776,484 bytes and the largest gap between consecutive samples is
1,792,084, so a fixed window can land inside one tool output and see
nothing. At rest the first rung is ample: across the 207 files carrying
samples, the newest usable record began at most 9,909 bytes from EOF.

`session_meta.payload.context_window` is NOT a size. It is the one-key
object `{"window_id": ...}`, which is compaction-generation identity.

## Gaps

- **Not exercised across multiple turns.** `exec` one-shot is single-turn;
  `codex exec resume` and multi-turn behavior are unverified.
- **`turn.failed` mapped from documentation, not a fixture.** It emits
  `error{kind:vendor}` with a best-effort message; the exact failure shape
  is unconfirmed.
- **macOS only**, codex-cli 0.146.0 only. The event dialect has changed
  across codex versions; re-verify on upgrade.
- **`.zst` rollout compression is untested under a live reader.** The
  binary carries a `codex.rollout_compression.*` metric family, the
  string `jsonl.zst`, and "compressed rollout reader is busy", so
  compression is a background job rather than archive-on-demand. No
  `.zst` file exists on the measured host, so what a reader sees when its
  file is replaced by a compressed sibling mid-session is unknown.
  `ReadOccupancy` opens by path per call and holds no handle, so the
  failure mode is a hold rather than a stale read, but that is reasoning
  and not a measurement.
- **Whether `SessionStart source:"compact"` opens a NEW rollout file** is
  unverified. It would matter to any reader that tracked `(dev,ino)`;
  this one does not, because it takes the path from the hook payload on
  every fire.
