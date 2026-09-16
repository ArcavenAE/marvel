# marvel's internal bus: the ring, a watch seam, two lifecycle kinds, and a tap

Status: design, 2026-09-16. Workstream B from the operator, relayed by the
director seat; authored by the architect seat before any code. The build
hands off to marvel-builder against this document, not against the tickets;
where the two disagree, this document is the one to amend.

Tickets (all flat, `aae-orc,marvel,source:session`, no parent):
`aae-orc-5ltr3` (Ring.Watch), `aae-orc-31or7` (lifecycle kinds),
`aae-orc-zhx6x` (the NATS tap, rewritten to that half only),
`aae-orc-l7nui` (payload slot decision, P3), `aae-orc-ikxil` (the ring
question, notes appended, not closed).

Line numbers below are against marvel `b798131` (origin/main at writing).

---

## 1. What the internal bus is

There is no internal bus in marvel today. There is a seed: the bounded event
ring in `internal/events/events.go`. This document names the ring as the
internal bus, adds the one primitive it lacks (a subscription), adds the two
lifecycle kinds the event plane needs, and puts a tap on it so NATS receives
a projection. Nothing else changes shape. The ring stays a ring.

The distinction this document exists to hold:

| | internal bus | NATS bus |
|---|---|---|
| where | in the daemon process | a separate `nats-server`, marvel-supervised as a declared workload |
| memory | bounded (2000 events, fixed) | JetStream streams with their own retention |
| lifetime | dies with the daemon | survives daemon restarts (`--keep-bus`, reexec) |
| owner of the vocabulary | marvel; the `Kind` constants are the catalog | director owns the envelope (A2A v1.0) |
| who reads | the daemon itself, the `events` RPC, in-process subscribers | agents, the director seat, other clusters through leaf links |
| relation | source of truth for marvel's own events | a projection, through a tap, of the kinds that matter beyond the daemon |

The ruling this rests on (2026-08-01, reaffirmed 2026-09-16): external NATS,
marvel-supervised; embedded NATS declined; marvel owns the events, director
owns the envelope. Section 6 of the orc's `docs/design/utility-event-plane.md`
made the internal-bus-as-source statement explicit: "the marvel event set is
designed with that bus as the source of truth and NATS as one projection, not
as if NATS were marvel's only bus." The one-bus-vs-two dissent recorded on
`question-marvel-service-provider-shape` is answered by these rulings: two,
with a tap between them.

Nothing in marvel reaches NATS as an event sink today. The only
`nats.Connect` is admin provisioning (`internal/bus/provision.go:156`); the
supervisor starts, renders, reloads, and stops the broker and reports those
transitions as `bus.*` kinds on the ring. NATS is supervised substrate, not a
sink. That stays true until step 5 below.

## 2. The ring as it stands

`internal/events/events.go`:

- `Kind` (`:19`), 51 constants: 39 control-plane (`session.*`, `health.*`,
  `team.*`, `role.*`, `policy.*`, `context.*`, `admission.*`,
  `reconcile.*`, `heartbeat.*`, `credential.*`, `bus.*`) and 12 `agent.*`
  lifted 1:1 from the adapter vocabulary by `internal/session/bridge.go`
  (`ringKind` at `:43`, `maxRingMessage = 160` at `:81`). `allKinds` (`:205`)
  is test-locked and backs `marvel events --list-kinds`. `docs/demo.md:560`
  still says thirty-five; the source has fifty-one.
- `Event` (`:293`): `Seq`, `Timestamp`, `Kind`, `Severity` (info or warning,
  two values), `Workspace`, `Team`, `Role`, `Session`, `Actor`, `Generation`,
  `Message`. No payload slot. `Seq` is ring-assigned under the ring's lock and
  monotonic for the ring's lifetime; it exists so pollers resume without
  timestamp dedup.
- `Ring` (`:362`), `DefaultCapacity = 2000` (`:375`), `Emit` (`:392`)
  overwrites the head when full. `Filter` (`:417`) with `SinceSeq` (`:427`)
  is the resume cursor. `Snapshot(Filter, n)` (`:433`) is the only read.
  `Len`. That is the whole API: write-mostly, one reader.
