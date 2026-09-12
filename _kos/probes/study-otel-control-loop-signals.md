# Study: which signals the control loop needs, and whether harness OTEL feeds the ring or runs beside it

**Serves:** `aae-orc-24oy` (feeds the decision brief `aae-orc-mqgf`; scopes
the cardinality probe `aae-orc-dbfc`)
**Question:** `question-marvel-otel-architecture`, sub-questions B
(subscription scope) and C (ring versus parallel)
**Date:** 2026-09-12 · **Medium:** analysis, no code
**Measured on:** marvel `1003277` (main, 2026-09-12), read in a worktree; no
harness was driven; harness instrument names are at the evidence tier the
inventory assigned them
**Refs:** `study-otel-topology-options.md` (fkvv),
`probe-otel-affirmative-inventory.md` (e06y, Parts 7b, 7c, 9), finding-008,
finding-037, `question-shift-triggers`, orc ADR-007
**Sibling:** `study-otel-composability-boundary.md` (tpda) uses the allowlist
here as its "control-loop consumer reads it" test

## Premise check (finding-103)

Three claims in the ticket, re-checked against the tree:

1. **"internal/otel is a 32-line stub with a gauge nothing populates."** 35
   lines (`internal/otel/metrics.go`), unchanged since the ticket; the
   simulator populates the gauge (`cmd/simulator/main.go:21`), the daemon does
   not. Already corrected in the node 2026-08-09. Still nothing entrenched.
