# opencode → event vocabulary — mapping and divergences

Verified against real fixtures from opencode 1.18.5 on 2026-07-31
(`testdata/hello.jsonl`, `testdata/tool_call.jsonl`, `testdata/error.jsonl`)
and from opencode 1.18.15 on 2026-08-08 (`testdata/caching_first_turn.jsonl`,
`testdata/caching.jsonl`, turns 1 and 2 of one session), all captured live
via `opencode run --format json -m <model> <prompt> </dev/null`.
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

   **`Request.Layout` is `additive`, measured.** Settled 2026-08-08
   against 215 real `step-finish` rows on this host (opencode's local
   store, four models across `opencode` and `ollama` providers) plus a
   fresh live two-turn capture on opencode 1.18.15. 179 caching rows carry
   `input` BELOW `cache.read`, down to `{input 1, cache.read 35584}`, and
   `input` cannot be smaller than a set it contains. Occupancy is
   `input + cache.read + cache.write`.
   `RequestUsage.AdditiveConfirmed()` is the in-band signal and remains
   one-sided: it fires on a warm turn, and silence still proves nothing
   because a subsumptive `input` would always be the larger number.
   `testdata/caching.jsonl` is the confirming turn;
   `testdata/caching_first_turn.jsonl` is a caching turn from the same
   session where `input` (6018) exceeds `cache.read` (1920) and so cannot
   confirm anything.

   **`tokens.total` covers the cache classes**, which the earlier reading
   had backwards. All 215 rows satisfy `total == input + output +
   reasoning + cache.read + cache.write`; only 17 satisfy the narrower
   `input + output + reasoning`, and those 17 are exactly the 17
   non-caching rows, where the two identities are the same arithmetic.
   finding-007's `{total 29893, input 29879, output 2, reasoning 12}` is
   one of those non-caching rows, so it never distinguished the two
   readings. `Request.TotalExcludesCache` is therefore NOT set: setting it
   held the comparison to the narrow triple and so suppressed
   `TotalMismatch` by the whole cache read on every caching turn, which is
   precisely where the check has something to say.

   Residual gap: `cache.write` was 0 on all 215 rows and on both live
   turns, so its share of `total` is inferred from `cache.read` rather
   than measured. A cache-creation turn settles it. Until one arrives the
   declaration errs toward a check that can raise a false alarm (bounded
   by `cache.write`) over one that cannot speak at all.

   **`Cumulation` is `request`, measured.** The two live turns are one
   session: `input` falls 6018 → 27 as the prompt moves into cache, and
   across the local store six consecutive step-finish pairs show `total`
   or `input` decreasing. A running session total cannot fall.

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
- **No observed `cache.write`.** Zero on all 215 local-store rows and on
  both live turns, so its participation in `tokens.total` is inferred, not
  measured. See the residual note under divergence 2.
- **`serve` + `attach` SSE surface unimplemented** — the richer session /
  permission / streaming path, deferred per scope.
- **macOS only**, opencode 1.18.5 and 1.18.15 only.