- Writers, all through the `Emitter` interface and the nil-safe `Emit`
  helper (`:340`): `session.Manager`, `team.Controller`, `usage.Accountant`,
  `bus.Supervisor`, the daemon. The `agent.*` emits run inside each
  session's adapter drain goroutine (`internal/session/manager.go`), which
  is the path that back-pressures the harness. This one fact decides the
  subscription shape in section 3.
- Readers: the daemon's `events` RPC (`internal/daemon/daemon.go:819`,
  `handleEvents` at `:875`, one `Snapshot` per request, no streaming), and
  through it `marvel events` (`cmd/marvel/main.go`). `--follow` is a
  one-second `time.Sleep` poll on `SinceSeq` (`:604`; flag at `:624`). The
  per-instance `Events() <-chan` on `internal/runtime/instance.go:57` is
  the only push surface in marvel and it is single-consumer and internal to
  the session manager.
- Lifecycle: `Daemon.shutdown` (`daemon.go:602`) emits only
  `credential.transient-dropped`. There is no `daemon.stopping`, no
  `daemon.started`. `question-marvel-graceful-stop` names this missing event.

finding-028 (2026-08-25) measured this and said "nothing reads the ring."
The 24oy study (2026-09-12) then ruled two things this design inherits
without re-deriving: the harness-data seam is `usage.Accountant`, not the
ring (the ring carries transitions, the store carries levels), and if the
ring ever gains a push consumer the shape is the filtered watch.

## 3. The subscription primitive: `Ring.Watch`

Ticket `aae-orc-5ltr3`. Spec is the 24oy study's filtered-watch row,
restated here so the builder does not have to open the study:

```go
// Watch returns a subscription that receives every event matching f
// emitted after the call, plus the count of events it missed.
func (r *Ring) Watch(f Filter, queue int) *Watch

type Watch struct { /* unexported */ }

// Events is the subscriber's read side. Never closed by Emit; closed by Close.
func (w *Watch) Events() <-chan Event
// Gap returns the number of events dropped from this watch since the last
// call, and the Seq to resume a Snapshot from. Zero means none.
func (w *Watch) Gap() (dropped uint64, resumeFrom uint64)
// Close removes the watch from the ring. Idempotent.
func (w *Watch) Close()
```

The contract, in the order it matters:

1. **Emit never blocks.** `Ring.Emit` holds the ring lock for the append and
   then, still under the lock or after it (builder's call, but bounded
   either way), offers the event to each watch's queue with a non-blocking
   send. A full queue drops its oldest entry, counts the drop, and takes
   the new event. No callback registry; a subscriber's code never runs on
   the emitting goroutine. This is the constraint the study set and the
   reason a callback shape was rejected by construction: a slow callback in
   `Emit` is a new back-pressure path against the harness, which
   `manager.go` already refused once for the tee.
2. **Per-subscriber `Filter`.** The same `Filter` that `Snapshot` takes.
   The tap will use `Filter{MinSeverity: SeverityWarning}`; follow mode uses
   whatever the operator passed. Filtering happens before the queue, so a
   narrow watch pays nothing for events it does not want.
3. **Bounded queue, drop-oldest, gap recorded.** The queue depth is the
   caller's; a sane default (the ring's own capacity, or a fraction) lives
   in one constant. Overflow is not silent: the subscriber reads the drop
   count and a `Seq` to resume a `Snapshot` from. This is the property that
   separates the filtered watch from plain channel fan-out, where a
   subscriber cannot tell a quiet period from a dropped one. The ring is
   already honest about loss (it overwrites its head); a watch that fell
   behind is in the same position as a poller that slept too long, and it
   is told so.
4. **Slow subscribers lag or drop by policy, never by accident.** The policy
   is drop-oldest. There is no "block the producer" mode and no unbounded
   mode. If a consumer needs every event it takes a bigger queue and reads
   faster, or it reads `Gap()` and backfills from `Snapshot`.
5. **Close is the subscriber's act.** A closed watch is removed from the
   ring's set; `Emit` never closes a watch's channel.

