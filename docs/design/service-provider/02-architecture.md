# Marvel as a Service Provider: Architecture

Status: speculative design. Nothing here is committed. Captured from a design
session (2026-08-14/15) and written to be argued with, not built from as-is.
See [README.md](README.md) for the document set and cross-links.

Last updated: 2026-08-15

Companion knowledge node (the speculative, kos-native form):
`_kos/nodes/frontier/question-marvel-service-provider-shape.yaml` and
`_kos/ideas/marvel-service-provider-architecture.md`.

---

## The one-paragraph shape

Marvel is a theater. It provides the house, the stage, the wiring behind the
walls, the dressing rooms, the fly loft, and the control booth. sideshow packs
are the script; the agents are the company; the work performed on the stage can
be anything (software engineering, security operations, research, simulation,
roleplay). Marvel's job is to run the building so any company can mount any
show. The architecture below is the building's structure. The analogy names the
rooms; it does not wire them (see the naming-register guardrail in vision value
11, and bd `aae-orc-vxn8`).

---

## Planes and fabrics

Two ideas that are easy to conflate but must stay distinct: a **plane** is a
protocol seam (a surface something talks across); a **fabric** is internal
wiring that carries traffic between marvel's own parts. Planes face outward
(operators, agents). Fabrics face inward.

### Agent-facing and operator-facing planes

- **Control plane** (operator to marvel). Apply a manifest, inspect state,
  rotate a shift, administer remotely over `mrvl://`. This exists today
  (`internal/api`, `internal/daemon`, the embedded SSH server on port 6785).
- **Service plane** (agent to marvel). An agent files a capability request at a
  front-office dropbox and gets an answer. This is the load-bearing new seam and
  it is mostly unbuilt (marvel's `Endpoint` is a name-only record that "waits on
  director"; F12 is unbuilt).
- **Event plane** (push, not poll). Notifications, watchdog alerts, timers, and
  the completion signal for long work. Marvel owns an event ring today
  (12 `agent.*` kinds, non-durable). The agent-facing durable form is an
  external NATS bus supervised by marvel as a declared workload (ruled
  2026-08-01).
- **Trust plane** (identity minting and credential brokering). Deliberately not
  a service in the catalog: it is the surface where an agent's minted identity is
  exchanged for scoped access. See the credential boundary below.

### The two fabrics (the pieces built without their footings)

- **The backplane** (internal parts fabric). How marvel's own parts, its managed
  workloads, and its external integrations register, route to each other, stay
  healthy, and get metered. This is not the agents' message bus. It is more core
  than that: it is the floor the rest stands on. It is described in its own
  section below.
- **The measurement fabric** (internal observation fabric). The dual of the
  backplane: the backplane carries requests between parts; the measurement fabric
  carries observations about parts. Also described below. Less settled than the
  backplane.

The service plane and the event plane are facades over the backplane. An agent
sees a uniform "marvel service"; underneath, the backplane routes the request to
the right part, whether that part is compiled in, a supervised process, or a
remote integration.

---

## The backplane

### What it is

Marvel already does four things for agent sessions: it **registers** them
(the session/team model), **routes** control to them (tmux, adapters),
**supervises** them (the reconciler, healthchecks, shift), and **meters** them
(the usage accountant, admission). The backplane is those same four functions,
generalized from "a tmux session" to "any part."

- **Register**: a part declares its class, its provider, its locality
  (in-process, managed workload, or external integration), a health endpoint,
  and a meter hook.
- **Route**: deliver a typed capability request to the bound part, carrying the
  caller's minted identity, and return a small result (go/no-go plus a handle).
- **Supervise**: liveness and lifecycle. This is the existing Shift primitive
  applied to a part, so rolling a NATS 1.9 to 2.0 upgrade is the same mechanic
  as rotating an agent.
- **Meter**: what the part cost. This is the producer side of the measurement
  fabric.

### Why it was invisible

Marvel already built parts of the backplane without recognizing them. The
reconciler registering sessions, the healthcheck loop, the control routing
through tmux, and the event ring are backplane behavior hardcoded to one kind of
part. The building has a stage sitting on a floor that was never poured
separately, and it held because nothing heavy stood on it yet. Adding a vault
process, a dolt process, a retrieval server, or an inference router is the
heavy thing. Those need the same floor the tmux sessions already stand on.

The practical consequence for building it: do not grow a backplane in a corner.
Extract it from what the reconciler already does. The session registry becomes
the part registry, the control routing becomes the request router, the health
loop becomes part supervision, and the accountant becomes the meter. The four
functions exist; they are specialized to a tmux session, and the work is to
generalize the part.

### What it must be, and must not be

- It **must** be dumb, authenticated, metered, and auditable. It is the most
  trusted path in the system, so it is the least clever. A part registering on
  the backplane is a trust event (this process may now receive routed requests
  carrying minted identity), not a config reload.
- It **must not** have inference anywhere in it. Inference informs planes; it
  never enforces on a plane. A hallucinating router is worse than a hallucinating
  agent.
- It **must** treat internal, managed, and external parts as three registration
  modes on one fabric, not three different systems.

### Transport: buy, do not build

The backplane needs a transport: an in-process path for internal parts and a
light wire for out-of-process parts. This is a category decision, not a vendor
marriage, the same way "it is SQL" lets one move between MySQL, MariaDB, and
Postgres. The minimum shape a candidate must satisfy:

1. Register and deregister a part with class, provider, and locality.
2. Route a typed request carrying minted identity; return go/no-go plus a handle.
3. Report part liveness into supervision.
4. Expose a meter hook into the measurement fabric.
5. Run in-process for internal parts and over a light wire for out-of-process
   parts.
6. Be embeddable, light (not Kafka-weight or Spark-weight), and portable across
   a future Go to Rust move.

Anything an order of magnitude heavier than marvel itself is too mature for this
use; the target is a light dependency that can be replaced later, not a platform
to build the product around. First place to look in a survey: embedded NATS in
library mode, possibly serving both the internal backplane transport and the
agent-facing message bus over different subjects and accounts (one dependency,
two logical planes). That is a lead for the survey probe, not a decision. See
[03-probes-and-roadmap.md](03-probes-and-roadmap.md).

---

## The measurement fabric

### What it is, and how it relates to the backplane

Every part receives requests over the backplane and emits observations into the
measurement fabric. The backplane's meter function is the producer into it. The
event ring is a proto-measurement-fabric today: it already collects signals, and
they die in it. Marvel's adapters already know a great deal (session status,
CPU, RSS, context pressure, which model, which harness, which version); that
data currently has nowhere to flow.

