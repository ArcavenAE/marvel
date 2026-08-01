# opencode → event vocabulary — mapping and divergences

Verified against real fixtures from opencode 1.18.5 on 2026-07-31
(`testdata/hello.jsonl`, `testdata/tool_call.jsonl`, `testdata/error.jsonl`,
captured live via `opencode run --format json -m <model> <prompt> </dev/null`).
Compares what opencode actually emits with the sketch in
`aae-orc/docs/design/director-envelope-and-adapter-events.md` §3.3.

Every event is a top-level frame `{type, timestamp, sessionID, part}` (the
`error` frame carries `error` instead of `part`). `sessionID` is present on
every line and is propagated as `harness_session_id`. The top-level `type`
uses underscores (`step_start`); the nested `part.type` uses hyphens
(`step-start`); the parser routes on the top-level `type`.

## Verified mappings

| Vendor line (`type`) | part shape | Emits |
|---|---|---|
| `step_start` | `{part.type:step-start}` | `turn.started` |
| `text` | `{part.text,part.time}` | `message.completed{role:assistant}` |
| `tool_use` | `{part.tool,part.callID,part.state}` | `tool.call` (+ `tool.result` if state terminal) |
| `step_finish` | `{part.tokens,part.cost,part.reason}` | `turn.completed` (usage_delta incl. cost) |
| `error` | `{error:{name,data:{message}}}` | `error{kind:name,message}` |

`part.callID` is the tool correlation key → `tool.call.call_id` /
`tool.result.call_id`.

## Divergences from the design sketch

1. **No `session.started` / `session.ended`.** The design sketch said
   "session lifecycle endpoints → session.started/session.ended" — but
   that surface belongs to `serve` mode (the HTTP/SSE server with real
   session endpoints), which is out of scope here. `run --format json`
   one-shot emits step boundaries, not session lifecycle lines. This
   parser maps `step_start`/`step_finish` → `turn.started`/`turn.completed`
   and emits no session event. Inventing one was rejected on the same
   grounds as the codex case: lifecycle events trace to real vendor
   lifecycle lines. The `serve`+`attach` adapter is the follow-on that
   gains real session events (finding-005).

2. **`step_finish` carries cost; it rides `turn.completed`.** Unlike
   codex, opencode reports a cost (`part.cost`, 0 for the free model in
   the fixture). It is set on `TurnData.UsageDelta.Cost` (always present,
   distinguishing "reported 0" from "not reported"). Note the ring's
   turn summary does not currently print cost (only session-ended does),
   so opencode cost is in the event but not yet in `marvel events` output
   — a bridge-summary gap, tracked as deferred, not an adapter concern.

   `step_finish.part.tokens` also carries `total`, `reasoning`, and a
   `cache: {write, read}` sub-object. All three were previously undeclared
   in `ocTokens` and silently dropped; they now ride
   `TurnData.Request` (Total, ReasoningOut, CacheCreationIn, CacheReadIn).

   **`Request.Layout` is `additive` on an assumption, not a
   measurement.** Whether `tokens.input` already subsumes `cache.read` on
   a caching model is unverified: every fixture we hold is the free
   `opencode/deepseek-v4-flash-free` model, where `cache.write` and
   `cache.read` are both 0 and additive and subsumptive are
   indistinguishable. If the assumption is wrong, occupancy over-counts by
   whatever `input` already contains; every class is carried raw, so the
   correction is one `Layout` line and no data is lost.

   **`tokens.total` cannot arbitrate that question, and is not asked to.**
   Measured (finding-007), opencode's total is `input + output +
   reasoning`: `{total 29893, input 29879, output 2, reasoning 12}` with
   `cache` zero. A total defined that way is the same number whether
   `input` subsumes `cache.read` or not, so comparing it against a sum
   that includes the cache classes would report a mismatch equal to those
   classes on every caching turn, under a right assumption as readily as a
   wrong one. `Request.TotalExcludesCache` therefore holds the comparison
   to the `input + output + reasoning` triple, where it does earn its
   keep: a nonzero result means opencode changed what `total` covers.

   The one live signal on the layout itself is
   `RequestUsage.AdditiveConfirmed()`: `input < cache.read` is impossible
   if `input` already contained the cached tokens, so a warm caching turn
   (small `input`, large `cache.read`) confirms the additive reading from
   real data. It is one-sided. Silence proves nothing, because a
   subsumptive `input` is always the larger number. One paid caching-model
   turn settles the question either way.

3. **A tool part carries call and result in one event.** OpenCode's `tool`
   part is a small state machine (`pending` → `running` → `completed`/`error`).
   In `run --format json` the terminal state arrives; the parser emits a
   `tool.call` (with `state.input`) plus, when `state.status` is
   `completed` or `error`, a `tool.result` (`OK = status=="completed"`,
   output from `state.output` or, on error, `state.error`). A non-terminal
   state yields only the `tool.call` (unit-tested).

4. **Permission rejection surfaces as a failed tool.result, not
   `permission.requested`.** Without `--auto`, `opencode run` auto-rejects
   tool-permission requests (observed: stderr "permission requested: bash
   ...; auto-rejecting") and reports the tool with `state.status:error`,
   `state.error:"The user rejected permission..."`. That maps to
   `tool.result{ok:false}`. The blocking `permission.requested` event is a
   `serve`-mode SSE surface, out of scope here. An operator wanting
   autonomous tool execution passes `--auto` through `runtime.args`.

5. **`reasoning` parts are unmapped.** Like the codex/claude thinking
   analog, `type:reasoning` has no v1 event kind and emits
   `error{kind:unmapped}` with `raw` preserved.

6. **`text` maps to `message.completed`, never `message.delta`.** In
   `run --format json` each `text` part is a complete text part (it carries
   `time.start`/`time.end`). Streaming deltas are a `serve`-mode surface.

## Launch shape

The adapter (`internal/runtime/opencode.go`) launches headless opencode as:

```
opencode run --format json <role.args...> '<prompt>' < /dev/null > <fifo>
```

- `< /dev/null` closes stdin so the harness cannot block on the pane tty.
- No `--auto` is injected: auto-reject is the safe default for an
  unattended launch. `--model provider/model` and `--auto` are the
  operator's to set via `runtime.args`.

## Gaps

- **No live model turn against a fully authorized model on this host.** The
  fixtures used the free `opencode/deepseek-v4-flash-free` model; the
  local Bedrock role lacks `bedrock:InvokeModelWithResponseStream`, so a
  Bedrock-Claude smoke `t.Skip`s unless authorized. Event shapes are real;
  a completed (non-rejected) tool turn against a paid model is unconfirmed
  beyond the synthetic `state.status:completed` unit test.
- **`serve` + `attach` SSE surface unimplemented** — the richer session /
  permission / streaming path, deferred per scope.
- **macOS only**, opencode 1.18.5 only.