The proof, and the reason this is step 3 and not "add an API": move
`marvel events --follow` off its poll. The daemon's `events` RPC grows a
streaming shape (or a sibling `events.watch`) that holds a `Watch` for the
connection's life and writes each event as it arrives; the CLI prints them
through the existing `printBatch`. Output for the same events must be
byte-identical to the poll. Keep the poll as the fallback when the daemon
is older than the CLI. Zero new operator surface; one real subscriber; and
the never-block contract gets exercised under live adapter traffic in the
race suite, which is where a blocking bug would show.

Tests the ticket names: a subscriber that never reads does not change
`Emit` latency (race suite); overflow reports the gap; a `Filter`-scoped
watch sees only its kinds; follow output matches the poll.

## 4. The payload question

Ticket `aae-orc-l7nui`, P3. `Event` has no typed slot (finding-028;
state-observation R1). Once the ring has subscribers, a consumer receives a
`Kind`, a few scope fields, and 160 characters of prose. Three options:

1. **Leave prose.** Zero cost. Every consumer parses `Message`. The ring
   stays a display log for machines too. Correct for now, wrong the moment
   a consumer needs a number.
2. **A typed field on `Event`** (`Data any`, or a per-kind struct union).
   Simplest for consumers. It breaks the bounded-memory contract: arbitrary
   structure per event is unbounded where a 160-char string is not, and
   2000 slots times an unbounded payload is no longer a ring you can size.
   This is the tension the ring question node names and the reason the
   `Message` field came to carry everything in the first place.
3. **A side table keyed by `Seq`.** `Event` gains nothing. The ring keeps an
   optional companion, same capacity, same eviction, of small typed
   payloads for kinds that declare one, looked up by `Seq`. Each kind caps
   its payload size at declaration (a few hundred bytes), so the total is
   sized like the ring is. Kinds that carry nothing pay nothing. Display
   rendering is unchanged. A watch subscriber that wants the payload asks
   for it by `Seq`; a poller does the same.

**Recommendation: option 3, and defer the build.** Decide it when the first
consumer needs more than prose. The first candidate, died-vs-completed from
`session.crashed` plus `agent.session.ended`, is decidable today from
ordering alone (finding-028 says so) and does not need a payload. Nothing in
steps 3 to 5 below needs one either: the tap projects the `Event` as it is,
and the lifecycle kinds put their fields in the scope columns and `Message`.

Separable and small, priced with the same ticket or split from it: `marvel
events --output json`. It needs no slot and unblocks external metering.

Do not reopen ring-vs-parallel here. The 24oy study ruled it: levels
(context percent, spend) live in the store through the accountant; the ring
carries transitions. A consumer that wants "current CTX%" reads the store,
never replays the ring.

## 5. Lifecycle kinds: `daemon.stopping` and `daemon.started`

Ticket `aae-orc-31or7`. Emitted on the internal bus first and only; no NATS
connection, no authorization dependency. Fields from the event-plane design
section 6.

`daemon.stopping`, severity warning, emitted at the top of `Daemon.shutdown`
(`daemon.go:602`), before any session teardown. Scope fields: `Actor` (the
departing daemon's pid and socket). `Message` carries the reason (`stop`,
`teardown`, `reexec`, `update activation`, `detach`) and the grace window.
The daemon then follows the ordering the ruling set: publish, wait the
grace, stop sessions, then the broker (`bus.Supervisor.Stop`,
`supervisor.go:390`, whose own stop grace is 5s). Warning severity so the
tap carries it with no special case; a watcher on the other side of the tap
gets it before the sessions go.

`daemon.started`, severity info, emitted once the daemon is serving.
`Message` carries the build revision and the count of adopted sessions
(`reconcile.adopted` already fires per session; this is the one summary
line a successor or a watcher reads to know a daemon came up and what it
found).

Both join `allKinds` and the test lock; `--list-kinds` shows them;
`docs/demo.md`'s count is corrected in the same change. This also supplies
the missing event `question-marvel-graceful-stop` names.

The edge from this ticket to Ring.Watch is the proof, not the code: the
kinds compile without Watch, but the acceptance test for `daemon.stopping`
is a live watcher that sees the event strictly before the first session
stop, which is a Watch test and not a Snapshot test.

