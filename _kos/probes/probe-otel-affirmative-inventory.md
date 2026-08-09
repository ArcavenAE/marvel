# Probe brief: the affirmative OTEL inventory, read as resource-matrix instrumentation

**Status:** OPEN (brief and inventory; no probe run to its success signals).
**Question:** `question-marvel-otel-architecture`, sub-question B (which signals
the control loop actually needs).
**Medium:** re-reading two existing artifacts affirmatively. No new measurement
was taken for this document.
**bd:** aae-orc-e06y.
**Prior work it re-reads:** `probe-interactive-ctx-remainder-sweep.md` (rounds 3
and 4, and its SCOPE QUALIFICATION section), `finding-008-harness-native-telemetry.md`.

## Why this document exists

The remainder sweep ruled OTEL out. The ruling is correct and it is narrow, and
the operator has directed that it be understood narrowly. The sweep says so
itself, in one sentence:

> OTEL cannot carry context pressure. OTEL remains the right substrate for
> spend, throughput, tool access, and attention metering, and the same
> measurements that disqualified it here are positive evidence for those.

The mechanism of the disqualification is the reason it does not generalize.
Every harness token instrument that exists is a monotonic cumulative Counter.
Occupancy is a per-request LEVEL that resets at compaction, so folding a
counter into it reproduces finding-007's defect and grows worse the longer a
session runs. Spend is not a level. Dollars spent do not reset at compaction,
and neither do tokens billed, tools invoked, subagents spawned, or seconds
consumed. A counter is the correct instrument for each of those.

So the negative result is a mismatch between an instrument type and a quantity,
not a defect in the channel. This document takes the same enumeration that
produced the negative result and reads it as what it also is: an inventory of
resource-matrix instrumentation marvel does not otherwise have.

## The discipline this document holds itself to

Three tiers of evidence, marked per row, because the sweep it draws on was
largely desk research plus limited live observation:

- **MEASURED**: observed in a live run on kinu. Exactly one harness reached
  this tier: Claude Code 2.1.220, one `claude -p` headless turn against haiku
  with the console exporter (finding-008). That is the only model call spent
  across both source documents.
- **BINARY**: the instrument name is a literal in an installed binary on kinu,
  read by inspection. Not driven against a live model turn, so the name is
  confirmed and the emission conditions, cardinality, and attribute set are
  not.
- **DOCS**: vendor documentation only. Gemini CLI is not installed on this
  host, so its entire row is at this tier.

**Instrument type is marked separately from evidence tier**, because it is the
load-bearing property here and it is often inferred rather than read. Where the
type is inferred from the instrument's name or purpose rather than from a
verbatim symbol, the row says INFERRED.

**Nothing here is a finding.** A finding in this project requires a probe run to
its own success signals. This is an inventory assembled from prior work, and the
success signals that would promote it are stated at the end.

## Part 1: the instrument-type axis, stated once

Four shapes appear across the catalog. Which shape an instrument has decides
which matrix rows it can serve, independent of what it is named.

| Shape | Resets? | Serves | Fails at |
|---|---|---|---|
| **Cumulative counter** | never | spend, totals, rates over a window (by differencing) | any level that resets, occupancy first among them |
| **Histogram** | per-observation | per-event distributions (turn size, latency) | current value for one session, unless the dimensions carry a session id |
| **Gauge** | n/a, samples a level | levels, including occupancy | nothing relevant; no harness publishes one for occupancy |
| **Span / log event** | n/a, discrete | moments (compaction fired, request made), and attributes riding them | anything continuous |

The single most consequential fact in the catalog: **no harness publishes a
gauge for context-window occupancy.** That is the whole of the negative result,
and it says nothing about the other three shapes.

## Part 2: Claude Code

Claude Code 2.1.226 carries 20 distinct `claude_code.*` names by the sweep's
count, 8 metrics plus 12 spans. The table below carries the ones the sweep and
finding-008 record by name; it is not asserted to be all 20, because neither
source document enumerates the full set in one place.

