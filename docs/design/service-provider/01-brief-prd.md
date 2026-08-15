# Marvel as a Service Provider: Brief and PRD

Status: speculative design. Nothing here is committed. Captured from a design
session (2026-08-14/15). See [README.md](README.md) for the set and cross-links,
and [02-architecture.md](02-architecture.md) for the structure this brief
motivates.

Last updated: 2026-08-15

---

## Why this exists

Marvel today is a control plane that launches and supervises agent sessions. The
next thing it is being asked to be is a service provider to those agents: the
place an agent goes to get work done that it should not spend its own tokens and
its own context doing. An agent that carries credentials, polls for status, parses
walls of tool output, and re-runs the same seven-call sequence forty times a day
is burning the scarce resource (tokens and context) on plumbing. Marvel can hold
that plumbing behind a small request seam and hand the agent back a result.

The prize is not one service. It is the right structure for adding most services:
a message bus today, a different bus tomorrow, a vault, a knowledge graph, a code
graph, an inference router, a retrieval substrate, an evaluation service. The
design question is the shape that makes these pluggable, swappable, and safe,
without marvel becoming a monolith that owns everything and trusts everything.

---

## The framing

Marvel is a theater. It provides the building; the company performs the show; the
show can be about anything. The value of stating it this way is that it keeps the
scope honest: marvel runs the house, it does not write the play. The work an
agent team performs (software, security operations, research, simulation,
roleplay) is the playwright's and the company's business, carried in as sideshow
packs (the script). Marvel provides the stage, the wiring, the dressing rooms,
the booth, so any company can mount any show. The analogy names the rooms; it
never drives the technical design (guardrail per vision value 11; bd
`aae-orc-vxn8` tracks adopting this framing in the vision and README docs).

---

## Actors

Five actors, unequal, each with a different way to adapt the system. Naming them
is what keeps the design honest about who needs what.

| Actor | What they do | How they adapt the system |
|---|---|---|
| Maintainer | Writes a new provider driver for a capability class | Code |
| Administrator | Wires a workspace to a service | Manifests and bindings, no code |
| User | Defines a verb, sets a budget | Config, lua, budgets |
| Workload agent (the company) | Files a capability request at runtime and gets a result | The service-plane dropbox |
| Caretaker (the majordomo) | Keeps the cluster in its desired state; serves no actor | Behind the wall; deliberately dull; out of scope for this brief except as a boundary |

The workload agent is the actor the system is most for and, until this design,
the one it was designed around rather than for. This brief centers the agent's
request.

---

## Jobs to be done

Stated from the agent's point of view, because that is the actor the seam serves.

1. **Do this for me without spending my tokens.** Run a task off the agent's
   process and return a small result (go/no-go plus a handle), with full detail
   cached and read only on failure.
2. **Tell me when it is done; do not make me wait or poll.** Long work returns
   as a completion event the agent already subscribes to.
3. **Do not make me handle credentials.** The agent presents who it is; access is
   brokered on its behalf; the secret never enters its world.
4. **Give me the same command everywhere.** `bd ready` means dolt-A in one
   workspace and dolt-B in another; the agent does not learn the difference.
5. **Do not show me a capability I cannot use, and do not lie when one is down.**
   Unprovisioned is invisible; bound-but-down is an honest unavailable.
6. **Let me combine the sequence I run constantly into one verb.** One call
   prepares a PR and updates the bd ticket, returns go/no-go, caches the rest.

Two jobs belong to the operator and the maintainer, and they shape the same seam:

7. **Let me swap a provider for a better one tomorrow** without touching the
   agents or the bindings above it.
8. **Let me meter what every part costs** so the house can eventually be run on
   value, not motion.

---

## The proving vertical

The whole design should be validated by one thin slice before it is generalized.
The slice: `bd ready`, run through a minted identity, one Binding, the task-graph
provider (dolt-A), returning go/no-go plus a handle, with the result arriving as
a completion event. It exercises every plane (identity crosses the wall, the
binding routes and does the token exchange, the projection redirects the bare
command, the service seam takes the request, the event plane returns the result).
None of it is speculative: bd exists, dolt exists, the event catalog exists. If
`bd ready` works end to end through that stack for one agent in one workspace, the
architecture is validated and the rest is more drivers. If it cannot, the elegance
was a trap. This slice maps onto Track V1 and the demo's Meter-and-Admit and
Coordinate acts in the roadmap.

