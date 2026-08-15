# Marvel as a Service Provider: Probes, Open Questions, and Roadmap Draft

Status: speculative design. Nothing here is committed. Captured from a design
session (2026-08-14/15). See [README.md](README.md), the
[brief and PRD](01-brief-prd.md), and the [architecture](02-architecture.md).

Last updated: 2026-08-15

This document holds what is not settled: the open questions the design must state
honestly, the buy-not-build surveys, the dissenting views worth preserving, and a
sequencing draft against marvel's real roadmap tracks. It is a guide to next-step
probes, not a plan of record.

---

## Open questions (load-bearing, unresolved)

### Q1. The backplane transport (buy, do not build)

The single biggest undecided thing. The backplane needs an in-process path for
internal parts and a light wire for out-of-process parts. The decision is a
category choice, replaceable later, not a marriage. The minimum shape is in
the architecture doc. The probe is a market survey against that shape.

- Candidates to survey by category: an embeddable pub/sub or RPC library, a light
  message transport, an actor or channel runtime. First lead worth checking:
  embedded NATS in library mode possibly serving both the backplane transport and
  the agent bus over separate subjects and accounts.
- Disqualifier: anything an order of magnitude heavier than marvel (Kafka, Spark).
- Constraint: portable across a future Go to Rust move; embeddable; light.
- This is a survey probe. It is captured here rather than filed as a bd ticket,
  per the three-layer capture rubric (local first; escalate to a work-queue item
  only when committing to close it on a timeframe).

### Q2. Is measurement one fabric or several?

Logs (streaming text, message-like), metrics (numeric time series), and traces are
different levels of adaptation. A single measurement fabric may be the wrong
unification. Probe: sketch the emit contract (OTEL) and the consumer needs
(admission throttling, caretaker watch, critic scoring) and see whether one
pipeline or several fall out. Relates to the vision's measurement gap and to
usher's telemetry vision.

### Q3. The reference work-unit (the "official kilogram")

Velocity is unmeasurable without a unit. A calibrated, fixed task-set that a team
runs so that "this config does N reference-units per token" becomes a real
sentence. Probe: define a first reference task-set. Foundation stone; not this
quarter. Value-per-seat depends on this and is deferred behind it.

### Q4. Language direction (Go versus Rust)

