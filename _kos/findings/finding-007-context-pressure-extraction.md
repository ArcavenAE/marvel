---
id: finding-007-context-pressure-extraction
title: "Context pressure (CTX%) extraction from real harness streams"
question: question-stream-attachment
confidence: frontier
tags: [marvel, streaming, observability, harness, context-pressure]
bd: [aae-orc-frkq, aae-orc-w5su]
provenance:
  created_by: agent
  session: marvel-frkq-probe-2026-08-01
  created_at: "2026-08-01"
  host: kinu
---

# Context pressure (CTX%) extraction from real harness streams

Context pressure is reconstructable from each cooperative harness's own
structured stream without owning the PTY, so the shim (finding-004,
`aae-orc-gtpz`) is not required for this capability. The one Lens-1 trap the
brief named is real and confirmed by measurement: for Claude Code the per-turn
`input_tokens` alone is not the context size and understates it by orders of
magnitude when the prompt is cached, so CTX% must sum the cached token classes.
Claude Code declares its own context limit in the stream; Codex and OpenCode do
not, so marvel carries a model to limit table for those two. The derivation
belongs in one shared usage-accountant fed by the events ring, not in each
adapter's parser, because occupancy is stateful (it accumulates and resets on
compaction) and the parsers are deliberately per-line stateless emitters.

## Method

Host kinu, macOS (arm64). Harnesses as installed, identical versions to
finding-005: Claude Code 2.1.220, codex-cli 0.146.0, OpenCode 1.18.5, Crush
v0.67.0. Gemini CLI is not installed on this host.

Claude, Codex, and OpenCode were driven live and are authenticated on this
host. I drove each headless for a clean stream read (`claude -p --output-format
stream-json --verbose --model haiku`, `codex exec --json --skip-git-repo-check
-s read-only`, `opencode run --format json -m opencode/deepseek-v4-flash-free`),
then ran Claude through marvel's shipped FIFO path with the built `bin/marvel`
daemon to confirm the end-to-end binding and to observe the CTX% gap in
`marvel get sessions` directly. Claude also got a four-turn resumed session to
measure occupancy accumulation. Codex and OpenCode got one live turn each; their
per-turn usage shape is the load-bearing read and one turn settles it.

Cost discipline: all Claude turns were haiku and short; no compaction was forced
(crossing a Claude compaction needs ~160k tokens over many long turns and real
quota). Compaction-crossing measurement is named as a recommended follow-up, not
a blocker. Crush and Gemini rows are schema-read from finding-005 and the graph,
not live-verified, and are tagged as such.

The daemon and its tmux session were torn down; no marvel daemon or
`marvel-*` tmux session from this probe remains. Temp captures were removed.

## SP1: per-harness context-occupancy inventory

