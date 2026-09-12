# Study: the OTEL composability boundary, what marvel will not do, and what it keeps

**Serves:** `aae-orc-tpda` (feeds the decision brief `aae-orc-mqgf`; ADR
candidate for `aae-orc-4eoy`)
**Question:** `question-marvel-otel-architecture`, sub-questions D (non-goals)
and H (degradation and sovereignty)
**Date:** 2026-09-12 · **Medium:** analysis, no code
**Measured on:** marvel `1003277` (main, 2026-09-12), read in a worktree; no
harness was driven
**Refs:** `study-otel-topology-options.md` (fkvv), finding-008, finding-037,
`probe-otel-affirmative-inventory.md` (e06y), orc SOUL.md sections 1, 2, 3, 6
and 8, orc ADR-007, orc ADR-009, marvel B2
**Sibling:** `study-otel-control-loop-signals.md` (24oy) carries the allowlist
this document leans on for "what marvel keeps"

## Premise check (finding-103)

The ticket was written 2026-08-01. Re-checked against the tree today:

- "internal/otel is a stub." Still true. `internal/otel/metrics.go` is 35
  lines: a stdout meter provider and one gauge,
  `marvel.agent.context_window_percent` (metrics.go:12-30). The only importer
  is `cmd/simulator/main.go:21`. No commit has touched the package since the
  ticket was filed. Nothing is entrenched.