## 6. The tap: ring to NATS

Ticket `aae-orc-zhx6x`, rewritten to this half only. The tap is a second
`Watch` subscriber inside the daemon, `Filter{MinSeverity: SeverityWarning}`,
that publishes each event to `events.marvel.<cluster>.<kind>` on the local
broker. The ring's `Kind` constants are the subject catalog: a kind is a
subject token, and there is no second registry to drift from the first.
Subject shapes: orc `docs/design/parties-2026-09-16/utility-event-addresses-alignment-with-G.md`
section 2 and `bus-address-hierarchy.md` section 3.1 (marvel events stay
cluster-local; they reach the fleet tier only by supervisor lift or an
opt-in leaf import; lifting is explicit, never automatic mirroring).

The tap is one more subscriber under the never-block contract. If the
broker is slow or down, the tap's queue drops oldest and records the gap;
`Emit` is untouched; on reconnect the tap logs the gap and resumes from the
current `Seq` (it does not backfill warnings that happened while the broker
was away; the ring is the record, and a reader who wants history asks the
daemon, not NATS). The tap publishes and never reads the ring back from
NATS.

`LAMEDUCK`: the daemon translates the broker's drain into `bus.draining` on
the ring (from the `$SYS` advisory with the daemon's credential, or from the
supervisor's own knowledge that it asked the broker to drain; builder's
call, the second is simpler and needs no `$SYS` grant). The tap then
projects it like any other warning. Agents never read `$SYS`.

Precondition: `Cluster.Name` validated to the subject token class before it
appears in a subject (the `<cluster>` row of `bus-address-hierarchy.md`).
The event-plane design calls this "an existing marvel item"; no ticket for
it turned up by search, so it is named inside `zhx6x` until one does.

**Gate: never live on an anonymous broker.** On a broker with no
authorization block every subject has every publisher, and a sensory plane
on such a broker is a spoofing surface (event-plane design section 8). The
tap depends on `aae-orc-lgthv` (the grant rows: the marvel daemon publishes
`events.marvel.<c>.>`) and on `aae-orc-umw8p` (the authorization block
itself), directly and through `lgthv`. The daemon connects with its own
credential row, not the admin credential provisioning uses.

## 7. What the internal bus must never do

- **Block `Emit`.** No synchronous subscriber, no unbounded queue, no
  "wait for the slow consumer" mode. The producer side is the harness's
  back-pressure path.
- **Hold unbounded memory.** The ring is 2000 events; every watch queue is
  bounded; a payload slot, if built, is a bounded side table (section 4).
  Anything that grows with traffic is not the internal bus, it is a store,
  and stores are the accountant's and bolt's.
- **Become a control-flow graph.** The bus carries signals; decisions stay
  where they are (the reconciler, the shift state machine, admission). A
  consumer may act on an event, but no component's correctness may depend
  on having received one, because the ring drops under load by design and
  the store holds the level. ADR-007's boundary applies: the bus reminds,
  checks, and proposes; it does not judge, promote, or close.
- **Be the source of anything but marvel's own events.** Harness telemetry
  enters through the accountant; agent messages ride NATS under director's
  envelope. Neither is lifted onto the ring as a "bus" convenience.
- **Read NATS back.** The tap is one-way. The ring is the record; NATS is a
  copy of the part of it other agents care about.
- **Go live on an anonymous broker.** Section 6.

## 8. The plan

Order is the locate report's recommendation, smallest first, each a
separate PR. Steps 1 to 4 need no NATS authorization and can start now.

| # | Work | Ticket | Depends on | Gate |
|---|---|---|---|---|
| 1 | Harvest debt (section 9) | folded into the tickets and this doc | none | none |
| 2 | Split `zhx6x`: internal half out, NATS half kept | done 2026-09-16 (`31or7` filed; `zhx6x` rewritten) | none | none |
| 3 | `Ring.Watch`; `marvel events --follow` off its poll | `aae-orc-5ltr3` | none | none |
| 4 | `daemon.stopping`, `daemon.started`, shutdown ordering | `aae-orc-31or7` | `5ltr3` (the proof) | none |
| 5 | The tap; `LAMEDUCK` to `bus.draining`; `Cluster.Name` validation | `aae-orc-zhx6x` | `31or7`, `lgthv`, `umw8p` | authorization live |
| later | Payload slot decision; `--output json` | `aae-orc-l7nui` (P3) | after `5ltr3` lands, for a real consumer to ask | none |