| Harness | Evidence | Occupancy in stream? | Best occupancy proxy (from the stream) | Model limit source | Compaction observability |
|---|---|---|---|---|---|
| Claude Code 2.1.220 | live-verified | No direct field; reconstructable | `result.usage.input_tokens + cache_read_input_tokens + cache_creation_input_tokens` (the full prompt on the final API request of the turn). `input_tokens` alone is wrong. | In-stream: `result.modelUsage.<model>.contextWindow` (= 200000 for haiku-4-5); `maxOutputTokens` also present (= 32000). No table needed. | Downward step in the proxy. No explicit marker in the `-p`/resume result stream; `compactMetadata` exists only in the transcript jsonl (orc finding-057), a separate surface. |
| Codex 0.146.0 | live-verified | No | `turn.completed.usage.input_tokens` (already includes `cached_input_tokens` as a subset; `total_tokens = input_tokens + output_tokens`). Occupancy ≈ `input_tokens`. | Not in the `exec --json` stream (`thread.started` carries only `thread_id`). Present only in the rollout file `token_count.info.model_context_window` (= 258400 for the current model). Use a marvel model to limit table keyed on the launch `-m` arg, or read the rollout file. | Downward step in `input_tokens` across turns; the rollout `total_token_usage` is the accumulating counterpart. No explicit marker in the exec stream. |
| OpenCode 1.18.5 | live-verified | No | `step_finish.part.tokens.input` (+ `cache.read` + `cache.write`). For the free non-caching model `cache` was 0 and `total = input + output + reasoning`. | Not in the stream. From a marvel model to limit table keyed on the launch `-m` arg (opencode also carries the model in its own model DB, out of stream). | Downward step in `part.tokens.input` across turns. No explicit marker in `run --format json`; session/compaction lifecycle is a `serve`-mode surface. |
| Crush v0.67.0 | schema-read | No structured stream at all | None from a stream. Token accounting lives in `<repo>/.crush/crush.db` (sqlite `messages`); occupancy would be reconstructed by summing message tokens against a limit table. | Marvel model to limit table (no in-stream or db-declared limit found). | Not observable from a stream; would require diffing db message-token sums. |
| Gemini CLI | schema-read | Unknown (not installed here) | Unverified. `--output-format stream-json` documented; usage-field shape unconfirmed on a current build (finding-005 gates on #9009 / #13561). | Unverified; assume marvel table until measured. | Unverified. |

Measured detail behind the rows:

**Claude Code, the cache trap, measured.** First turn: `input_tokens` = 10,
`cache_read_input_tokens` = 16514, `cache_creation_input_tokens` = 13379. The
true context on that request is the sum, 29903 tokens, 14.95% of the declared
200000 window. `input_tokens` alone (10) understates the context by roughly
3000x because the system prompt, tools, skills, and memory are cached. A
four-turn resumed session showed clean monotone accumulation as history moved
into the cache: 29903 -> 29935 -> 30033 -> 30168 tokens (14.95% -> 15.08%),
with `input_tokens` flat at 10 the whole time and `cache_read` carrying the
growth. This is decisive: the proxy is the three-way sum, and the limit is in
the same line (`modelUsage.<model>.contextWindow`), so Claude needs no table.
The top-level `usage` reflects the final API request of the turn, which is the
turn's high-water mark (context only grows within a turn as tool results
append), so the proxy is a peak, not an underestimate.

**Codex declares the limit only in the rollout file, and its rate-limit percent
is a different quantity.** The `exec --json` stream's `turn.completed.usage`
carries `input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`,
`output_tokens`, `reasoning_output_tokens`, but no context-window field. The
newest rollout `token_count` payload carries `info.model_context_window` (258400)
and `rate_limits.primary.used_percent` (95.0). That `used_percent` is the weekly
plan rate-limit budget, not context-window occupancy; do not confuse it with
CTX%. Codex's `input_tokens` (13992) already includes `cached_input_tokens`
(11008) as a subset, which is the opposite of Claude's additive layout, so the
occupancy proxy for Codex is `input_tokens` itself, not a sum.

**OpenCode carries a total but no limit.** `step_finish.part.tokens` was
`{total:29893, input:29879, output:2, reasoning:12, cache:{write:0, read:0}}`.
Occupancy is `input` plus the cache classes. The context limit is not in the
stream and must come from a marvel table (or OpenCode's own model DB, out of
band).