- "Where marvel does act, say why it is inseparable from orchestration." The
  tree has grown the things marvel acts on since August, and none of them is
  an OTEL path: the usage accountant (`internal/usage`, PR #99), admission
  (`internal/admission`, PR #101), the process sampler (`internal/procstat`),
  the token-bound heartbeat (`internal/api/heartbeat.go`, PR #168). The
  "keeps" list below is drawn from those, not from the ticket's candidates.
- The ticket cites `aae-orc-w5su` (context pressure from OTEL) as a consumer.
  Retired 2026-08-08: occupancy is a level, every harness token instrument is
  a cumulative counter (node corrections block; inventory Part 1).

## The test that makes the list testable

The ticket asks for kitchen-sink avoidance that is testable rather than
aspirational. The topology study already supplies the outer rule (vision
value 10: own policy, custody, records and judgment; rent capacity through
standard seams). This study adds the inner predicate, so that each "could
marvel also just..." can be answered in the moment rather than escalated.

Marvel keeps a telemetry capability only if all three hold:

1. **A control-loop consumer reads it.** Named in the sibling allowlist
   (24oy): reap, healthcheck, restart, shift readiness, activity, admission.
   Not "an operator might want to see it"; the ring already serves that.
2. **It is bounded and not persisted.** The ring holds 2000 events
   (`internal/events/events.go:319`) and dies with the daemon; process
   metrics are "deliberately not persisted" (`internal/admission/doc.go`);
   `UpdateSessionContext` does not persist (`internal/api/store.go:670`).
   Anything that would need a retention policy fails here by construction.
3. **No standard component already does it.** If otelcol, Prometheus, Jaeger,
   Grafana or a vendor backend does it, marvel interfaces (value 10).

A capability that fails any one of the three is a non-goal. The seven
candidates the ticket names all fail at least two, which is why they are
easy; the erosion risk is the eighth thing that fails only one.

## Non-goals, each with its reason

**No telemetry storage or retention.** Fails tests 2 and 3. Every marvel
store of anything telemetry-adjacent is latest-wins or a bounded ring, and
the two persisted structures (bolt for sessions and role health, the log ring
for stderr) hold control-plane state, not telemetry. A retention policy is
the first thing a store needs and the first thing marvel has no basis to
choose; it is also exactly what a backend is for. The ring is the line: it
answers "what did marvel do to this session in the last couple of hours",
and it is lost on restart, on purpose.

**No query language or query API.** Fails tests 1 and 3. `marvel events`
filters on marvel's own coordinates (workspace, team, role, session, kind,
severity, sequence cursor: `events.go:361-372`) over an in-memory ring. That
is a filter, not a query: no time-range arithmetic, no aggregation, no
telemetry field access, and it cannot grow into one without the ring growing
into a store. PromQL, TraceQL and LogQL exist; marvel's job is to make its
telemetry attributable so those can be used, not to reimplement a subset.

**No dashboards or visualization.** Fails tests 1 and 3. `marvel watch` and
`marvel get sessions` render control-plane state (CTX%, health, activity,
CPU and RSS). They render what marvel decided, from marvel's store. A
telemetry dashboard renders what the harness emitted, from a backend, and
Grafana already does it against every backend an operator would run.

**No alerting or notification routing.** Fails tests 1 and 3, and it is the
one most at risk of erosion, because the ring already carries severity and
"just notify on warning" is a five-line change. Marvel emits transitions; it
does not decide who hears about them. Routing is a policy surface the vision
gives to Gap 1 (operator attention routing) as its own owner, and that owner
would consume marvel's events, not extend marvel into a notifier. Until it
exists, an operator's alerting stack reads the ring over mrvl:// or the
remote their collector feeds.

**No long-term aggregation or rollups.** Fails tests 2 and 3. The one
aggregate marvel holds, `TeamSpend`, is a cumulative meter within a daemon
lifetime that admission reads as a floor (`internal/daemon/admission.go:32-47`,
ruling R3 in `internal/admission/doc.go`). It is reset by restart, it exists
because a gate needs a number in the same process as the gate, and it has no
time axis. A rollup has a time axis and survives restarts; that is a
backend's recording rule.

**No becoming or replacing Prometheus, Jaeger, Grafana, or a vendor
backend.** Fails all three. This is the sum of the five above, stated so
that the sum cannot be assembled one reasonable increment at a time. Marvel
cannot poll (delivery is OTLP push, finding-008), so it is not a Prometheus;
it does not ingest spans (trace ingest is a second pipeline, inventory Part
9), so it is not a Jaeger; and per test 3 it fronts whichever backend the
operator owns.

**No marvel-branded collector distribution.** Graveyarded by the topology
study with reasoning recorded there; repeated here only so this list is
complete without a cross-reference. Reopen condition unchanged: an operator
requirement no upstream distribution can meet, which is evidence about
upstream.

**No telemetry processing or translation on the pass-through.** Not in the
ticket's list, added because it is the eighth thing. Marvel normalizes only
what it consumes: `sampleFromEvent` (`internal/usage/sample.go:109`) is a
feed boundary for the accountant's own arithmetic. It does not rewrite,
enrich, sample, or re-attribute telemetry bound for a remote; the otelcol
processor chain is the operator's, and a marvel-side transform in the
pass-through would be a second processor with no version handle.

## What marvel keeps, and why a control plane cannot do without it

Each item passes all three tests. Line references are the evidence.

**Advertisement: per-session telemetry configuration injected at spawn.**
Marvel constructs the process environment (`internal/runtime/adapter.go:188`,
`baseEnv`) and already puts session identity and the heartbeat token there
(`adapter.go:204-212`; `internal/session/manager.go:894-903`). Nobody else
can name a per-session endpoint, resource attribute, or Codex `-c` override
before the process exists (finding-037). Without it, harness telemetry is
unattributable to a marvel session, and every later consumer, marvel's or the
operator's, loses the join key. This is enforcement locus 1 applied to
telemetry, and it is the whole of topology shape (a).

**Supervision of a collector as a declared workload (opt-in).** Supervising
processes is the product; the M2 bus ruling (external NATS, marvel-supervised)
already ratified "supervise a standard component rather than embed it."
Shape (d) adds config generation and lifecycle, not a competence. Test 3
holds because the component is upstream's.

**The control-loop allowlist, materialized in the store.** The sibling study
derives it from the consumers in code. What marvel keeps is the latest-wins
per-session reading (`api.SessionContext`, `types.go:273-302`), the heartbeat
timestamp (`types.go:199`), the live cumulative team meter, and the process
sample (`types.go:328-338`). Each is read by a decision in `ReconcileOnce`
(`internal/team/controller.go:428-448`) or by admission. None is persisted
except through the session record's own lifecycle. This is state, not
storage.

**Marvel's own producer surface.** SOUL section 6: observable by default,
never mandatory. The quantities marvel derives (CTX% against a resolved
window, admission verdicts, shift phases, activity) exist nowhere else and are
what an operator would want on a dashboard. Marvel emits them; it does not
keep them. The 35-line stub is the right size for this today.

**The event ring.** Bounded, in-memory, poll-only, one line per event
(`internal/session/bridge.go:80-81`). It is `kubectl get events`, and it
passes test 2 by being lost on restart. It stays a sink for transitions
derived from any feed; it does not become a telemetry path (sibling study,
ring-versus-parallel).

## Degradation contract

Per SOUL section 2 and marvel B2 (no conscription): the three absent cases
must each leave marvel working. The evidence that they do is that no
control-loop consumer reads an OTEL path today. Every read in `ReconcileOnce`
is against store fields written by the heartbeat RPC
(`internal/api/store.go:558`), the accountant's sink (`store.go:670`), the
process sampler (`store.go:691`), or tmux pane liveness (`reapDeadLocked`,
`controller.go:456-462`). A grep of `internal/team` and `internal/daemon` for
`ContextPercent` returns nothing: not even CTX% is consumed by a decision yet.

| case | what marvel does | what the operator sees | evidence |
|---|---|---|---|
| **No collector present** | Advertises nothing that needs one. Claude emits nothing without `CLAUDE_CODE_ENABLE_TELEMETRY=1`; Codex defaults `otel.exporter = none`. Marvel sets neither unless a remote is declared. The control loop is unchanged. | CTX%, health, activity, admission, shifts all work from the stream and the heartbeat, exactly as today. | finding-008 per-harness table; `manager.go:772-775` (the drain feeds the accountant, not a receiver) |
| **Collector present, no remote configured** | Does not spawn a sidecar with nowhere to export. Shape (d) is opt-in and needs a declared destination (sub-question F); absent one, marvel is at the shape (a) floor. No local buffering, no "hold until a remote appears". | Same as above. | topology study, recommendation 1 and 2 |
| **No OTEL-capable harness in the team** (opencode, Crush, generic) | Advertisement is a documented no-op for that adapter; the accountant runs on the finding-007 stream profile where one exists (`internal/usage/profiles.go:37`). Zero resource-matrix rows are lost, because those harnesses serve zero rows over OTEL. | Same as above. | inventory Part 5 (measured negatives); node corrections block item 2 |

The contract in one sentence: **marvel's telemetry surface is additive to a
control loop that already closes without it.** A change that makes any row
above false is a change to bedrock B2 and needs its own ruling.

Two things the contract does not promise, stated so they are not read in:
marvel does not detect or report that an advertised collector is unreachable
(the harness's exporter retries or drops; that is the harness's behavior and
the operator's remote's problem), and marvel does not fall back from a failed
sidecar to an embedded receiver (shape (c) is a contingency revived by
ruling, not a runtime fallback).

## Sovereignty boundary

Per SOUL sections 1 and 3, applied to each thing that could cross the line.

**The operator owns the remotes and the data.** Marvel has no default remote,
hosts no remote, and writes to no destination it did not receive from the
operator's manifest or daemon configuration (sub-question F decides which).
There is no phone-home in the marvel binary and no marvel-run endpoint a
harness could be pointed at by default. Advertised endpoints resolve to
loopback or to a receiver the operator declared. This is user sovereignty as
the vision states it: the alternative collaborators name is the company town.

**Prompt content stays out by default, and marvel never turns it on.** Every
harness that can export prompt text gates it behind an off-by-default switch:
Claude Code `OTEL_LOG_USER_PROMPTS` (finding-008, confirmed as a binary
literal and left unset in the live capture), Codex `otel.log_user_prompt`
(default false, finding-008; present in the 0.153.4 `[otel]` surface beside
`otel.tool_result`, finding-037), Gemini `--telemetry-log-prompts` (docs
tier). Marvel's advertisement sets none of these, and a sidecar config marvel
generates enables none of them. Enabling prompt or tool-result export is an
operator act in the operator's own configuration. The sibling allowlist needs
no prompt text; nothing in the control loop reads content.

**Telemetry is not an exfiltration path.** Two sides. Outbound: the only
destinations are operator-declared (above). Inbound to marvel: the allowlist
is the ceiling on what marvel materializes, and everything else passes
through untouched or is not collected at all. Marvel does not tail, buffer,
or forward telemetry it does not consume. Where a listener-free path is used
(Claude's Prometheus pull exporter on a marvel-allocated port, Gemini's file
exporter, inventory Part 8), marvel reads the allowlist fields and nothing
else, the way `internal/procstat` reads a process table.

**Vendor analytics channels marvel does not own are left alone.** Codex's
`metrics_exporter` defaulted to `statsig` in the 0.146 era (OpenAI's channel,
finding-008) and Crush ships PostHog to `data.charm.land`. Marvel neither
enables nor disables these: doing either would be marvel deciding the
operator's relationship with a vendor. Whether a per-session Codex config
marvel constructs (via `-c` or `CODEX_HOME`) leaves the statsig default in
place is unmeasured and belongs to `aae-orc-1n6b`.

**Marvel routes no credentials: the ADR-009 audience test on OTLP.** The
test is audience, not format, applied to the most durable artifact held.

- An OTLP **endpoint** is a name. Its audience is a network; it confers no
  authority. Marvel may mint, inject, and record it freely. This is
  advertisement.
- An OTLP **header** is, in practice, a vendor ingest key: bearer authority
  at a third party. Its audience is the vendor, so holding it is custody, and
  custody stays out. Marvel never reads, stores, generates, or projects
  `OTEL_EXPORTER_OTLP_HEADERS` or the per-signal variants. If a remote needs
  one, it lives where the operator's collector reads it: an otelcol config
  references it as an environment variable the operator supplied, and the
  value never passes through marvel's store or a file marvel writes. A pane
  that inherits such a header from the operator's own shell does so by the
  operator's act, and marvel is not its holder.
- The **heartbeat token** is marvel's own issuance (audience: the daemon;
  ADR-009 names it as the worked example). It must never ride
  `OTEL_RESOURCE_ATTRIBUTES` or any exported attribute, because export
  changes the audience to whoever runs the remote. Marvel's resource
  attributes carry identity coordinates (workspace, team, role, session
  key), never a secret. `HeartbeatRefused` exists precisely because a leaked
  token lets one process report on another's behalf
  (`internal/events/events.go:104-112`).
- Marvel already applies the same discipline to model backends:
  `ClassifyBackendRedirection` (`internal/api/backend.go:95`) reads which
  base-URL variables are set to grade the denominator, and reads no key.

**The multi-user boundary is untouched.** Each session's telemetry is emitted
under that session's own harness auth. A marvel receiver, if one ever exists,
ingests metrics; it routes nobody's credentials (finding-008, auth section).
One person's agents under one person's credentials remain the permitted case;
nothing in this design changes SOUL section 3's routing boundary.

## What this study decides versus defers

**Decided here (subject to ratification):** the three-part test; the eight
non-goals; the five keeps with their inseparability arguments; the
degradation contract as three rows; the sovereignty boundary including the
ADR-009 reading of OTLP headers and the resource-attribute rule for the
heartbeat token.

**Deferred, and to whom:** whether remotes are a manifest resource or daemon
config (sub-question F, `aae-orc-h6ck`); whether the statsig default survives
a marvel-constructed Codex config (`aae-orc-1n6b`); the row-13 verdict that
decides whether shape (b) is worth its seam (topology study); every
cardinality question (`aae-orc-dbfc`, scoped by the sibling study).

## What would change the read

- **A control-loop decision that needs a time axis.** If an automatic shift
  trigger (M4) turns out to need "occupancy over the last N minutes" rather
  than the latest reading plus a peak, test 2 is under pressure and the
  rollup non-goal needs a ruling rather than an inference.
- **A harness that only exports over an authenticated endpoint.** If a
  harness cannot be pointed at loopback without a key, the header rule forces
  the operator's collector to sit in front of it, which is already shape (d).
- **Gap 1 finds an owner that wants marvel to route.** The alerting non-goal
  holds until the attention-routing owner argues, with a ratified decision,
  that routing is inseparable from orchestration. This study says it is not.

## Decision text candidate (for `aae-orc-4eoy`)

Written so the brief can lift it. Status: proposed, not ratified (ADR-007:
ratification is the operator's act).

> **Title.** Marvel's telemetry boundary: advertise, supervise, consume an
> allowlist, and nothing else.
>
> **Context.** Marvel spawns harnesses that emit OTLP; the operator owns one
> or more remotes; marvel's control loop closes today without any telemetry
> path (`ReconcileOnce` reads the store, fed by the heartbeat RPC, the
> stream accountant, the process sampler, and tmux liveness). The
> composability trap has two sides: too little and marvel cannot attribute
> telemetry to its own sessions; too much and marvel becomes an
> observability product.
>
> **Decision.**
> 1. Marvel advertises telemetry configuration into every session it spawns
>    (endpoint, resource attributes, per-harness config) and touches no
>    telemetry itself by default. This is the floor and the degradation
>    state.
> 2. Marvel may supervise an operator-selected collector as a declared
>    workload, generating its configuration from operator-owned remote
>    declarations. Marvel ships no collector.
> 3. Marvel materializes only the control-loop allowlist (24oy), as
>    latest-wins, bounded, non-persisted state in the session store.
>    Everything outside it passes through to operator remotes untouched.
> 4. Non-goals: storage or retention; query language or API; dashboards;
>    alerting or notification routing; rollups; replacing any backend; a
>    marvel-branded collector; processing or translating the pass-through.
>    A capability is added to marvel's telemetry surface only if a
>    control-loop consumer reads it, it is bounded and non-persisted, and
>    no standard component provides it.
> 5. Degradation: with no collector, no remote, or no OTEL-capable harness,
>    marvel's behavior is identical to today's. Any change that breaks this
>    amends bedrock B2.
> 6. Sovereignty: no default or marvel-hosted remote; prompt and tool-result
>    export switches are never set by marvel; OTLP endpoints are names and
>    may be minted, OTLP headers are third-party authority and are never
>    held; the heartbeat token never rides an exported attribute.
>
> **Consequences.** Shape (a) plus opt-in (d) per the topology study. The
> subscriber shape (b) is gated on the row-13 verdict and, if built, enters
> through the usage accountant's feed boundary, not the event ring (24oy).
> `aae-orc-5ed6` (embedded receiver) is contingency only. Reopen conditions
> are the "what would change the read" lists in the two studies.

## Harvest if ratified

- Sub-questions D and H of `question-marvel-otel-architecture` resolved;
  record the three-part test as the erosion guard, not only the list.
- Graveyard entries for the non-goals that need one (storage, alerting), so
  they are not re-proposed; (e) already has its entry via the topology study.
- The sovereignty clauses cross-link ADR-009 (the OTLP header reading is a
  new application of its test, not a new rule).
- Node harvest is left for the ratification session, per the brief.

## Sources

- `study-otel-topology-options.md` (this repo, 2026-09-08)
- `probe-otel-affirmative-inventory.md` (this repo)
- `_kos/findings/finding-008-harness-native-telemetry.md`
- `_kos/findings/finding-037-codex-ignores-otel-env-but-takes-per-process-config.md`
- orc `SOUL.md` sections 1, 2, 3, 6, 8; orc `decisions/adr-007`, `adr-009`
- [OTel Collector configuration: environment variable substitution](https://opentelemetry.io/docs/collector/configuration/)
- [Claude Code monitoring (OTEL_LOG_USER_PROMPTS)](https://docs.anthropic.com/en/docs/claude-code/monitoring-usage)
