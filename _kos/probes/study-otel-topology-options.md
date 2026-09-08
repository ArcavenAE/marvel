# Study: what role does marvel play in the telemetry path?

**Serves:** `aae-orc-fkvv` (gates `aae-orc-5ed6`, `aae-orc-h6ck`; feeds the
decision brief `aae-orc-mqgf`)
**Question:** `question-marvel-otel-architecture`, sub-question A (Topology)
**Date:** 2026-09-08 · **Medium:** analysis, no code
**Inputs:** finding-008, `probe-otel-affirmative-inventory.md` (via
`aae-orc-e06y`), finding-037 (today), the ratified M2 bus decision, vision v4
design value 10

## The question as it actually splits

The five candidate shapes are scored in the ticket as if they were five points
on one axis. They are not. They are two independent decisions:

1. **Advertisement** — does marvel put telemetry configuration into the
   harnesses it spawns?
2. **Reception** — does marvel sit in the telemetry *data path*?

(a) is advertise-yes / receive-no. (c), (d) and (e) are all receive-yes,
differing only in *what runs the receiver* and who maintains it. (b) is
receive-yes-but-filtered, and additionally assumes a subscription seam.

Once split, the decision stops being "pick one of five" and becomes "yes to
advertisement, and what if anything hosts reception" — which has a different,
and much cheaper, answer.

## Constraints established rather than assumed

- **Delivery is OTLP push; nothing is scrapeable.** finding-008 checked all
  five harnesses: where "Prometheus" appears it is an exporter option that
  still pushes OTLP, a transitive string, or a backend behind a collector.
  **Marvel cannot poll.** A receiver must exist somewhere or the telemetry is
  simply dropped.
- **Nothing is entrenched.** `internal/otel` is a 35-line stub the daemon does
  not import, and the events ring is append-only with **no subscribe or
  fan-out seam**. So (b)'s "tap a filtered slice" is not a configuration
  choice; it is a seam that must be built first.
- **The control-loop payload is narrow.** The affirmative inventory scores 2 of
  17 resource-matrix rows as having an instrument whose type matches its
  quantity (row 2 spend, row 5 tool access), 5 partial or attribution-only, 9
  with nothing, and **one unknown and most valuable** — row 13 human
  attention, via `claude_code.tool.blocked_on_user`, whose verdict turns on
  whether it carries duration or only counts events.
- **Marvel already meters the one row OTEL serves cleanly.** `internal/usage`,
  `internal/admission` and `internal/procstat` cover spend without OTEL, which
  bounds what OTEL work would add.
- **The row marvel most needs is the row OTEL cannot serve.** Context-window
  occupancy is a per-request LEVEL that resets at compaction; every harness
  token instrument is a monotonic cumulative Counter. That is an
  instrument-type mismatch, not a wiring gap.
- **Advertisement is uniform across harnesses** (finding-037, today). Codex
  ignores OTEL env vars entirely, but takes per-process configuration through
  `-c otel.…` and `CODEX_HOME`, both measured. So no harness forces a shared
  endpoint, and advertisement does not have a ragged exception.

### The load-bearing consequence

The strongest argument for marvel owning a pipeline was *marvel needs the feed
for its own control decisions*. Measured, that argument is weak: the quantity
marvel most needs is the one OTEL structurally cannot supply, and the one it
supplies cleanly is one marvel already meters internally.

**Marvel does not need to be in the telemetry data path today.** That is the
finding this study turns on, and it was not knowable when the shapes were
enumerated.

## Prior art: the ecosystem already answers this

OpenTelemetry's own deployment taxonomy is *agent* (collector beside the
application or on the same host — sidecar or DaemonSet), *gateway* (a
standalone service behind one OTLP endpoint), and the recommended
*agent-to-gateway* two-tier combination, which is explicitly motivated by
separation of concerns: agent configs stay small and focused, central
processors do the heavy work.

More directly on point: the **OpenTelemetry Operator** — the ecosystem's own
control plane for this problem — does two things. It injects configuration
(env vars, auto-instrumentation) into workloads, and it injects and manages
**sidecar collectors**, with `.Spec.Mode` selecting
`DaemonSet | Sidecar | StatefulSet | Deployment`.

That is advertisement plus supervised sidecar. **The reference implementation
of a control plane managing OTEL for workloads it schedules is (a) + (d).** It
is not (c), and it is emphatically not (e). Adopting that shape is adopting a
known one; the collector-anti-patterns literature exists precisely because
home-grown pipelines are a recognised mistake.

## Options table

Scored against composability (SOUL §2), independence and graceful degradation
(B2 — must work with no collector, no remote, no OTEL-capable harness),
operational cost, maintenance surface, and the kitchen-sink test.

| shape | composability | independence / degradation | ops cost | maintenance surface | kitchen-sink | verdict |
|---|---|---|---|---|---|---|
| **(a) advertiser only** | best — marvel touches no telemetry, any collector works | perfect: `exporter = "none"` is the degraded state and needs no code | zero | ~nil; config injection is what adapters already do | passes | **ADOPT as the floor** |
| **(b) advertiser + selective subscriber** | good in principle | fine | low | **requires building a fan-out seam on the events ring plus an ingest to tap** | passes | **DEFER** — gated on row 13 |
| **(c) embedded receiver in the daemon** | moderate — self-contained, but marvel becomes an OTLP endpoint | strongest on "zero external infra" | low to run | marvel owns OTLP parse, backpressure, retry, TLS, cardinality — a pipeline, in-process | **borderline fail** | **CONTINGENCY only** |
| **(d) supervised sidecar otelcol** | good — standard component, replaceable | needs otelcol present; degrades to (a) when absent | one supervised process | config generation + lifecycle; the component itself is upstream's | passes | **ADOPT as the opt-in** |
| **(e) marvel ships a collector distribution** | worst | irrelevant | high | an entire vendor product | **fails outright** | **GRAVEYARD** |