> **CORRECTED IN PLACE 2026-08-08. The sentence that stood here, "with
> `total = input + output + reasoning`", was removed rather than annotated,
> because it was read downstream as a general fact and it is not one.**
>
> The row above has BOTH cache classes at zero. On such a row
> `total == in + out + reasoning` and
> `total == in + out + reasoning + cache.read + cache.write` are the SAME
> ARITHMETIC. The observation could not discriminate between them, so it was
> evidence for neither, and stating the narrower identity presented a
> degenerate measurement as a settled shape.
>
> Measured 2026-08-08 against 215 `step_finish` rows in the local opencode
> store plus a fresh two-turn capture on opencode 1.18.15: **215 of 215
> satisfy the cache-inclusive identity**, and the 17 rows that also satisfy
> the narrower triple are **exactly the 17 non-caching rows**. Two caching
> models agree independently. The same data settles the adjacent question
> below in the other direction: 179 caching rows carry `input` BELOW
> `cache.read` (as low as 1 against 35584), so `input` does not subsume cache
> reads and the additive layout is correct.
>
> `cache.write` was 0 on all 215 rows and both live turns, so its share of
> `total` remains inferred rather than observed.
>
> **What this cost, and why the correction is worth this much space.** The
> removed sentence justified `TotalExcludesCache: true` in the opencode
> adapter, which is the single flag that SUPPRESSES the `TotalMismatch`
> cross-check. So the unsupported claim disabled the instrument that would
> have falsified it, and it was quoted in `internal/runtime/events/events.go`
> as the motivating measurement. Fixed in marvel#154.
>
> **The process failure is narrower and more interesting than "a finding was
> wrong."** This finding DID flag the adjacent gap: the next sentence said
> cache subsumption was "unverified here". The caveat sat beside the claim
> without being attached to it, and a downstream reader took the unqualified
> half as general. A caveat that does not travel with the claim it qualifies
> is not a caveat. See `aae-orc-37hm`.

## SP2: the "selected other values" inventory

The Lens-1 directive said "context pressure and selected other values" without
writing the list down. Candidates, each tagged with the harness that exposes it
and the field. This is the scope boundary for `aae-orc-w5su` plus follow-ons.

| Value | Claude Code | Codex | OpenCode | Notes |
|---|---|---|---|---|
| Turns elapsed | `result.num_turns` (Metering.NumTurns) | count `turn.completed` lines | count `step_finish` lines | one-shot exec is single-turn; multi-turn is `--resume`/`serve` |
| Tool-call rate | `tool.call` events (parsed) | `tool.call` from `command_execution` | `tool.call` from `tool` parts | already normalized in the ring; rate is a ring-side derivation |
| Cost | `result.total_cost_usd`; `modelUsage.costUSD` | none in stream (rollout only) | `step_finish.part.cost` | codex cost needs the rollout file |
| Permission-block state | `result.permission_denials` (Metering) | not in exec stream (approval policy) | rejection shows as `tool.result{ok:false}` | `permission.requested` kind is defined but emitted by no parser (interactive / serve only) |
| Auth-required state | not in NDJSON; stderr-derived | not in stream | not in stream | `auth.required` kind defined, emitted by no parser; needs a stderr sink |
| Time-since-last-output | ring-side from event `ts` | same | same, plus OpenCode carries `part.time` | adapters stamp `ts` at consumption (no vendor ts except OpenCode) |
| Rate-limit / throttle | none in stream | rollout `rate_limits.primary.used_percent` | none in stream | codex only, and rollout-file only, not the exec stream |
| Error rate | `error` events (parse/unmapped/vendor) | same | vendor `error` frames + parser errors | already normalized |
| Model id | `system/init.model` + `modelUsage` key | NOT in exec stream (`thread.started` = thread_id only) | NOT in stream (from launch `-m`) | only Claude declares model in-stream; matters for the limit table |

The load-bearing SP2 conclusions: model id is in-stream only for Claude, so the
limit table for Codex/OpenCode must key on the launch arg; rate-limit/throttle
is a Codex-only, rollout-file-only signal; and auth-required is the one common
value that no primary stream carries, so surfacing it is a separate stderr-sink
concern (a plausible, narrow reason to want the shim later, distinct from CTX%).

## SP3: derivation seam, and the shim yes/no