| Instrument | Shape | Matrix row | Fidelity | Evidence |
|---|---|---|---|---|
| `token.usage` (attrs `type`, `model`) | cumulative counter, VERBATIM: `or.tokenCounter=t("claude_code.token.usage",{description:"Number of tokens used",unit:n("tokens")})` | 2 (tokens/spend/rate) | high for billed tokens by class and model; useless for occupancy | MEASURED |
| `cost.usage` | counter (INFERRED from name and pairing) | 2 (spend) | high; the direct dollar line | MEASURED |
| `active_time.total` | counter (INFERRED) | 14 (time) | medium; wall-clock consumed, not turn deadlines | MEASURED |
| `session.count` | counter | 7/12 (lifecycle, fleet composition) | low on its own, useful as a denominator | MEASURED |
| `tool.blocked_on_user` | INFERRED counter | **13 (human attention)** | see below; the highest-value row in the table | BINARY |
| `subagent.spawn` | INFERRED counter | 12 (fleet composition), per-team accounting | medium; counts fan-out marvel cannot otherwise see | BINARY |
| `tool.execution`, `tool` (span), `bash.subprocess` (span), `mcp.rpc` (span) | mixed counter and span | 5 (tool/MCP access), 6 (filesystem/git) | medium to high for access accounting; spans carry per-call detail | BINARY |
| `interaction`, `llm_request`, `hook` | INFERRED counters | 7 (lifecycle), throughput | medium | BINARY |
| `lines_of_code.count`, `commit.count`, `pull_request.count` | counters | output measurement, which is critic's remit | see the caution below | BINARY (`code_edit_tool.decision` also present) |
| `compaction` (span, attrs `trigger: auto\|manual`, `message_count`) | span | 1 (context window), as an ACTUATOR signal not a level | low on tokens: carries none | BINARY |
| `api_request` (log event) | event | 2 (spend), and the occupancy NUMERATOR | high: `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `cost_usd`, `model`, `duration_ms` | MEASURED |

Four things about this table that are worth more than the table.

**`tool.blocked_on_user` is the find.** Vision Gap 1 is operator attention
routing, described there as the scarcest metered resource and explicitly
recorded as having no owner. Row 13 of the matrix is the same quantity. A
harness is already emitting an instrument for precisely it, and the sweep found
it while looking for something else. Whatever happens to CTX%, this should be
carried to whoever picks up Gap 1. Caveat held honestly: the name was read from
a binary, not driven live, so what it counts (blocks? seconds blocked?
approvals?) is unverified, and that distinction decides whether it meters
attention or merely counts interruptions.

**The output-measurement counters are a trap, and the matrix already says so.**
`lines_of_code.count`, `commit.count`, and `pull_request.count` are exactly the
game-able units `fleet-throughput-as-the-objective-function.md` warns about:
lines of code rewards verbosity in the same way output tokens do. They belong
to critic's per-selected-outcome accounting, not to a scheduler's objective.
Recorded as present, recommended against as a throughput numerator.

**`api_request` carries the numerator but not the denominator.** finding-008
grepped the entire emitted telemetry for `contextWindow`, `context_window`,
`used_percent`, `occupancy`, and `remaining` and got zero. So CTX% over the
Claude OTEL feed requires marvel to supply the limit from its own table, which
is a strictly worse position than the stream, where the limit rides inline.
This is the one row where the affirmative reading does not rescue anything.

**The compaction span is gated behind beta.** `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`
is beta and withdrawable. The numbers a shift trigger would want
(`preCompactTokenCount`, `postCompactTokenCount`, `truePostCompactTokenCount`,
`autoCompactThreshold`, `willRetriggerNextTurn`) exist in-process but are
arguments to the vendor's internal analytics sink, not to the OTEL span, which
gets trigger and message_count and nothing else.

## Part 3: Codex

| Instrument | Shape | Matrix row | Fidelity | Evidence |
|---|---|---|---|---|
| `codex.turn.token_usage` (dim `token_type` = total, input, cached_input, output, reasoning_output; the sweep also records `cache_write_input`, `non_cached_input`, `reasoning_output`, `total`) | **histogram** | 2 (spend), at the richest class decomposition in the catalog | high for per-turn distributions | BINARY |
| `codex.api_request`, `codex.sse_event`, `codex.tool.call`, each with a `*.duration_ms` histogram | counter plus histogram | 5 (tool access), 14 (time/latency) | high on latency, which no other harness matches | BINARY |
| `codex.conversation_starts` (log event) carrying `context_window`, `auto_compact_token_limit`, `max_output_tokens` | log event | 1 (context window), **the DENOMINATOR** | high, and unique: codex is the only harness publishing its own window over OTEL | BINARY |
| `codex.task.compact`, `codex.compaction.model_fallback` (reason enum: `user_requested`, `context_limit`, `model_downshift`, `comp_hash_changed`) | span | 1, as actuator | carries tokens, on the same span as the seven turn token levels | BINARY |
| `gen_ai.usage.*` on `codex.api_request` | standard convention | 2 | see the conformance defect in Part 6 | BINARY |

Two corrections this inventory owes to `question-marvel-otel-architecture`, both
already named in the sweep's scope qualification and repeated here because the
node has not absorbed them:

1. The node cites finding-008 as establishing that no harness exports the
   context window. **Codex does**, on `conversation_starts`. The conclusion that
   OTEL cannot carry pressure still holds, but the reason is now instrument TYPE
   (the numerator is a counter) rather than field absence, and anyone
   re-checking this later needs the distinction.
2. The codex catalog and the wire disagree on the denominator. The sweep records
   two terms in the catalog and one on the wire (round 3, "The codex denominator:
   two terms in the catalog, ONE on the wire"). Treat the catalog listing as a
   claim about the binary, not about a live export.

**One inference marked as inference, not recommendation.** A histogram of
per-turn token usage is a per-turn quantity, so a session's current level is in
principle recoverable from it in a way a cumulative counter's is not. That does
not rescue occupancy here, because codex's default metric dimensions are
`auth_mode`, `originator`, `session_source`, `model`, and `app.version`, with no
`conversation.id`. The logs carry the conversation id and the metrics do not.
So the shape is closer to right and the attribution is missing. Stated because
the distinction matters if codex ever adds the dimension; not stated as a path.

## Part 4: Gemini CLI (DOCS tier throughout)

| Instrument | Shape | Matrix row | Fidelity | Evidence |
|---|---|---|---|---|
| `gemini_cli.token.usage` (type dimension: input, output, thought, cache, tool) | counter | 2 (spend) | high on classes | DOCS |
| `gen_ai.client.token.usage` | counter, standard convention | 2 | see Part 6 | DOCS |
| `gemini_cli.session.count`, tool-call count and latency, api-request count and latency, file-op counts | counters and histograms | 5, 7, 14 | medium | DOCS |
| `gemini_cli.chat_compression`, emitted as **both** counter and log event, carrying `tokens_before` and `tokens_after` | counter plus event | 1, as actuator, with tokens | high, and the only actuator event carrying both counts | DOCS |
| `gemini_cli.token.efficiency` | unknown shape | 2 | unassessed; name appears in the sweep with no measurement behind it | DOCS |

Gemini is the harness where the affirmative reading changes a decision. Its
`--telemetry-outfile` is a genuine file exporter: it writes logs and metrics to
a path, overrides the OTLP endpoint, and is recommended by the project's own
docs for local use. A per-agent path assigned at spawn gives marvel the
compaction event with both token counts, read the way `internal/procstat` reads
anything else, with no listener, no receiver, and attribution by a path marvel
itself chose. The sweep's verdict: **the only harness where OTEL gives marvel
something it cannot get more cheaply elsewhere.**

One arithmetic shortcut the sweep records and explicitly refuses, repeated here
so nobody rediscovers it as a good idea: at auto-compaction, gemini's
`tokens_before` is approximately the threshold fraction times the window, so a
window is recoverable by division. It would be wrong whenever the threshold
moves, which is the silent-misreport mode `internal/usage/limits.go` refuses by
design. Do not ship it.

## Part 5: the measured negatives

Both are worth carrying because a measured negative retires a question.

- **opencode 1.18.5.** Ships `@opentelemetry/api` and `sdk-trace` with no
  exporter and no metrics SDK. Its OTEL SDK init was removed as unused upstream
  (sst/opencode#1738). Its `gen_ai.usage.*` strings are Vercel AI SDK span
  attributes that stay inert until a plugin registers a provider, and emission
  is gated behind a per-call `experimental_telemetry` flag with no config
  surface exposing it. Serves zero matrix rows over OTEL. BINARY.
- **Crush 0.67.0.** Links the otel API plus `metric/noop` and `trace/noop`
  transitively and carries no SDK or OTLP exporter, so it cannot export at all.
  Its analytics stream is PostHog to `data.charm.land`, which is Charm's channel
  and not marvel-addressable. Serves zero rows. BINARY.

For both, the finding-007 stream and sqlite accountant remains the only path.
There is no telemetry channel to add.

## Part 6: the one genuine defect, kept separate from the mismatch

Everywhere else in this document the negative result is an instrument-type
mismatch. In one place it is a real defect, and conflating the two would be a
mistake in both directions.

For the standard `gen_ai.usage.*` semantic convention specifically, opencode's
bundle carries exactly `input_tokens` and `output_tokens`, with no cache term
and no reasoning term. Applied to a measured turn where input was 117 and cache
read was 30,080, a spec-conformant reading reports 117 against a true occupancy
of 30,199.

**Conformance is the defect there.** It cannot be fixed without breaking
portability, which was the convention's whole value proposition. This is a
limitation of one semantic convention for one quantity. It is not a limitation
of OTEL as a transport, and it is not a limitation of vendor-namespaced
instruments, and it should not be generalized into either.

## Part 7: coverage against the matrix, and the honest gaps

Reading the inventory against the 17 rows of `elem-agentic-resource-matrix`:

| Row | Best OTEL instrument available | Assessment |
|---|---|---|
| 1 context window | codex `conversation_starts` (denominator only); compaction spans on 3 harnesses | numerator unavailable in the right shape. **Ruled out for pressure; usable as an actuator.** |
| 2 tokens / spend / rate | claude `token.usage` + `cost.usage`; codex `turn.token_usage`; gemini `token.usage` | **Best-served row in the matrix.** Counter is the correct shape. |
| 3 cache locality | claude `api_request` cache_read and cache_creation token classes | partial: the classes are there, the placement decision is not an OTEL question |
| 4 model/runtime capability | `model` attribute on several instruments | attribute, not instrument; enough for attribution, not for placement |
| 5 tool access (MCP) | claude `tool`, `tool.execution`, `mcp.rpc`; codex `tool.call` | well served |
| 6 filesystem/git access | claude `bash.subprocess`; gemini file-op counts | partial |
| 7 data services | none | no coverage |
| 8 work-tracking access | none | no coverage |
| 9 session I/O access | none | no coverage; not an OTEL-shaped question |
| 10 interagent communication | none today; the M2 bus would be the emitter | no coverage, by construction: the thing is unbuilt |
| 11 authority | none | no coverage; M1 prerequisite |
| 12 compute | none from harnesses; marvel's own `internal/procstat` covers it | marvel-side already |
| **13 human attention** | **claude `tool.blocked_on_user`** | **one instrument, and Gap 1 has no owner. Highest-value row here.** |
| 14 time | claude `active_time.total`; codex `*.duration_ms` histograms | well served on elapsed; nothing on deadlines |
| 15 durable agent state | compaction spans, indirectly | weak; handoff has no schema to instrument yet |
| 16 behavior provisioning | none | no coverage; a sideshow question |
| 17 credential custody | none, and correctly so | out of scope by design |

Seven of seventeen rows have some OTEL instrument. The concentration is exactly
where the instrument type is right: spend, tool access, and time. That
concentration is the affirmative result, stated as a shape rather than a
headline.

## Part 8: the two mechanisms that transfer regardless

Both were found chasing pressure and neither depends on it.

1. **`OTEL_RESOURCE_ATTRIBUTES` is honored by claude and codex**, and claude
   promotes resource attributes to datapoint labels by default. Marvel can stamp
   its own pane identity into exported telemetry at spawn. It is free, it makes
   any later OTEL consumption attributable to a marvel session, and the sweep
   recommends doing it whether or not a byte of OTEL is ever ingested. This is
   the same move as the spawn-time session-id pin.
2. **Two listener-free receive paths exist.** Claude's Prometheus PULL exporter
   (`OTEL_EXPORTER_PROMETHEUS_HOST` and `_PORT`, both present in the binary),
   where marvel allocates the port per agent and scrapes on its own cadence; and
   gemini's file exporter. Both make attribution a function of a port or path
   marvel itself assigned, and both avoid standing up a collector.

Note the tension with finding-008, which recommends one shared in-process OTLP
receiver. The sweep's later rounds prefer listener-free paths. These are not
reconciled in either document, and reconciling them is
`question-marvel-otel-architecture`'s job, not this inventory's.

## What this inventory does NOT rule on

`question-marvel-otel-architecture` holds the real design question: what marvel
advertises, hosts, forwards, and subscribes to, with five candidate topologies
and the kitchen-sink trap named. Nothing here touches it. This document answers
one half of its sub-question B (which signals the control loop needs) by saying
which signals EXIST and in what shape. Whether marvel should consume any of them
is a separate ruling.

Two further exclusions:

- **Not a build list.** The sweep's own ruling 3 says the inventory is a
  selection instrument, not a build list. Presence of an instrument is not an
  argument for consuming it.
- **Not a throughput answer.** `lines_of_code.count` and friends are present and
  are the wrong unit; see `probe-fleet-throughput-prior-art.md` for why the
  numerator problem is not solved by any instrument in this catalog.

## Success signals, if this is ever run as a probe rather than assembled

This document is desk work over prior desk work with one live turn under it.
Promoting it to a finding needs:

1. **One live OTLP smoke per harness.** Codex and gemini were never driven end
   to end against a model turn. finding-008 names this as its first gap. Success:
   the metric and event shape settled for codex and gemini the way finding-007
   settled the stream shape, including whether emission conditions match the
   binary literals.
2. **`tool.blocked_on_user` characterized.** What it counts, at what cardinality,
   under what conditions. Success: a statement of whether it meters attention
   (duration, or approvals outstanding) or merely counts interruptions. Failure
   signal: it turns out to be a permission-prompt counter with no duration, in
   which case Gap 1 gains a weak proxy rather than an instrument, and the
   headline in Part 2 should be walked back.
3. **Codex per-session env override tested.** Whether `OTEL_EXPORTER_OTLP_ENDPOINT`
   overrides the user-level `otel.*` config per process. finding-008 names this
   as the load-bearing unknown for codex under marvel multi-session isolation.
   Failure signal: it does not override, in which case codex OTEL is host-global
   and per-session routing on that harness is not available at any price.
4. **Gemini installed and its outfile exercised.** The entire gemini row is DOCS.
   Success: a real compaction event with `tokens_before` and `tokens_after` read
   from a marvel-assigned path.
5. **Re-measure on Linux** (B13), and re-pin versions. Everything here is macOS
   at Claude 2.1.220/2.1.226, Codex 0.146.0, opencode 1.18.5, Crush 0.67.0. A
   harness bump that changes the telemetry schema changes the read.

## What would change the read

If a harness ships an occupancy GAUGE, the negative result evaporates and the
pressure question reopens on this channel. Nothing in the catalog suggests one
is coming, and codex publishing its denominator on a log event while publishing
its numerator as a counter is mild evidence that vendors are not thinking of
occupancy as a metric at all.

If `tool.blocked_on_user` turns out to carry duration, row 13 gains its first
real instrument anywhere in the platform, and that is a larger result than
anything else in this document.