2. **"The events ring is append-only with no subscribe or fan-out seam; it
   is polled via `marvel events`."** Still true of the ring itself:
   `Ring` exposes `Emit`, `Snapshot`, `Len` (`internal/events/events.go:336,
   377, 396`), and the daemon's only consumer is `handleEvents`, which calls
   `Snapshot` (`internal/daemon/daemon.go:786-808`). What changed: follow
   mode landed 2026-08-05 (PR #106) as a sequence cursor, `Filter.SinceSeq`
   (`events.go:368-371`). That is a resumable poll, not a subscription. Zero
   control-loop code reads the ring.
3. **The claim the ticket did not make, and which changes the answer.** The
   fan-out point for harness data is not the ring. It is the per-session
   drain goroutine in `internal/session/manager.go:772-775`: one loop over
   `inst.Events()` that calls the usage accountant inline and then emits a
   flattened line to the ring. The comment at `manager.go:733-740` records
   that a tee was considered and rejected, because the adapter channel
   back-pressures the harness on purpose and a second reader would add a
   second back-pressure path. And `internal/usage/accountant.go:73-77`
   states the seam this study was asked to sketch: "Every method is safe for
   concurrent use, so a future OTEL receiver goroutine can share one
   accountant with N per-session drain goroutines." `internal/usage/doc.go:96-104`
   adds that an OTEL feed "adds a sibling constructor and one caller, and
   changes nothing in the fold." The subscription seam exists; it is the
   accountant, not the ring.

Also retired since filing: `aae-orc-w5su` (context pressure over OTEL),
2026-08-08, type mismatch. The allowlist below treats context pressure as a
consumer of the stream and the heartbeat, with OTEL able to serve only the
activity timestamp beside it.

## Where the control loop actually reads

Every decision runs inside `ReconcileOnce` (`internal/team/controller.go:428-448`)
on a 2 s tick (`internal/daemon/daemon.go:48`, `controller.go:2084-2096`) or
at an operator verb. Each reads store fields, never a feed:

| consumer | reads | written by | evidence |
|---|---|---|---|
| reap (pane death, crash charge) | tmux pane liveness | `session.Manager.ReapDead` | `controller.go:456-462`; `manager.go:967` |
| heartbeat healthcheck | `Session.LastHeartbeat`, role `HealthCheck.Timeout` and `FailureThreshold` | heartbeat RPC, token-bound | `controller.go:1268-1283`; `internal/api/store.go:558`; `types.go:126-130` |
| restart policy and crash-loop backoff | `FailureCount`, `RestartCount`, `BackoffUntil`, `MaxRestarts`, `RestartPolicy` | the two consumers above via `noteCrashAndBackoff` | `controller.go:1501`, `715-735`, `97-101` |
| shift readiness (`allReady`) | `State == Running`, `LastHeartbeat` non-zero when the role has a heartbeat check | same | `controller.go:1986-2005` |
| activity (advisory, restart-neutral) | `SessionContext.ContextAt` against role `ActivityTimeout` | accountant sink and heartbeat RPC (both stamp `ContextAt`) | `controller.go:1377-1400`, `1419`; `types.go:399` |
| admission (`max_sessions`, `max_tokens`) | live count; `TeamSpend` totals as a floor | store; accountant | `internal/daemon/admission.go:20-49`; `internal/api/budget.go:69` |
| context pressure (CTX%) | `SessionContext` (percent, tokens, limit, peak, source) | accountant sink; heartbeat RPC with numerator and window | `store.go:670`, `558-600`; `internal/api/heartbeat.go:91-127` |
| process metrics | `SessionMetrics` (CPU, RSS, IO) | `RunMetrics`, 5 s | `internal/daemon/metrics.go:16-36`; `daemon.go:49-53` |

Two facts fall out. First, **no decision reads CTX% yet**: a grep of
`internal/team` and `internal/daemon` for `ContextPercent` returns nothing.
The automatic shift trigger (M4, `question-shift-triggers`) is the consumer
that would, and it is unbuilt. Second, process metrics "inform an operator
rather than a control decision" by declaration (`daemon.go:51-53`) and
admission excludes them on evidence (`internal/admission/doc.go`, excluded
dimensions). So the signals marvel materializes for decisions today are:
liveness, heartbeat recency, crash counts, activity recency, and cumulative
team tokens.

## The allowlist

Derived from the table above. "Materialize" means marvel keeps derived state
in the store; "pass through" means it goes to operator remotes and marvel
holds nothing. "OTEL today" names the harness instrument that could carry the
signal, at the inventory's evidence tier, and whether its TYPE fits.

| # | signal | consumer(s) | source today | OTEL today (type fit?) | required attributes | rate / window | verdict |
|---|---|---|---|---|---|---|---|
| 1 | pane liveness | reap, restart, readiness | tmux pane exists | none. `claude_code.session.count` is a counter, not liveness; `session.ended` is lagging and unauthenticated (no) | n/a | every 2 s tick | **materialize; not an OTEL signal** |
| 2 | heartbeat recency | healthcheck, readiness, activity | heartbeat RPC, token-bound (`heartbeat.go:91-127`) | none with the same authority. A datapoint arrival is unauthenticated; the token exists because a forged beat keeps a dead peer looking healthy (`events.go:104-112`) (no) | session key + token | per role `Timeout`; evaluated each tick | **materialize; stays on the RPC** |
| 3 | crash and restart accounting | restart policy, backoff, readiness hold | derived from 1 and 2 | none (no) | role key | edge, per tick | **materialize; derived** |
| 4 | activity recency (`ContextAt`) | `evaluateActivity` | any accountant sample or heartbeat | **yes, as a timestamp only.** Any per-session datapoint or log record with `session.id` proves the harness did work: `claude_code.api_request` (MEASURED), `codex.api_request` (BINARY) (type is irrelevant; only arrival time is read) | `session.id` or marvel's injected session key | per request; latest-wins | **materialize the timestamp; OTEL is a plausible third feed** |
| 5 | occupancy level + window (CTX%) | none today; M4 later; CLI column | accountant per request (headless); heartbeat with numerator + window (interactive) | numerator per request on `claude_code.api_request` event (MEASURED); denominator on `codex.conversation_starts` (BINARY); every metric is a cumulative counter (no for metrics; yes for the two events, with the denominator caveats in the node corrections) | `session.id`, `model`, token classes, layout | per request; latest-wins, plus peak | **materialize; if OTEL ever feeds it, through the accountant's sibling constructor** |
| 6 | cumulative team tokens | admission `max_tokens` | accountant `TeamSpend` (`accountant.go:726`) | `claude_code.token.usage` (counter, VERBATIM; attrs `type`, `model`), `codex.turn.token_usage` (histogram), `gemini_cli.token.usage` (yes: cumulative is the right shape; row 2 is the best-served row) | `session.id` plus marvel `workspace`, `team`, `role` resource attrs; `type`, `model` | export interval (Claude docs: 60 s metrics by default) versus per-request from the stream; sum per team | **materialize; OTEL type fits, and a lagging feed understates, which R3 makes sound** |
| 7 | process CPU / RSS / IO | operator display only | `procstat`, 5 s | none from harnesses; adapter `health.heartbeat` carries RSS for the simulator (n/a) | pid subtree | 5 s | **materialize as today; not OTEL** |
| 8 | blocked-on-user (attention) | none today; Gap 1 | adapter `permission.requested` to the ring as warning | `claude_code.tool.blocked_on_user` (BINARY; counts or durations unknown) | `session.id` | unknown until characterized | **pass through; deferred behind the row-13 verdict** |
| 9 | auth required | none today; shift-trigger candidate ("login failures") | adapter `auth.required` to the ring | none identified (`api_error` event is a guess) | session | edge | **pass through; ring line is enough** |
| 10 | per-session cost, turns, durations | ring line at `session.ended`; accountant retire | adapter `SessionEndedData.Metering` | `claude_code.cost.usage`, `codex.*.duration_ms`, `active_time.total` (yes) | session, model | export interval | **pass through; critic's remit, not the scheduler's** |
| 11 | tool and MCP call detail, spans | none | adapter `tool.call` / `tool.result` (one line to the ring) | `tool.execution`, `mcp.rpc`, `bash.subprocess` spans (BINARY) | session, tool | per call | **pass through; trace ingest is a second pipeline** |
| 12 | message content, prompts | none | adapter `message.*` (clipped to 160 bytes for the ring) | gated off by default on every harness | none | none | **never materialize; sovereignty (tpda)** |

**The allowlist is rows 1 through 7**, and only rows 4, 5 and 6 have any OTEL
carrier at all. Row 4 needs a timestamp, row 5 needs two log events that
the stream already delivers with the denominator in a better place, and row
6 is the one row where the harness metric type matches the quantity and
marvel's meter already computes the same number from the stream. Rows 8
through 12 pass through.

That matches the inventory's Part 9 draft (rows 2, 5, 14 as the defensible
set) with one narrowing: tool access (row 5) and elapsed time (row 14) are
metered cleanly over OTEL but **no control-loop consumer reads them**, so
they pass through until one exists. Presence is not a build argument.