### The stance

Marvel's role is to emit, to route, and to consume; it is never the store.
Admission throttles on context percentage, the caretaker watches, and critic
scores, all as consumers. The store is bought, not built (the OTEL seam as the
emit contract, and the observability-pipeline family as the store).

### The honest open question

It is not yet settled whether measurement is one fabric or several. Logs are
streaming text and behave like messages; metrics are numeric time series;
traces are a third shape. These are different levels of adaptation and a single
fabric may be the wrong unification. This is recorded as open, not resolved. See
the probe guide.

---

## The resource model

Marvel already has most of the nouns. The work is naming and decomposing, not
inventing. Three families, plus config and state.

### Where the built model is right

`Workspace`, `Session`, `Runtime` (the harness, correctly separated from the
agent), `Team`, `Host`, `Healthcheck`, and the event ring exist. `Vault`,
`Pack`, `Volume`, `Schedule`, and `Gateway` are modeled but not built.
Admission (`internal/admission`, PR #101) already refuses over-budget work at
operator verbs, which is the embryo of runtime metering. `Policy` projection is
live: a per-session settings file that re-projects and emits `policy.projected`.

### One word was hiding several: Role becomes Role plus Casting

forestage's B14 (bedrock) defines five primitives: Persona (the costume), Theme
(the roster), Identity (the lens), Role (the job: responsibilities, procedures,
outputs, authority), and Process (the game). Composition is
`Agent = Persona + Identity + Role(s) + LLM + Tools` inside
`Team = Agents + Roles + Process`.

Marvel's built `Role` struct is not B14's Role. It carries name, replicas,
runtime, restart policy, permissions, policy, persona, identity, and budget.
That is not a job; it is a casting decision (this slot, filled by this persona,
wearing this identity, running this harness, at N replicas). The proposal:

- Keep B14's Role exactly as bedrock defines it (the job).
- Rename marvel's struct to **Casting** (name is a bikeshed: Casting, Billet, or
  Slot). It references a Role (the job), a Persona and Identity (B14 ingredients),
  a Runtime, and Tools, at N replicas with a restart policy.