## Recommendation: layered, not a single pick

1. **(a) is the default and the degradation floor.** Marvel advertises
   per-session endpoints and attributes into every harness it spawns, and
   touches no telemetry itself. With no collector configured, `exporter =
   "none"` and nothing breaks. This satisfies B2 by construction.
2. **(d) is the opt-in** for operators who want collection and have no
   collector of their own. Marvel spawns and declaratively configures a real
   `otelcol` as a supervised workload. Marvel already supervises processes —
   that is the product — so this adds configuration generation, not a new
   competence.
3. **(b) is a later increment**, gated on the row-13 verdict. If
   `claude_code.tool.blocked_on_user` carries duration, operator attention
   becomes measurable from harness telemetry, which is Gap 1 material and would
   justify building the events-ring fan-out seam. If it only counts events, the
   subscription earns little and should not be built.
4. **(c) is held as a contingency**, not a fallback in the normal path —
   revived only if a real deployment target cannot install `otelcol`.
5. **(e) is graveyarded** (below).

### Consistency with a ruling already made

The M2 bus decision (2026-08-01, ratified) chose **external NATS, supervised by
marvel as a declared workload**, and vision v4 records that choice as "the same
supervision pattern as the sidecar-collector spike." The fleet has already
ratified *supervise a standard external component rather than embed or rewrite
it*, and accepted the runtime dependency that comes with it via mise/brew. (d)
is that same ruling applied to a second component. Choosing (c) here would put
telemetry and messaging on opposite philosophies for no stated reason.

Vision design value 10 says it directly: **own policy, custody, records and
judgment; rent capacity through standard seams**, and never build the
utility-parity layer — "sandboxes, browsers, gateways, memory stores are now
commodity; interface, don't build." A collector is that layer exactly.

## Graveyard entry: (e) marvel ships its own collector distribution

Ruled out. A collector distribution is a vendor product with a release
cadence, a processor/exporter compatibility matrix, and a CVE surface, none of
which is differentiating for an agent control plane. It is the kitchen-sink
trap named in the operator framing, and the obsolescence discipline's measured
pattern — *the most-built surface is the most-eaten surface* — applies with
full force: this is the surface most directly in a vendor roadmap's path.
**Reopen only if** an operator requirement appears that no upstream
distribution can satisfy, which would be evidence about upstream, not about
marvel.

## Disconfirming the prior

The ticket's prior was: (e) is out, the live contest is **(b) versus (d)**,
with (c) as the zero-external-infrastructure fallback that B2 argues for.

- **(e) out — CONFIRMED**, with reasoning recorded above so it is not
  re-proposed.
- **"(b) versus (d)" — DISCONFIRMED as framed.** They are not alternatives.
  (b) is a *subscription* decision; (d) is a *hosting* decision. You can have
  (d) with no subscription at all, and (b) cannot exist until (c) or (d)
  provides something to subscribe to. Treating them as rivals hid the actual
  question, which is whether marvel belongs in the data path at all — and the
  affirmative inventory answers "not yet."
- **"(c) is the fallback B2 argues for" — PARTLY DISCONFIRMED.** B2 requires
  marvel to work with *no* telemetry infrastructure. That is satisfied by (a)
  degrading to `exporter = "none"`, which costs nothing. It does not require
  marvel to host a receiver. Independence argues for (a), not for (c); (c) was
  reaching for independence and landing on ownership.

## What would change the read

- **Row 13 carries duration.** Operator attention becomes measurable from
  harness OTEL; (b) becomes worth its seam.
- **A harness ships a context-window gauge.** Marvel's primary control signal
  becomes available over OTEL and the data-path calculus inverts.
- **A deployment target that cannot install `otelcol`.** Revives (c).
- **The M2 bus supervision pattern fails in practice.** Since (d) leans on that
  precedent, its failure is evidence against (d) too.

## Explicit non-goals of THIS study

Which signals the control loop needs (`aae-orc-24oy`); the composability
non-goals list — storage, dashboards, alerting (`aae-orc-tpda`); the
advertisement mechanism and whether it rides the service directory
(`aae-orc-1n6b`); cardinality and overhead (`aae-orc-dbfc`).

## Harvest if ratified

- `question-marvel-otel-architecture` sub-question A resolved; record the
  layered answer rather than a single shape.
- Graveyard node for (e) carrying the reasoning above.
- **Re-scope the two spikes this study gates:** `aae-orc-h6ck` (shape d,
  supervised sidecar) becomes the primary spike; `aae-orc-5ed6` (shape c,
  embedded receiver) becomes contingency-only and should not run until (d) is
  measured or a no-otelcol target appears.
- Operator ratifies per ADR-007 — this is a recommendation, not a self-ratified
  choice.

## Sources

- [Deploy the Collector](https://opentelemetry.io/docs/collector/deploy/)
- [Agent deployment pattern](https://opentelemetry.io/docs/collector/deploy/agent/)
- [Gateway deployment pattern](https://opentelemetry.io/docs/collector/deploy/gateway/)
- [Agent-to-gateway deployment pattern](https://opentelemetry.io/docs/collector/deploy/other/agent-to-gateway/)
- [OpenTelemetry Operator](https://github.com/open-telemetry/opentelemetry-operator)
- [Injecting Auto-instrumentation](https://opentelemetry.io/docs/platforms/kubernetes/operator/automatic/)
- [Collector anti-patterns](https://github.com/open-telemetry/opentelemetry.io/blob/main/content/en/blog/2024/otel-collector-anti-patterns/index.md)