**Rate.** Nothing in the allowlist needs sub-tick delivery. The loop runs at
2 s; the heartbeat window is a role setting measured in seconds to minutes;
admission reads a cumulative floor at verbs. A 60 s metric export interval is
too slow for row 4 (activity) if the role's `ActivityTimeout` is short, which
is why row 4 should key on log records (5 s default interval per the Claude
docs, unmeasured here) rather than metrics if OTEL ever feeds it.

**Aggregation.** Latest-wins per session for rows 4 and 5, sum per team for
row 6, edge-triggered for rows 1 through 3. No window, no rate-of-change, no
percentile is consumed anywhere. That is the "bounded and not persisted"
test in the sibling study, seen from the consumer side.

## Ring versus parallel: the decision

The ticket's dichotomy is: harness OTEL feeds INTO the ring (one vocabulary,
one consumer path) or runs as a parallel channel. Both halves assume the ring
is a consumer path. It is not. The ring is an operator log with one 160-byte
line per event (`bridge.go:21-25, 80-81`), bounded at 2000, and the comment
in `bridge.go:21-25` is explicit that verbatim detail "stays in the adapter
stream for consumers that subscribe to it." `session.UsageObserver`
(`manager.go:681-689`) exists precisely because "the event ring cannot serve
this purpose: bridgeEvent flattens the typed payload into a one-line string
clipped at 160 bytes, so token counts survive only as prose."

So the one internal vocabulary and one consumer path already exist, and
neither is the ring:

```
adapter stream ──> drain goroutine ──┬──> usage.Accountant.Observe ──> store (SessionContext, TeamSpend)
  (rtevents.Event,                   │        (typed, arithmetic)          │
   twelve kinds)                     │                                     └──> ReconcileOnce / admission read the STORE
                                     └──> events.Emit(bridgeEvent) ──> ring (one line, operator log)

heartbeat RPC ──> store.UpdateSessionHeartbeat ──> store (LastHeartbeat, SessionContext)
```

