# Prompt moderation: fit each seat's prompt to its model (design)

Owner: arcaven-architect-g5-0. Subject: marvel. Issue: #379.
Status: design, for skippy review; F1-a to F1-c ruled 2026-09-27. No code until reviewed.

## Why

A seat whose starting prompt is larger than its model's context window fails
before it does any work, or worse, if a local server truncates at its
configured context instead of failing. Whether each server we run does
that is not checked yet; ticket 1 checks it. The reported baseline puts a Claude Code seat's
starting prompt at about 61k tokens against a 40960-token model, while the
same model completes a tool turn at about 15k. marvel launches every seat and
already holds a per-session context limit, so marvel is where the two can be
compared before the first turn.

## Principles

1. **Fit at spawn first; rewrite in flight last.** Most of the gap is
   configuration: which MCP servers and tools a role loads, and what text is
   injected. Configuration is chosen at spawn and needs no proxy.
2. **Never truncate silently.** A seat that cannot be fitted is refused with
   an error that names the seat, the model, the limit, the predicted size and
   the largest contributors.
3. **Record, don't guess.** Every size carries its source. The limit carries
   the rung it came from (#181). A predicted spawn size is either measured for
   this exact composition or marked unmeasured.
4. **Optional.** With moderation absent, marvel launches exactly as today and
   emits one `session.unmoderated` event per spawn (SOUL §2).

## 1. The record (extends what exists)

Per seat, beside the existing `ContextLimit` / `ContextTokens`:

| field | source | notes |
|---|---|---|
| `model` | spawn assignment (the backend registry idea, #354) | an assignment, not an observation (the router node's open-loop ruling) |
| `context_limit`, `limit_source` | the existing ladder (`internal/usage/limits.go`: stream, learned, manifest, feed, table, table-alias, unresolved) | what the fit check does per source is in section 2a; the prompt size is a separate measurement, not a rung |
| `composition_fp` | hash of harness and version, model, role, MCP server set, tool set, and the slice and seat-file hashes | the key for the measured size |
| `spawn_prompt_tokens` | the harness's first-turn usage (input plus cache fields, as the accountant already reads them) | measured once per `composition_fp`, then reused |
| `spawn_prompt_parts` | a design-time ablation, not runtime | system prompt, tool schemas per MCP server, injected content |
| `headroom` | `context_limit - spawn_prompt_tokens - reserve` | reserve covers the first turn plus a handoff (the remainder framing in `bound-context-instead-of-measuring-it`) |

The first measurement the design needs, before any lever is built: the split of
a real seat's spawn prompt across system prompt, per-server tool schemas and
injected content. Ablate one component at a time on a scratch seat (never a
live one), for the claude and codex harnesses.

## 2. The levers, in order of cost

| # | lever | acts | where | notes |
|---|---|---|---|---|
| L1 | MCP server set per role | spawn | manifest declares `mcp_servers` per role; the adapter passes the harness's strict MCP config flag | largest expected lever; no proxy |
| L2 | tool set per role | spawn | adapter passes the harness's allow/deny tool flags | measure whether a denied tool's schema still reaches the model; if it does, L2 saves nothing and only L1 counts |
| L3 | injected content | author time | wardrobe slice, seat file, appended prompt | marvel measures and names the contributor; it does not edit role text |
| L4 | in-session compaction threshold | spawn | adapter sets the harness's own compaction threshold relative to `context_limit` | the harness compacts; marvel sets the operating point |
| L5 | refuse | spawn | the fit check | the terminal lever; never silent |
| L6 | request rewriting on the model endpoint | per request | a proxy | not recommended; see section 3 |

**The fit check** runs at spawn:

1. Look up `spawn_prompt_tokens` for this `composition_fp`.
2. If it fits within `context_limit` minus the reserve, spawn.
3. If it does not, apply L1 and L2 as the manifest permits, recompute the
   fingerprint and look the new one up.
4. If it still does not fit, refuse (L5).
5. An unmeasured fingerprint on a model whose limit is under a configured
   floor (for example 64k) is refused with "unmeasured composition" (F1-b,
   ruled). There is no automatic calibration spawn. The operator measures it
   with `marvel fit --calibrate`, which runs the composition once on a
   scratch seat and records `spawn_prompt_tokens` for its fingerprint; the
   next spawn then finds a measured size.

## 2a. What the fit check does per limit source

The ladder's sources arrive at different times. At spawn only `manifest`,
`learned`, `table` and `table-alias` exist; `stream` and `feed` are declared
by the harness after the session starts, and every codex window arrives as
`feed` (`cmd/marvel/codexctx.go` routes each heartbeat window as
`LimitFromFeed`).

| source | available at spawn | the fit check at spawn | after the first turn |
|---|---|---|---|
| `manifest` | yes | refuses when the prompt does not fit | unchanged |
| `learned` | yes, when a prior session of this model declared its window on this daemon | refuses when the prompt does not fit | unchanged |
| `stream` | no | not applicable | becomes the model's `learned` value for the next spawn; if the first turn already exceeds it, the session is stopped with the same error as a refusal (never left to truncate) |
| `feed` | no | not applicable | stays `feed`; it is NOT promoted to `learned`. `learned` ranks above `manifest` in `limitLadder`, so a promoted feed window would let the statusline side channel overrule an operator's `runtime.context_window`, inverting the 2026-08-08 stream/feed ruling recorded at `internal/usage/limits.go` (`limitLadder`). See amendment F2 |
| `table` | yes | warns, never refuses (#181: an exact key can be wrong by 3.8x) | superseded by `stream` or `feed` when they arrive |
| `table-alias` | yes | warns, never refuses (an alias means whatever the harness points it at today) | as `table` |
| `unresolved` | yes | emits `session.unmoderated` and spawns | as `table` |

So a codex seat with no manifest window is warned, not refused, on every
spawn: its window arrives only as `feed`, which this design does not promote.
To make a codex seat (or a small local model) checkable, the operator sets
`runtime.context_window` in the manifest.

**Open amendment F2, for the operator (not part of this design until ruled).**
A remembered feed window could get its own rank, `feed-learned`, placed
BELOW `manifest` (stream, learned, manifest, feed-learned, feed, table,
table-alias). A codex seat would then be checked from its second spawn on,
and an operator's manifest value would still win. Default: not adopted; the
manifest route above covers the case with no ladder change.

## 3. Placement

- **Recommended: a marvel-supervised moderator service** (a declared
  workload, the same pattern as the NATS supervision). It owns the record and
  the fit check. The adapters call it at spawn. It holds no credentials and
  sees no request traffic.
- **Degradation.** If the service is not running, the adapter spawns as
  today and emits `session.unmoderated`. If it is running but the limit rung
  is `table`, `table-alias` or `unresolved`, the adapter spawns with a
  warning. It refuses only on a `manifest` or `learned` limit (section 2a).
- **Why not a sidecar per seat.** Nothing per-seat runs after spawn in L1 to
  L5. A sidecar would be a resident process whose only job is done in the
  first second.
- **Why not a proxy on the model endpoint (L6), by default.**
  `question-router-and-backend-layering` rules that marvel does not manage the
  routing layer, for three reasons:
  - a router holds live provider credentials (SOUL §3);
  - installing one pulls marvel into its supply chain;
  - a router is capacity, and marvel owns the record, not the routing
    (vision value 10).

  A prompt-rewriting proxy is a router in all three respects for any cloud
  backend.
- **The one narrow case for L6 later.** A local-only proxy in front of a
  credential-free local model server may be defensible, because it holds no
  third-party credential. It still rewrites what the model sees. That is a
  separate ruling, taken only if L1 to L5 measure short of the gap.

## 4. Operator surface

- `marvel describe session` shows `model`, `context_limit (rung)`,
  `spawn_prompt_tokens (measured|unmeasured)`, `headroom`, and the levers
  applied.
- `marvel fit <manifest> --role <r> --model <m>` is a dry run: predicted size,
  the levers it would apply, the verdict. It spawns nothing.
- `marvel fit <manifest> --role <r> --model <m> --calibrate` runs the
  composition once on a scratch seat (its own daemon, socket and state;
  never the live fleet), records `spawn_prompt_tokens` for the fingerprint,
  and stops the seat. This is the only way an unmeasured composition gets
  measured (F1-b).
- A refusal error lists the three largest contributors from
  `spawn_prompt_parts`.

## 5. Proving (for the build, per lever)

- The fit check refuses a composition predicted over the limit, and the
  error names the contributors.
- It spawns one that is under.
- L1 on a fixture role cuts the measured spawn size by the ablated schema
  size, within tolerance.
- With the moderator absent, spawns proceed and emit `session.unmoderated`.
- A `table`, `table-alias` or `unresolved` limit warns and does not refuse; a `manifest` or `learned` limit refuses.
- An unmeasured composition under the floor refuses; after `--calibrate`, the same composition spawns.
- Nothing is ever truncated. A scratch local server configured below the
  prompt size never receives the oversized prompt.

## 6. Tickets on the yes (flat, with edges)

1. The spawn-prompt split measurement, claude and codex, on scratch seats,
   plus what each local model server we run does with an over-limit prompt
   (fail or truncate). This is the gate for the rest.
2. The record fields plus `composition_fp`, marvel store (additive, json
   omitempty). Blocked by 1.
3. L1 and L2 in the manifest and adapters. Blocked by 1.
4. The moderator service, the fit check and the refusal. Blocked by 2 and 3.
5. `marvel fit` dry run, `marvel fit --calibrate` on a scratch seat, and the describe fields. Blocked by 4.
6. L4, the compaction threshold relative to the limit. Blocked by 2.

## Rulings (operator, RULED 2026-09-27, relayed by director)

- **F1-a, placement: RULED.** A marvel-supervised moderator service, with no
  proxy on the model endpoint (section 3).
- **F1-b, an unmeasured composition on a model under the floor: RULED.**
  Refuse with "unmeasured composition", and measure with a calibration run on
  a scratch seat (`marvel fit --calibrate`, never on the live fleet). No
  automatic calibration spawn.
- **F1-c, the reserve: RULED.** The larger of 8192 tokens and 10 percent of
  `context_limit`, revisited once ticket 1 measures a real first turn and
  handoff.