**Seam: one shared usage-accountant fed by the events ring. Not per-adapter
parser logic.** Occupancy is stateful. It accumulates across turns and resets on
compaction, and compaction is detected as a downward step in a per-session
series. The parsers (`internal/runtime/claudecode`, `codex`, `opencode`) are
deliberately stateless one-line-in, events-out emitters, and one Parser is
constructed per session; pushing accumulation and compaction detection into each
parser would triple the stateful logic and the model to limit table across three
packages. The ring already carries the normalized per-turn/session usage
(`events.Usage` plus `events.Metering`). An accountant that subscribes to the
ring, keys on `agent_id + harness_session_id`, owns the single model to limit
table, computes occupancy per turn, and runs one compaction-detection algorithm
is the seam that matches the existing shape. It writes the result into the same
store field the simulator already drives (`ContextPercent`) so `marvel get
sessions` renders CTX% for real harnesses with no change to the renderer.

One small parser change feeds the accountant: the claudecode parser reads
`modelUsage` for per-model In/Out/cache/cost but drops the `contextWindow`
subfield. Surfacing it (one field on the parser's `ModelUsage` struct, carried
on `Metering`) hands the accountant Claude's authoritative limit for free.
Codex/OpenCode need no new parser fields for occupancy; their per-turn
`input`/`tokens.input` are already normalized, and the limit comes from the
table.

**Shim required for context pressure? No.** Every occupancy signal is in the
structured stream that marvel already parses over the FIFO: Claude's cached
token classes plus in-stream `contextWindow`, Codex's `input_tokens`,
OpenCode's `tokens.input`. The FIFO daemon run confirmed the shipped path
delivers the Claude result line marvel parses. The shim (finding-004,
`aae-orc-gtpz`) adds a PTY tee for rendered output and a route to stderr-derived
signals; neither is context pressure. The only stream gap is Codex's model limit
and rate-limit, which live in the rollout file, reachable by a table or a file
poll, not by the shim. So CTX% does not pull the shim into the daemon. If the
shim is pulled later it will be for auth-required (the stderr signal in SP2) or
rendered output, on its own evidence.

## SP4: Claude Code's own context percentage, and how the proxy compares

Claude Code's displayed context figure is computed against the model's total
window (the same `contextWindow` = 200000 that appears in `modelUsage`), with
the numerator being the tokens actually in context, which is exactly the full
prompt the last request carried: `input_tokens + cache_read_input_tokens +
cache_creation_input_tokens`. Marvel's SP1 proxy uses the same numerator and the
same denominator, so on the raw occupancy quantity the two agree by construction.

The divergence is in what Claude Code *displays*. It shows a "% until
auto-compact" figure that is normalized to the auto-compaction threshold and a
reserved compaction buffer, not to the raw window. The threshold is
operator-configurable (`CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`, recent default builds
trigger in the ~76 to ~84% band; the effective window can be shrunk with
`CLAUDE_CODE_AUTO_COMPACT_WINDOW`), and the buffer has been ~33 to 45k tokens.
This is why a user can see a "10% until auto-compact" readout while raw usage is
~64%: the displayed figure is measured against the threshold, not the window.

Recommendation from SP4: marvel should report raw occupancy against
`contextWindow`, which is the honest, model-portable quantity and matches Claude
Code's own numerator/denominator. If marvel later wants to mirror the "until
auto-compact" framing, it needs the two operator env vars, which are not in the
stream, so raw occupancy is the correct default. I did not cross a live Claude
compaction (cost discipline), so the downward-step detection and the exact
threshold agreement are reasoned from the field layout and the documented
behavior, not measured across a compaction; that measurement is the named
follow-up.

## Recommended approach for w5su