**Decided now.**

1. **Harness OTEL does not feed the ring as a data path.** Rejected: the ring
   drops the payload by design, and a ring consumer would have to parse prose.
2. **Harness OTEL does not run as a parallel channel** with its own state and
   its own consumers. Rejected: that is the two-competing-paths failure mode
   the ticket names, and it would give the store a third writer of
   `SessionContext` with no provenance field to name it. (`ContextSource`
   was added in marvel#153 precisely because two producers were already one
   too many to infer from shape: `types.go:274-287`.)
3. **If harness OTEL is ever consumed, it enters as a feed into the
   accountant**, through the sibling constructor to `sampleFromEvent`
   (`sample.go:109`; `doc.go:96-104`), on the same `Coords`, writing the same
   store fields through the same `Sink` (`accountant.go:66-68`), and stamping
   a new `ContextSourceKind` value so provenance stays declared. Transitions
   it produces reach the ring through the same `events.Emitter` the
   accountant already holds (`accountant.go:89, 103`). One vocabulary
   (`usage.Sample`), one consumer path (accountant to store to loop), one
   operator log.
4. **The ring needs no subscription seam for OTEL.** The seam the topology
   study said "does not exist" was looked for in the wrong structure. It
   exists at the accountant, and it is already concurrency-safe for the
   receiver goroutine the comment anticipates.

**Deferred, with the gate named.**

- **Whether to build the OTEL feed at all** stays behind the row-13 verdict
  (topology study, recommendation 3). The allowlist shows what it would buy
  with the verdict unknown: row 4 for interactive sessions the stream cannot
  see (the `question-interactive-context-pressure` gap), and row 6 as a
  cross-check on a meter that already exists. Neither justifies the build on
  its own; row 13 (attention) would.
- **Where the receiver goroutine gets its bytes** is the hosting decision:
  shape (d)'s sidecar exporting to a loopback listener, Claude's Prometheus
  pull exporter on a marvel-allocated port, or Gemini's file exporter
  (inventory Part 8). That is `aae-orc-h6ck` and `aae-orc-1n6b`, not this
  study. Whatever the transport, it terminates in `Observe`.

## The subscription seam, sketched for the record

The ticket asks for the sketch if the ring is the answer. The ring is not the
answer for OTEL, but the M2 bus and the director protocol will eventually
want push delivery of control-plane transitions, so the three shapes are
compared once here with their consequences, and the recommendation is
recorded so the question is not reopened from zero.

The constraint that decides it: `Ring.Emit` runs inside the drain goroutine
(`manager.go:772-775`), which is the path that back-pressures the harness.
**A subscriber must never be able to block `Emit`.** Any seam that can is a
new back-pressure path against the harness, which is what `manager.go:733-740`
refused for the tee.

| shape | delivery | back-pressure | memory | verdict |
|---|---|---|---|---|
| callback registry (`Subscribe(func(Event))`) | synchronous, in `Emit` | a slow callback blocks the drain and the harness | none beyond the callback's own | **reject**; violates the constraint by construction |
| channel fan-out (one buffered chan per subscriber, `Emit` selects with default) | asynchronous | never blocks: full channel drops the event | bounded per subscriber by the buffer | acceptable, but silent loss: a subscriber cannot tell a quiet period from a dropped one |
| filtered watch (per-subscriber `Filter` + bounded queue + `Seq` cursor; on overflow drop-oldest and record the gap) | asynchronous | never blocks | bounded per subscriber; the gap is a number the subscriber reads | **recommend when needed**; reuses `Filter` and `Seq` that follow mode already ships |

The filtered watch is the poll cursor made push: the subscriber holds the
`Seq` it last saw, the queue is bounded, and an overflow is reported as
"resume from Seq N, M events skipped" rather than swallowed. It fits a
bounded in-memory ring because it inherits the ring's own honesty about
loss: the ring already overwrites its head when full (`events.go:354-355`),
and a subscriber that fell behind is in the same position as a poller that
slept too long.

**Is a bounded in-memory ring appropriate for control-loop consumption?**
For the loop as built, the question does not arise: the loop reads the store.
For a future push consumer, the ring is appropriate for transitions (edge
events, low rate, loss tolerable because the store holds the level) and not
for levels (a consumer needing "current CTX%" should read the store, never
replay the ring). That split is the same one the accountant encodes: levels
are latest-wins state, events are the record of change.