- Move **budget** out entirely. Budget is a QoS and admission concern on the
  workspace or team (the production budget, never the actor's or the part's).
- Route **policy** to Projection and **permissions** to a Sandbox profile.

This is a rename plus an extraction, not a redefinition of bedrock. It touches
forestage and the charter B14 wording, so it is flagged, not slipped in. It can
be built as Casting-holds-everything today and decomposed under pressure as the
seam lands (evolve, do not big-bang).

### One word was hiding two: Endpoint versus Binding

- **Endpoint** is service registration and discovery: a service named X, of
  class C, exists and is reachable. Registered once.
- **Binding** is a scope attachment and resolution: workspace alpha may use X,
  resolved as provider P, with this identity exchange, this projection, and this
  policy. Bound to many scopes.

Register once, bind many. Marvel's current name-only `Endpoint` is the
registration half; the Binding (the resolution half) is unbuilt.

### Two words were one: Policy is a mode of Projection

Projection is the general primitive: everything an agent touches is a
marvel-controlled view (files, service endpoints, credentials, the team roster,
settings). `Policy` projection is the single mode marvel built first
(settings-inject). The modes:

- **readonly**: present, do not allow writes.
- **writeblock**: present, reject writes with an honest error.
- **inject**: place a file or directory that is not in the working tree.
- **redirect**: present a bare command (for example `bd ready`) and route it to
  the resolved provider (dolt-A here, dolt-B there).
- **redact-and-mint**: hide the secret, present a working tool (the credential
  case).

Curtain is the kernel enforcer of these; the projection layer describes them.
Settings management and sandboxing are the same primitive in different modes.

### The write path (an edge worth stating)

Reads are settled. For writes, each projection carries a declared
write-disposition chosen at bind time, from a closed set:

- **passthrough**: the write lands in the real working tree.
- **redirect**: the write lands in the agent's private overlay directory, not
  shared.
- **block**: the write is rejected honestly.
- **capture**: the write lands in a marvel-held staging area for a later gate
  (used sparingly).

Marvel describes the write; it does not become a filesystem. Whether a working
tree is shared or isolated is a declared Volume mode, not an invented concurrency
engine. Collisions on a shared tree are git's already-solved problem. A general
union or overlay filesystem is deliberately out of scope for now (VFS and slotefs
are "study first, not committed" in the roadmap).

### The Verb

A Verb is a named, versioned, deterministic composition of capability requests
with a declared contract (inputs, the bindings it touches, a go/no-go plus handle
result). Its body is a lua utility orchestrating requests; no model is in its
execution path. It is authored by a user, or proposed by the caretaker and
ratified by a human (the automation boundary, ADR-007). A ratified verb
terminates in a durable artifact (a kos finding or a sideshow pack) that teaches
future agents. The learning loop ends in the knowledge and behavior planes, not
in a marvel runtime that mines and mints macros on its own.

### Lua, scoped

Lua is a utility scripting layer, in the spirit of an n8n workflow but in the
Rust and Go world rather than Node. It describes deterministic process
(schedules, loops, conditionals) and, where authorized, calls into the control
plane and services. It is not how marvel is extended, and it is not the
caretaker's judgment. It runs the boring loop so a model does not pay tokens to
count.

---

## The service catalog, filed by lifecycle

The cut that organizes the whole catalog is provisioning-time versus
runtime-request.

### Runtime services (an agent requests these during the show, via the dropbox)

- **bd** (task-graph): Dolt-backed issue and work tracking. The reference
  service; the proving vertical runs through it.
- **kos** (knowledge-graph): orient, query the decided bedrock, open frontier,
  and ruled-out graveyard. Read surface is unbuilt today (this is Gap 0 in the
  vision).
- **flyloft** (retrieval): curated, provenance-carrying, distance-appropriate
  context over MCP. Skeleton today.
- **critic** (evaluation): run registry and arena; the instrument that tells you
  whether a team structure produces value. Pre-code.
- **code-graph** (missing): compiler-grade symbol and reachability facts. Marvel
  is also a consumer: changed-symbol reachability tells the reconciler which
  sessions to invalidate.
- **message-bus** (NATS): publish, subscribe, request-reply, durable consume.

### Provisioning capabilities (these shape the house before the company arrives)

