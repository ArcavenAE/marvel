# goose stream-json to event vocabulary: mapping and divergences

Verified against the recordings in `internal/runtime/testdata/goose-arm2`
(goose v1.57.0, `goose run --output-format stream-json`, local Ollama with
qwen3:0.6b and qwen3:8b; runs R1 to R5 and R9 to R11, see that README). Bedrock
streaming, usage and cost are not recorded, so nothing here claims them.

Each line is one JSON object with a top-level `type`. A `message` line is one
chunk of an assistant or user message: a run streams hundreds to tens of
thousands of them. Blank lines are skipped, and so is any line before the first
JSON object that does not start with `{` (the banner goose prints despite `-q`).
After the first object, a line that does not start with `{` is
`error{kind:"parse"}` with the line bounded, and so is a line that starts with
`{` and does not decode.

## Verified mappings

| Vendor line | Content item or field | Emits |
|---|---|---|
| `message` | `text` | `message.delta{role,text}` |
| `message` | `thinking` | nothing (see divergence 1) |
| `message` | `toolRequest{id,toolCall.value.name,arguments}` | `tool.call{tool,call_id,input}` |
| `message` | `toolResponse{id,toolResult}` | `tool.result{call_id,ok,output}` (see C4 below) |
| `message` | `error{kind,message}` | nothing (the `error` line says it, divergence 2) |
| `message` | `metadata.outputTokenLimitReached` true | `error{kind:"output-limit"}`, once per run (divergence 3) |
| `complete` | totals | `session.ended{exit_code:0,usage,metering}` |
| `error` | `{error:"..."}` | `error{kind:"vendor",message}`, the raw line kept when within 64 KiB |

One `message` line can carry many content items (R9: nine parallel
`toolRequest` items on one line, nine `toolResponse` items on the next). Items
emit in order, and a call and its response match by id, never by position.

## What ran comes from tool results only (v1.1 C4)

A call counts as run only when its `toolResponse` has `toolResult.status`
"success" AND `toolResult.value.isError` false. Anything else is a response
that did not run: `tool.result{ok:false}`, and an entry in
`session.ended` `Metering.PermissionDenials` with the tool taken from the
matching `toolRequest` and the call id.

- goose has no denial type of its own. R3's refusal is a `toolResponse` whose
  status is "success" and whose `isError` is true, with the decline text in
  `value.content`. The parser never reads that text to decide: a text match is
  one sample from one goose version.
- For goose, `PermissionDenials` therefore means not-ran responses, and it
  includes tools that failed as well as tools that were declined. That
  over-counts on the safe side.
- `toolCall.status` "success" means the request was well formed, not that the
  tool ran. A `toolRequest` that is not well formed (no id, no name, a status
  other than "success") is `error{kind:"unmapped"}` with the raw line, not a
  `tool.call`. A response that later names its id still gets `tool.result` and a
  denial entry by the rule above, with the tool name when the request carried one
  and an empty name when it did not.
- A request with no response by `complete` is not-ran: `tool.call` stays, there
  is no `tool.result`, and it is not listed.
- The exit code and the model's text never decide what ran. R3 exited 0 and the
  model said the command had run.
- `complete` means the run finished, not that every tool ran.

## Divergences and gaps

1. **`thinking` emits no event.** The v1 vocabulary has no kind for it, and
   goose sends one item per chunk, so an `unmapped` error for each would flood
   the ring. This departs from opencode's reasoning row, which emits one
   `unmapped` error per reasoning part.
2. **The content `error` item is not emitted.** R4 carries the same text twice,
   once as an `error` content item inside a `message` and once as the `error`
   line. The line is the failure's reason (v1 section 3), so the item adds no
   second event.
3. **`output-limit` is a goose tag on `error`.** `ErrorData.Kind` is an open
   string, so this is not a new event kind. R11 ends at the output token limit
   with an assistant `message` whose `content` is empty and whose metadata carries
   `outputTokenLimitReached`, and `complete` still follows with exit 0, so a run
   cut at its limit looks finished. The flag is read whatever the content holds,
   once per run. An empty `content` array with no flag is a no-op.
4. **`notification` is unmeasured.** v1 section 3 names it, and no recording
   shows one. It maps to `error{kind:"unmapped"}` with the raw line until one
   does, so it stays visible. Any other unknown line type or content item type
   does the same.
5. **No `session.started`, no `turn.*`, no cost.** No line carries a session
   start. Per-request usage appears only in goose's sessions table (v1.1 L2),
   never in the stream, so there is no `turn.completed`. `complete` carries
   cumulative totals: `input_tokens` and `output_tokens` go to `Usage`, the two
   cache counts to `Metering`, and `total_tokens` is read and not stamped
   (`Usage` has no total). The recordings show `cache_read_input_tokens` above 0
   (R1 3217, R5 3, R9 7) and `cache_write_input_tokens` 0 on every run, which the
   fixture README's "cache token counts are 0" does not match. Neither the
   recordings nor `complete` carry a cost.
6. **A run without `complete`.** R2 (a tool outside the permission list in
   `approve` mode) ends after a `toolRequest`, exits 1 and has its reason on
   stderr only. The parser emits `tool.call` and nothing else: no
   `session.ended` and no synthetic failure. The exit code, the missing
   `complete` and the run record's `stderr_tail` decide it at the reap.
7. **No harness session id.** The stream carries none, so
   `harness_session_id` is empty.