## What this scopes for the cardinality probe (`aae-orc-dbfc`)

The allowlist shrinks the probe to what marvel would materialize, and moves
the rest to the operator's collector.

1. **Only row 6 is a metrics series marvel would consume.** So the series
   count that matters to marvel is `claude_code.token.usage` by `type` x
   `model` x `session.id` (plus codex's histogram by `token_type` x
   dimensions, which lacks `conversation.id` on the wire). Measure: series
   per session with `OTEL_METRICS_INCLUDE_SESSION_ID` on (default) versus
   off, and whether marvel's injected `workspace/team/role` resource
   attributes are enough for the team sum so that `session.id` can be dropped
   at the collector for metrics while logs keep it.
2. **Row 4 is a log record, not a metric.** Attribution rides
   `conversation.id` / `session.id` on log records, which both Claude and
   Codex carry (finding-008). Measure log volume per session at the default
   5 s log export interval under a busy turn, since that is the record marvel
   would tail for activity.
3. **Churn shape.** A shift doubles a role's sessions transiently (admission
   ruling R5), and short-lived headless runs are the norm. Measure collector
   memory and series growth across ten shift generations, with the
   collector's own retention of stale series as the variable.
4. **Lag.** Export interval versus the 2 s reconcile tick and versus a short
   `ActivityTimeout`. The probe should say at what `ActivityTimeout` a
   60 s metric interval would false-positive "stalled," which is the
   argument for keying row 4 on logs.
5. **Out of scope for marvel's cost model.** Spans (row 11), tool and MCP
   detail, message content: their cardinality is the operator remote's
   problem by the pass-through rule, and the probe should not spend budget on
   them beyond confirming they are not ingested.

## What would change the read

- **A control-loop consumer for tool access or elapsed time appears** (a
  per-role tool budget, a wall-clock timebox). Rows 5 and 14 of the inventory
  move from pass-through to materialize, and they are the rows where OTEL's
  type fits best.
- **Row 13 carries duration.** Attention becomes materializable, the feed
  earns its build, and row 8 above moves into the allowlist.
- **A harness ships an occupancy gauge.** Row 5's OTEL column flips to yes
  for metrics, and the stream stops being the better place for the level.
- **The store gains a third `SessionContext` writer by any other route.**
  That would be the parallel-channel failure mode arriving under another
  name; decision 2 above is the guard.

## Explicit non-goals of THIS study

The non-goals list, degradation, and sovereignty (`study-otel-composability-boundary.md`);
the hosting and advertisement mechanics (`aae-orc-h6ck`, `aae-orc-1n6b`);
characterizing `tool.blocked_on_user` (inventory success signal 2); the
M4 shift trigger's own signal (`question-shift-triggers`, where the
"pressure is not a fill level" correction lives).

## Harvest if ratified

- Sub-question B resolved as the twelve-row table with rows 1 through 7 as the
  allowlist; sub-question C resolved as "neither: the accountant is the
  feed seam, the ring is the operator log."
- The topology study's "events-ring fan-out seam that does not exist" is
  corrected in the node: the seam exists at `usage.Accountant`, and the ring
  seam, if ever built, is the filtered watch.
- `aae-orc-dbfc` re-scoped to the five measurements above.
- Node harvest is left for the ratification session, per the brief; operator
  ratifies per ADR-007.

## Sources

- `study-otel-topology-options.md`; `probe-otel-affirmative-inventory.md`
- `_kos/findings/finding-008-harness-native-telemetry.md`; finding-037
- `internal/session/manager.go`, `internal/session/bridge.go`,
  `internal/usage/{doc,accountant,sample}.go`, `internal/events/events.go`,
  `internal/team/controller.go`, `internal/daemon/{daemon,admission,metrics}.go`,
  `internal/api/{types,store,heartbeat,budget}.go` at `1003277`
- [Claude Code monitoring: export intervals and `OTEL_METRICS_INCLUDE_SESSION_ID`](https://docs.anthropic.com/en/docs/claude-code/monitoring-usage)
