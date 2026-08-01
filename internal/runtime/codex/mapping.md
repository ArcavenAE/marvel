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

4. **Cache/reasoning token counts are dropped from the event.** `TurnData`
   carries only the shared `Usage` shape (In, Out, Cost). `cached_input_tokens`,
   `cache_write_input_tokens`, and `reasoning_output_tokens` are not
   surfaced. They remain in `raw` if a consumer needs them; promoting them
   would parallel claudecode's `Metering` follow-on.

5. **`reasoning` items are unmapped.** Codex emits
   `item.completed{type:reasoning}` (the thinking analog). There is no v1
   event kind for it, so per the never-drop rule this parser emits
   `error{kind:unmapped}` with the vendor line preserved in `raw`. Not
   observed in the trivial fixtures, but handled and unit-tested.

6. **Non-command tool items are unmapped, not guessed.** `command_execution`
   is the only tool item verified against a live fixture. `mcp_tool_call`,
   `web_search`, `file_change`, and `todo_list` are documented codex item
   types but their field shapes are unverified here, so they emit
   `error{kind:unmapped}` with `raw` rather than a speculative mapping.
   Follow-on: capture fixtures and promote each to `tool.call`/`tool.result`.

7. **No per-event timestamps.** Codex JSONL lines carry no timestamp; the
   parser stamps `ts` at consumption time via the injected clock. Good for
   monotonic ordering, not for wall-clock correlation.

8. **`item.started` is surfaced only for tool items.** `agent_message`
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

## Gaps

- **Not exercised across multiple turns.** `exec` one-shot is single-turn;
  `codex exec resume` and multi-turn behavior are unverified.
- **`turn.failed` mapped from documentation, not a fixture.** It emits
  `error{kind:vendor}` with a best-effort message; the exact failure shape
  is unconfirmed.
- **macOS only**, codex-cli 0.146.0 only. The event dialect has changed
  across codex versions; re-verify on upgrade.