Marvel is pure Go today (module `github.com/arcavenae/marvel`, go 1.25.4, zero
Rust). The stated direction "largely Rust" contradicts both the tree and the
2026-08-01 ruling ("Go through the push; Rust as satellites; VFS or slotefs
first"). The architecture is language-agnostic, but this needs reconciliation on
paper before a build plan. Decision, not a probe: the operator reconciles the
ruling.

### Q5. How much to slim the resource model now versus later

Renaming Role to Casting and extracting budget, policy, and permissions is agreed
in direction. Marvel ships Role-as-god-struct today. Open: refactor before
building the seam, or build the seam alongside and let the struct decompose under
pressure. The session's lean was evolve, not big-bang: ship Casting-holds-
everything, extract under pressure. Touches forestage and charter B14 wording;
flag before doing.

### Q6. The service-plane request contract on the wire

The dropbox seam is designed at the level of "go/no-go plus a handle, completion
as an event." Unspecified: the concrete request envelope, the handle format, the
relationship to the director envelope draft (which owns `sender.principal`,
correlation id, and a FIPA-ACL performative). Probe: reconcile the service-plane
request with the director envelope so they are one envelope family, not two.

### Q7. The capability-class contract boundary

Swapping a provider invisibly requires the class contract to carry coordination
pattern plus delivery semantics (ordering, at-least-once, at-most-once,
exactly-once, durability), not just method names. Open: where exactly the line
sits between the class contract and provider-specific extras, and how the opt-in
"by provider name, forfeits portability" door is expressed.

### Q8. The write path through a shared working tree

Reads and the closed set of write dispositions are settled in direction. Open:
the concrete behavior when two workspaces share a Volume and both write, beyond
"it is git's problem." Probe when a real shared-tree case appears; do not build a
concurrency engine speculatively.

---

## Dissenting and minority views (preserved on purpose)

- **The caretaker with authority.** The maximalist view wanted an
  inference-backed majordomo that takes actions (restart the stuck agent), not
  only proposes them. The room ruled against it: inference informs any plane;
  inference never enforces on a plane; the caretaker proposes and a deterministic
  gate executes. Recorded because the pressure to let the caretaker act to save a
  human an interruption will recur, and the ruling should be re-read when it does.
- **Value-per-seat now.** One view wanted value measurement designed in from the
  first preview. The counter (which held): there are too many leaps to value-per-
  seat today; lay the measurement foundation (make adapter signals flow) and the
  reference work-unit first. Value-per-seat is the cathedral bell, hung after the
  tower stands.
- **Learned verb-assembly by inference.** The ambitious view wanted marvel to
  observe traces and mint macros on its own. The ruling: verbs are deterministic
  and human-authored or human-ratified; the learning loop terminates in a kos
  finding or sideshow pack, not in a marvel runtime. Recorded because the demo
  appeal of self-learning verbs is real and will be argued again.
- **One bus.** It is not settled whether the internal backplane transport and the
  agent-facing message bus are one embedded dependency (NATS, two subjects and
  accounts) or two. The session leaned toward exploring one; Q1 decides.

---

## Sequencing draft (against the real roadmap tracks)

This maps the design onto the adopted roadmap (rev 3, 2026-08-01) tracks M
(marvel), P (packs), V (integrating build), K (knowledge), E (evaluation). It is
a draft ordering, not a commitment, and it defers to the roadmap where they
differ.

1. **Prove the vertical (Track V1, demo Meter-and-Admit then Coordinate).** Build
   `bd ready` through minted identity, one Binding, the task-graph provider,
   returning go/no-go plus a handle, result as a completion event. This validates
   the seam with a capability that already exists.
2. **Extract the backplane from the reconciler (Track M).** Generalize the part
   beyond a tmux session: part registry, request router, part supervision (Shift
   applied to a part), meter hook. Do not grow it in a corner. This underlies M3
   (self-lifecycle) and M4 (auto shift triggers).
3. **Settle the transport (Q1 survey), then the bus (Track M2).** The bus is an
   external NATS workload supervised by marvel; if the survey lands on embedded
   NATS for the backplane too, one dependency serves both planes.
4. **Name the resource model (Q5).** Rename Role to Casting, extract budget to a
   QoS resource, add Binding as distinct from Endpoint, generalize Policy into
   Projection. Evolve, do not big-bang.
5. **Reconcile the credential boundary (bd `aae-orc-dqhf`).** Rewrite SOUL section
   3; specify token exchange at the binding boundary; keep the single-user versus
   multi-user routing boundary.
6. **Make measurement flow (Track E foundation, Q2).** Route adapter signals out
   of the event ring over an OTEL seam to a bought store; wire admission, the
   caretaker, and critic as consumers. Critic (E1) is the early unblock.
7. **Then the wider catalog.** kos read surface (Gap 0), flyloft phase 0,
   code-graph ingest, each as a provider behind its capability class.

Foundation stones deferred behind the above: the reference work-unit (Q3) and
value-per-seat.

Gates carried from the roadmap: M5 and M6 (multi-host, local model runtimes) stay
behind the bet tripwire (2026-10); nothing here changes that.

---

## Documents, code, and nodes to evaluate for updates

Cross-references that this design bears on, each of which may need updating to fit
(flagged, not yet changed):

- **SOUL.md section 3** (auth delegation): too narrow; redefine per bd
  `aae-orc-dqhf`.
- **vision.md** (the "city of agents" framing): the theater framing reads better;
  revise per bd `aae-orc-vxn8`. Keep vision value 11 (function first, analogy as
  annotation).
- **marvel/charter.md**: stale in the built-state review (still says "three
  adapters," still leads with the k8s mapping). B14 wording is touched by the Role
  to Casting rename. F8 (Gateway), F10 (permission model), F11 (stream
  attachment), F12 (agent communication) all bear on the seam and the backplane.
- **docs/roadmap.md** (rev 3): the sequencing draft above should be reconciled
  into Tracks M, P, V, K, E rather than kept separate.
- **decisions/adr-008** (pack management phased sideshow and marvel) and
  **adr-002** (superseded): the provisioning-versus-runtime cut and sideshow's
  place as a provisioning capability should be checked against these.
- **decisions/adr-007** (automation boundary): the "inference informs, never
  enforces" line and human-ratified verbs are instances of this ADR; cross-link.
- **decisions/adr-005** (component independence): the two honest projections of
  absence are this ADR at the agent's eye level; cross-link.
- **Component charters**: critic, sideshow, callbook, flyloft, and kos each get
  placed as a capability (runtime) or a provisioning capability here; their
  charters should carry a back-reference.
- **docs/research/2026-08-08-code-graph-briefing.md**: code-graph is placed here
  as a missing runtime capability and a marvel-internal input; reconcile with its
  open questions.
- **The agentic-resource-matrix idea** (`_kos/ideas/marvel-agentic-resource-
  matrix.md`): budget-as-QoS, measurement, and the enforcement loci connect
  directly.
- **bd tickets**: `aae-orc-dqhf` (SOUL section 3), `aae-orc-vxn8` (theater
  framing docs). Further probes (Q1 transport survey, Q2 measurement fabric, Q6
  envelope reconciliation) are captured here and become bd items only when work is
  committed to a timeframe, per the capture rubric.

---

## kos companions

The speculative knowledge from this design lives in kos as a frontier question
and an idea, not as findings (nothing here is proven):

- `_kos/nodes/frontier/question-marvel-service-provider-shape.yaml`
- `_kos/ideas/marvel-service-provider-architecture.md`

When a probe here produces evidence, it becomes a finding and the frontier node is
updated or partially resolved.