- **sideshow** (behavior packs): installs skills, commands, rules, and hooks into
  a workspace. The agent never calls sideshow; it runs at setup.
- **callbook** (task-graph provider standup and identity enrollment): stands up
  the Dolt service shape and enrolls humans and agents under durable names. Feeds
  the trust plane.
- **curtain** (sandbox profile install): installs the kernel-enforced containment
  profile a workspace's agents run under.

Conflating the two lifecycles is a category error: do not build a runtime API for
a thing that runs once at setup, and do not let an agent re-provision its own
enclosure.

### Capability class, provider, service

A capability class is an interface defined by the coordination pattern and the
delivery semantics the agent depends on, not by a provider's API. For a message
bus the class carries the pattern (fire-and-forget, work-queue, request-reply,
broadcast) and the semantics (ordering, at-least-once, at-most-once,
exactly-once, durability). A provider is a driver of a class (NATS and RabbitMQ
both provide message-bus; dolt-A and dolt-B both provide task-graph). A service
is a configured provider instance. Provider-specific extras are exposed only
through an explicit, opt-in door the agent knocks on by provider name, and
knocking forfeits portability by design. Swapping a provider changes the driver;
the class contract, and every binding above it, is untouched.

The three service "types" (internal, managed workload, external) are not three
types. They are one Service resource with a provider field describing how much of
the service marvel runs, and three registration modes on the one backplane.

---

## The credential boundary

Marvel does not become the long-term secret store. It does broker access, hold
session state on the agent's behalf (for example the OAuth session for a backend
service such as the Claude backend, so the agent does not handle the token), and
run credential-handling services that connect agents to a vault that mints
short-lived scoped credentials. Durable custody is delegated to the vault
service.

The one thing that crosses the wall from the agent's side is its minted identity:
an attenuable token (biscuit-shaped or SPIFFE-shaped). At the binding boundary, a
token exchange trades that identity for a short-lived scoped downstream
credential. The plaintext secret never enters the agent's world.

This corrects SOUL.md section 3, whose "never manages credentials" wording was
too narrow and foreclosed brokering and session-holding by accident. The
single-user versus multi-user routing boundary in section 3 still stands. Tracked
as bd `aae-orc-dqhf`.

---

## Absence has two honest projections

When a capability is not available to an agent, the two cases must not look the
same:

- **Unprovisioned**: the capability is invisible in the agent's world. It is not
  on the roster, the verb is absent, and the agent never forms the intention. You
  cannot miss what was never in your world.
- **Bound but down**: an honest go/no-go result of "unavailable, retry," which
  the agent or the caretaker acts on.

This is the component-independence principle (SOUL section 2, ADR-005) at the
agent's eye level: an unprovisioned capability is invisible, not broken; a
bound-but-down capability is honestly unavailable, not silently wrong.

---

## Module sketch (language-agnostic)

The seam maps onto packages regardless of implementation language. Names are
indicative:

- `identity`: the issuer. Mint, attenuate, verify. Marvel is an issuer here, not
  a name registry.
- `capability`: the class interfaces and provider drivers.
- `binding`: the resolution (route, trust exchange, projection shape, policy).
- `projection`: the one-hat mechanism and its modes; hands curtain the
  kernel-enforcement spec.
- `service`: the registry and the request dropbox (the seam itself).
- the **backplane**: register, route, supervise, meter, extracted from the
  existing reconciler rather than grown separately.

A standing flag: marvel is pure Go today (module `github.com/arcavenae/marvel`,
go 1.25.4, zero Rust). The stated direction of "largely Rust" contradicts both
the current tree and the 2026-08-01 "Go through the push, Rust as satellites"
ruling. The structure above is language-agnostic (each item is a trait or an
interface either way), but the language direction needs reconciliation on paper
before this becomes a build plan. See the probe guide.

---

## What is genuinely new here (against the built model)

- The backplane as an explicit fabric extracted from the reconciler, carrying
  internal, managed, and external parts uniformly.
- The measurement fabric as its dual.
- The service plane (dropbox) with a go/no-go-plus-handle return contract, where
  long results arrive as completion events (the service plane and event plane are
  the two ends of one interaction).
- Binding as a distinct resource from Endpoint.
- Minted identity as an issued, attenuable token, and token exchange at the
  binding boundary.
- Projection generalized beyond settings, with a closed set of read modes and
  write dispositions.