---

## Capabilities, and what each should and should not be

The service catalog is filed by lifecycle. Full structural detail is in the
architecture doc; this is the product view of what each capability is for.

### Runtime services (requested during the show)

- **Task-graph (bd)**: the agent's work ledger. Should be a redirected bare
  command resolved per workspace to a Dolt provider. Should not require the agent
  to know which Dolt instance it is on.
- **Knowledge-graph (kos)**: orient and query decided bedrock, open frontier, and
  ruled-out graveyard. Should gain a read and query surface (this is the vision's
  Gap 0). Should not be summarized in place; artifacts stay verbatim.
- **Retrieval (flyloft)**: curated, provenance-carrying context at the right
  distance. Should be a query the agent makes and gets ranked results with
  provenance. Should not be an uncurated dump.
- **Evaluation (critic)**: the instrument that tells you whether a team structure
  produces value. Should be non-optional foundation, not a late add. Should not be
  the thing that gates promotion automatically (promotion stays a human act,
  ADR-007).
- **Code-graph (missing)**: compiler-grade symbol and reachability facts. Should
  be a queryable capability and a marvel-internal input (reconciler
  invalidation). Should not be a new graph database marvel operates; it is an
  ingest pipeline into existing stores.
- **Message-bus (NATS)**: the coordination fabric for a team. Should be defined by
  coordination pattern plus delivery semantics. Should not leak a provider's
  peculiarities into the agent's assumptions.

### Provisioning capabilities (shape the house before the company arrives)

- **Behavior packs (sideshow)**: install skills, commands, rules, hooks into a
  workspace. Should run at setup with real provenance (signed packs). Should not
  be a runtime API an agent calls.
- **Provider standup and identity enrollment (callbook)**: stand up the Dolt
  provider and enroll humans and agents under durable names. Should feed the
  trust plane. Should not be conflated with the runtime task-graph service that
  uses the provider.
- **Sandbox profile (curtain)**: install the kernel-enforced containment a
  workspace runs under. Should be declared per role or workspace and enforced by
  the kernel. Should not put the policy language in the trusted path.

---

## Cross-cutting requirements

- **Credential handling**: agents do not handle credentials; marvel brokers and
  holds session state on the agent's behalf; durable custody is delegated to a
  vault service. Corrects SOUL section 3 (bd `aae-orc-dqhf`).
- **Identity**: agents get a minted, attenuable identity. Token exchange at the
  binding boundary turns identity into scoped downstream access.
- **Budget, QoS, throttling**: a production-budget concern on the workspace or
  team, not a property of a role or a job. Admission (PR #101) is its embryo.
- **Measurement**: marvel emits, routes, and consumes observations; it is never
  the store. A calibrated reference work-unit (a fixed task-set, the "official
  kilogram") is a prerequisite for measuring velocity, and is a foundation stone,
  not this quarter's work.
- **Swappability**: capability class (interface) versus provider (driver) versus
  service (configured instance). Swap the provider, keep the class contract.

---

## Non-goals

- **Marvel is not a credential store.** It brokers and holds session state;
  custody is the vault's.
- **Marvel does not write the play.** The work is the company's; marvel is
  workload-agnostic.
- **Marvel does not build its own message bus, metrics store, or log store.** It
  buys the category and keeps the seam generic.
- **The caretaker (majordomo) is not in scope here** beyond being a boundary. It
  is deliberately dull, behind the wall, and never extended into services.
- **No runtime verb-learning by inference.** Verbs are deterministic and
  human-authored or human-ratified.
- **No general union or overlay filesystem now.** Projection describes writes with
  a closed set of dispositions; VFS is study-first.

---

## What this brief depends on being true

Stated so it can be checked rather than assumed:

- Marvel's existing resource model can be extended (rename Role to Casting,
  extract budget, add Binding) without a rewrite. The architecture doc argues
  this is a rename plus extraction, not a bedrock change.
- A light, embeddable, language-portable transport for the backplane exists to be
  bought. This is a survey probe, not a proven fact.
- The proving vertical can be built on today's marvel plus bd plus the event
  catalog. This looks true from the built-state review but has not been built.