Edges as filed: `31or7 <- 5ltr3`; `zhx6x <- 31or7, lgthv, umw8p`;
`ikxil <- 5ltr3`. No parent, no epic.

**Critical path:** `umw8p` (authorization block, director#4) then `lgthv`
(grant rows) then `zhx6x` (the tap). Everything internal (`5ltr3`, `31or7`)
runs beside that path and finishes before it, so when authorization lands
the tap is a small PR against a bus that already has a watch seam and the
two kinds it projects. The internal bus is not blocked on director.

Nothing today depends on `zhx6x`. Director-side consumers (`7vw44`,
`aubd6`, `bstdv`) hang off `lgthv`, not the tap.

## 9. Harvest debt

Named so the builder and the operator can see what is owed, and where it
already has an owner:

- **The 24oy study's decisions into `question-marvel-otel-architecture`**
  (ring-vs-parallel: accountant carries levels, ring carries transitions;
  the filtered watch as the ring's subscription shape). Owned by
  `aae-orc-mqgf` (the OTEL decision brief), whose close reason on `24oy`
  says "ratification and node harvest are mqgf's act." Not re-filed here.
- **`question-event-ring-as-signal-bus` lacks an edge to anything that
  commits its answer.** The node's edges point at
  `question-session-state-observation`, `question-agent-communication-broker`,
  and `elem-agentic-resource-matrix`; nothing points back from `zhx6x` or
  the study. This document is the recommended answer (option (a), payload
  deferred). When the operator closes `ikxil`, the harvest is: a
  `resolves`-typed edge from the node to this document's finding, and the
  node moves or gains a resolution note. Recorded in `ikxil`'s notes.
- **`question-marvel-service-provider-shape` records one-bus-vs-two as
  open ("A decides").** The 2026-08-01 and 2026-09-16 rulings answer it:
  two, with a tap. The `1oaz` transport-benchmark ticket still lists
  embedded NATS as a lead, which the declined ruling forecloses; re-scope
  when `1oaz` is next touched. Marvel `charter.md:245` still lists embedded
  NATS JetStream among broker candidates; per `charter-light-touch`, that
  is the node's to fix and the renderer's to propagate, not a hand edit.
- **`docs/demo.md:560` says thirty-five kinds; the source has fifty-one.**
  Corrected inside `31or7`, which changes the count again.
- **finding back-reference:** finding-028 is referenced by exactly one node
  (the ring question). Once `5ltr3` lands, the finding that records the
  watch's measured never-block behavior should back-reference 028 as the
  inventory it answers.

None of this is a separate ticket. Each item either has an owner already
(`mqgf`), rides inside a build ticket (`31or7`), or is the operator's close
act (`ikxil`).

## 10. Open questions

1. **Streaming RPC shape.** One long-lived `events` request that streams,
   or a sibling `events.watch` verb. The daemon's RPC layer today is
   request-response over the socket; the builder should pick the shape that
   costs least there and keep the poll as fallback.
2. **Where the tap lives.** In the daemon proper, or in `bus.Supervisor`
   (which already owns the broker's lifecycle and credentials). The
   supervisor is closer to the connection; the daemon is closer to the ring.
   Either is fine; one file, one owner.
3. **`LAMEDUCK` source.** `$SYS` advisory (needs a system-account grant for
   the daemon) versus the supervisor's own knowledge of a drain it
   initiated. The second covers marvel-initiated drains only; an operator
   who sends `nats-server --signal ldm` by hand is invisible to it. Decide
   in `zhx6x`.
4. **`Cluster.Name` validation ticket.** Named as existing by the event-plane
   design; not found by search. Either it exists under a title search
   missed, or it should be filed when `zhx6x` starts.
5. **Watch queue default.** Ring capacity, a fraction, or per-caller only.
   Small; decide in `5ltr3`.
6. **Payload side table trigger.** Which consumer first needs more than
   prose. `l7nui` waits for it.

---

## Appendix A: marvel-builder's locate report (provenance)

Copied verbatim from the scratchpad on 2026-09-16 because the scratchpad is
not durable. Author seat: fleet-marvel-builder-g2-0. Line numbers in it are
against the marvel checkout the builder read; those cited in the body above
were re-checked against `b798131` and match.

> # Marvel internal bus: locate-and-recommend report
>
> Date: 2026-09-16. Author seat: fleet-marvel-builder-g2-0. Method: five isolated
> read passes (marvel/_kos, marvel/docs, marvel code, orc docs and _kos, live bd),
> distilled here. Nothing built, nothing filed.
>
> Scope: the in-process event bus INSIDE marvel. The external NATS director bus is
> named only where our material draws the boundary between the two.
>
> ## Headline
>
> No internal bus exists in marvel. What exists is the bounded event ring
> (marvel/internal/events/events.go: Ring at :319, DefaultCapacity 2000, allKinds
> at :191 with 51 Kind constants, 39 control-plane + 12 agent.*). It is
> write-mostly: Emit, Snapshot, Len only; no Subscribe or Watch; no payload field
> (160-char Message); no daemon lifecycle kind (shutdown() at
> internal/daemon/daemon.go:602 emits only credential.transient-dropped).
> `marvel events --follow` is a one-second poll on SinceSeq
> (cmd/marvel/main.go:523-590). Nothing reaches NATS: the only nats.Connect is
> admin provisioning (internal/bus/provision.go:156). Party H's grounding holds.
> One count drift: docs/demo.md says thirty-five kinds; source has 51 (bus.*
> kinds landed via briefs 9 and 10).
>
> ## 1. What exists and how far it got
>
> Design text, in order of concreteness:
>
> - orc docs/design/utility-event-plane.md section 6 (2026-09-16, ruled, nothing
>   built). The only explicit "internal bus as source, NATS as projection"
>   statement. Ring kind constants are the catalog; tap at warning and above onto
>   events.marvel.<cluster>.>; two new kinds daemon.stopping and daemon.started;
>   $SYS LAMEDUCK translated to bus.draining; precondition Cluster.Name validated
>   to the subject token class. Plan row 5 = aae-orc-zhx6x. Party reports under
>   docs/design/parties-2026-09-16/ (vote 3, 5 to 1: one ring-to-bus tap at
>   warning and above; subject shapes in utility-event-addresses-alignment-with-G.md
>   section 2).
> - marvel/_kos/probes/study-otel-control-loop-signals.md (2026-09-12, study for
>   aae-orc-24oy, closed; node harvest NOT done). The only concrete subscription
>   design. "A subscriber must never be able to block Emit" (Emit runs in the
>   adapter drain goroutine). Compared callback registry (rejected), channel
>   fan-out (acceptable, silent loss), filtered watch (per-subscriber Filter,
>   bounded queue, Seq cursor, drop-oldest on overflow with the gap recorded),
>   recommending filtered watch when a push consumer needs it. Also rules that
>   the harness-data fan-out seam is usage.Accountant, not the ring: the ring
>   carries transitions, the store carries levels.
> - marvel/_kos/findings/finding-028-event-ring-inventory.md (2026-08-25).
>   Measured inventory; "nothing reads the ring"; no payload; no --output json.
> - marvel/_kos/nodes/frontier/question-event-ring-as-signal-bus.yaml
>   (2026-08-25, OPEN, placeholder ticket aae-orc-ikxil P3). Options: (a) ring
>   grows payload and readers; (b) ring stays a display log with a separate
>   signal path; (c) adapter typed events become primary with ring and state
>   machine as two subscribers. Unanswered.
> - marvel/docs/design/service-provider/{README,02-architecture,03-probes-and-roadmap}.md
>   and orc _kos/ideas/marvel-service-provider-architecture.md plus
>   _kos/nodes/frontier/question-marvel-service-provider-shape.yaml (2026-08-15,
>   speculative, RELOCATION-PENDING to marvel/_kos). The "backplane" framing: the
>   internal parts fabric (register, route, supervise, meter); "the reconciler,
>   adapters, and event ring are its first citizens"; "this is not the agents'
>   message bus." Tickets aae-orc-b64o (extract backplane), aae-orc-1oaz
>   (transport benchmark; lists embedded NATS as a lead, which conflicts with the
>   2026-08-01 declined ruling), aae-orc-zsd5 (push vs poll). Dissent recorded:
>   one-bus-vs-two.
> - orc docs/design/state-observation-requirements.md (2026-08-25): R1 typed
>   slot, R2 an active reader; the two requirements a real internal bus satisfies.
> - orc docs/design/director-envelope-and-adapter-events.md (2026-07-05):
>   adapter events transport is "in-process (adapter -> session manager),
>   optionally re-published"; envelope = director, events = marvel.
> - Note: aae-orc-zhx6x's notes say "design in marvel/_kos", but zero zhx6x hits
>   exist under marvel/. The plan lives only at the orc.
>
> Code seams (paths relative to marvel/):
>
> - internal/events/events.go: Kind :19, allKinds :191, Ring :319,
>   DefaultCapacity :331, Snapshot(Filter, n) :398. Writers via the Emitter
>   interface: session.Manager, team.Controller, usage.Accountant,
>   bus.Supervisor, the daemon.
> - internal/daemon/daemon.go: shutdown :602, "events" RPC :819, handleEvents :875
>   (one Snapshot per request; no streaming RPC).
> - cmd/marvel/main.go: events command :449, follow loop :523-590, --list-kinds.
> - internal/session/bridge.go:46-76: ringKind lifts the 12 adapter kinds 1:1
>   onto agent.* ring kinds, flattening payload to prose. Comment at :21-29 says
>   detail "stays in the adapter stream for consumers that subscribe to it"; no
>   such consumer exists.
> - internal/runtime/instance.go:57: per-instance Events() <-chan, the only
>   push-style surface today, single consumer, internal.
> - internal/runtime/events/events.go: the separate 12-kind adapter vocabulary,
>   SchemaVersion 1, drift-guarded against contracts/schema/director-event.schema.json.
> - internal/bus/: render.go, supervisor.go, provision.go (AGENT_INBOX,
>   AGENT_AUDIT, AGENT_STATE). NATS is supervised substrate, not an event sink.
> - No TODO/FIXME mentioning bus, ring, NATS, projection, or subscribe.
>
> Ticket thread (live bd, read 2026-09-16):
>
> k0t created the ring (Apr, marvel PR #25) -> nxs0 adapter vocabulary and
> bridge.go lift (Jul) -> yi02 catalog enumerable and test-locked (Aug, PR #182)
> -> 96st, m8n0 filled emit gaps -> finding-028 and ikxil posed the signal-bus
> question -> brief 9/10 batch (e9g8i, xy1dh, apeoc, 1qyo3, vqq2c, 8br8d; closed
> 2026-09-15, PRs #263-#269) put a marvel-supervised nats-server under the daemon
> with bus.* kinds on the ring -> 2026-09-16 ruling: umw8p (broker authorization,
> open) -> lgthv (grant rows in render.go, open) -> zhx6x (open P2). Director-side
> consumers 7vw44, aubd6, bstdv hang off lgthv. Nothing depends on zhx6x.
>
> Open internal-adjacent: kct (durability story for history, rides under k28s),
> y4w3 (minor), b64o, 1oaz, zsd5, ikxil. Log-ring tickets 1d2, 4wz, 407l are
> internal/logbuf, a different ring; do not conflate.
>
> Gap: ikxil has no dependency edge to zhx6x, though zhx6x commits the ring as
> source of truth without closing the question.
>
> ## 2. The distinction as our material draws it
>
> Internal bus (in-marvel): the ring. In-process, bounded, dies with the daemon.
> The source of truth for marvel's own events; its kind constants are the
> catalog. Consumers are the daemon itself, the events RPC, and (future)
> subscribers through a watch seam. "Marvel owns the events." The backplane
> material calls it the floor the rest stands on, "more core than" the agents'
> bus.
>
> External bus (NATS): "EXTERNAL NATS, marvel-supervised as a declared workload.
> Marvel stays thin. Embedded NATS declined" (ruled 2026-08-01;
> question-agent-communication-broker, marvel/CLAUDE.md, marvel-remap Round 2).
> "Director owns the envelope" (A2A v1.0 per b69n). Identity study: "NATS: a
> projection surface, not an authority." Two tiers: local per cluster, global for
> the director-supervisor channel (R-86). bus-address-hierarchy.md section 3.1:
> marvel events stay cluster-local (events.marvel.<cluster>...), published by a
> daemon not a session, reaching the fleet tier only by supervisor lift or an
> opt-in leaf import.
>
> The seam between them: a tap. Kinds that matter to other agents repeat onto
> NATS; kinds that do not stay internal. "Lifting is explicit, never automatic
> mirroring; the bus carries decisions and signals, not raw telemetry streams."
> The one-bus-vs-two dissent from the service-provider set is answered in
> practice by these rulings (two), but no node records that.
>
> ## 3. Recommended next steps, smallest first
>
> 0. Harvest debt before code (all in marvel/_kos, all cheap): record the 24oy
>    study's ring-vs-parallel decision and the filtered-watch shape in
>    question-marvel-otel-architecture; answer or link ikxil to zhx6x; mark
>    one-bus-vs-two resolved (two) on question-marvel-service-provider-shape;
>    retire charter F12's stale embedded-NATS candidate; fix demo.md's kind count.
> 1. Split zhx6x. It bundles three things with different gates. The internal half
>    (new kinds, a watch seam) needs no NATS authorization; only the tap waits on
>    umw8p -> lgthv. Filing the internal half separately lets the bus start now.
> 2. First build: Ring.Watch per the study's filtered-watch shape (per-subscriber
>    Filter, bounded queue, Seq cursor, drop-oldest with gap recorded, Emit never
>    blocks). Prove it with an existing consumer: switch `marvel events --follow`
>    from the one-second poll to Watch. Zero new surface, one real subscriber,
>    and it tests the never-block contract under the adapter drain goroutine.
> 3. Second: daemon.stopping and daemon.started kinds, emitted internally, with
>    the section-6 shutdown ordering (publish, grace, sessions, then the broker
>    with its own 5s grace). Internal-only; also supplies the missing event named
>    by question-marvel-graceful-stop.
> 4. Third, gated on lgthv: the tap as a second Watch subscriber publishing
>    warning-and-above kinds to events.marvel.<cluster>.<kind>; LAMEDUCK to
>    bus.draining; Cluster.Name validation. This is the remaining zhx6x scope.
> 5. Defer: payload field on Event and --output json (finding-028 R1; needed
>    before any consumer wants more than prose), kct durability, b64o backplane
>    extraction, 1oaz transport benchmark (re-scope to drop embedded NATS).
>
> ## 4. State of the ikxil question (question-event-ring-as-signal-bus)
>
> The question is formally open and its placeholder ticket (aae-orc-ikxil, P3)
> names only the ordering question relative to b64o as its deliverable. In
> practice two later artifacts have leaned on it without closing it. The 24oy
> study (2026-09-12) ruled the harness-data seam is the accountant, not the
> ring, which removes most of option (c)'s motivation, and specified the filtered
> watch as the ring's own subscription shape, which is option (a) minus the
> payload. The 2026-09-16 ruling (utility-event-plane section 6, zhx6x) then
> treats the ring as the source of truth for marvel events and its kinds as the
> NATS subject catalog, which is option (a) again. What remains unanswered is the
> payload half of (a): the Event has no typed slot (finding-028, state-observation
> R1), so a subscriber today receives 160 characters of prose, and the
> bounded-memory contract versus payload is the one open tension the node names.
> My read: the question can close as (a) with the payload deferred to its own
> ticket, and ikxil should gain an edge to zhx6x either way, because zhx6x
> currently commits the answer with no link back to the question.