Build a shared usage-accountant that subscribes to the runtime events ring,
keyed on `agent_id + harness_session_id`, and compute occupancy per turn:
Claude = `(usage.In + Metering.CacheReadIn + Metering.CacheCreationIn) /
contextWindow`, Codex = `usage.In / limit`, OpenCode = `(tokens.input +
cache.read + cache.write) / limit`. Get the limit from the stream for Claude
(surface `modelUsage.<model>.contextWindow` by adding one field to the
claudecode parser's `ModelUsage`/`Metering`, which currently drops it) and from
a small marvel model-to-limit table for Codex and OpenCode keyed on the launch
`-m` arg (seed it with the values measured here: Codex current model 258400,
Claude 200000 as a fallback; the rollout file is an optional higher-fidelity
source for Codex). Detect compaction as a downward step in the per-session
occupancy series beyond a small hysteresis and reset the accumulator, since no
exec/one-shot stream carries an explicit compaction marker. Write the result
into the existing `store.UpdateSessionHeartbeat` path (or a sibling setter) so
`marvel get sessions` CTX% renders for real harnesses exactly as it already does
for the simulator.

## Gaps and what was not measured live

- **No live compaction crossing** on any harness (cost discipline). Downward-step
  detection and the SP4 threshold agreement are reasoned from field layout and
  documented behavior, not measured across a compaction. This is the top
  recommended follow-up: one long Claude session that crosses auto-compact, with
  the occupancy series and the transcript `compactMetadata` captured together.
- ~~**OpenCode cache inclusion unverified.**~~ **CLOSED 2026-08-08.** Settled
  against 215 store rows plus a live two-turn capture on 1.18.15: `input` does
  NOT subsume `cache.read` (179 caching rows carry input below cache read), so
  the additive layout is correct, and `total` DOES include the cache classes
  (215/215), which is the opposite of what the SP1 prose above asserted. See
  the correction block in SP1 and marvel#154. `cache.write` remains inferred,
  having been 0 on every observed row.
- **Codex/OpenCode single-turn only.** Their per-turn usage shape is verified;
  multi-turn accumulation (`codex exec resume`, OpenCode `serve`) is not, though
  Claude's four-turn accumulation is the pattern the others should follow.
- **Crush and Gemini are not live.** Crush has no structured stream (finding-005);
  its occupancy would be reconstructed from `crush.db` message tokens against a
  table. Gemini is not installed and stays graph-only, gated on #9009 / #13561.
- **macOS only** (B13). Stream and process behavior can differ on Linux; re-measure
  before treating these as the platform contract.
- **Version-pinned.** Claude 2.1.220, codex 0.146.0, opencode 1.18.5. A harness
  version bump that changes the usage/stream schema changes the read; re-verify
  on upgrade.

## Addendum 2026-08-01 (aae-orc-w5su implementation): the SP1 Claude numerator reads a cumulative total, not a level

SP1 prescribed the Claude numerator from the terminal line:
`result.usage.input_tokens + cache_read_input_tokens +
cache_creation_input_tokens`. Measured during the w5su build on the
repo's own fixture (`internal/runtime/claudecode/testdata/tool_call.ndjson`,
three distinct `message.id` values, `num_turns: 3`): per-request
occupancy levels are 33377, 33481, and 34136, while `result.usage`
reports 11701 / 17162 / 72131, each class the exact column-wise sum
across the three requests (total 100994). `result.usage` and
`result.modelUsage` are session-cumulative spend totals, not the final
request's level.

Consequence: the SP1 numerator reads 10.1% against a 1M window where
the truth is 3.4%, and the error grows linearly with request count, so
the longest sessions (the ones a shift trigger exists for) are the most
wrong. This finding's own four-turn live measurement missed it because
each turn was a separate `claude -p --resume` invocation, a
single-request session where sum equals level by coincidence. The
"Claude's four-turn accumulation is the pattern the others should
follow" line in the gaps section inherits the same caveat.

Correction, as shipped in the accountant (`internal/usage/doc.go`):
occupancy is a LEVEL, taken per API request from each assistant
message's usage (deduplicated on `message.id`), latest-wins. The
terminal `result` line is a spend total and a reconciliation check,
never an occupancy source. The in-stream denominator
(`result.modelUsage.<model>.contextWindow`) remains valid as SP1
stated.
