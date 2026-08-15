# Marvel as a Service Provider (design set)

Status: speculative design. Nothing in this set is committed or proven. It was
captured from a design session (2026-08-14/15) and is written to be argued with.
Where it conflicts with the graph (`_kos/nodes/`) or the roadmap, those win.

Last updated: 2026-08-15

## The question this set answers

How should marvel be structured so it can become a flexible service provider to
agents and teams of agents: extending services (a message bus, a vault, a
knowledge graph, a code graph, an inference router, retrieval, evaluation, task
tracking) into workspaces, over the control plane and a service plane, in a way
that is easy to adapt as this space evolves, and easy to swap one provider for a
better one tomorrow, without marvel becoming a monolith that owns and trusts
everything.

## The three documents

1. **[01-brief-prd.md](01-brief-prd.md)** — why this exists, the theater framing,
   the five actors, the agent's jobs-to-be-done, the proving vertical, the service
   catalog with what each capability should and should not be, cross-cutting
   requirements, and non-goals.
2. **[02-architecture.md](02-architecture.md)** — the structure: planes and
   fabrics, the backplane, the measurement fabric, the resource model with its
   splits (Role into Role plus Casting; Endpoint versus Binding) and merges
   (Policy as a mode of Projection), the credential boundary, projection modes and
   write dispositions, the catalog filed by lifecycle, and a language-agnostic
   module sketch.
3. **[03-probes-and-roadmap.md](03-probes-and-roadmap.md)** — the open questions,
   the buy-not-build surveys, the dissenting views preserved on purpose, a
   sequencing draft against the real roadmap tracks, and the list of documents,
   code, and nodes that may need updating to fit.

Read them as brief then architecture then probes, or jump to the one that matches
your role: an administrator or user wants the brief; a maintainer wants the
architecture; anyone deciding what to build next wants the probes.

## The shape in one screen

- Marvel is the theater: it runs the house; the company (agents) performs the show
  (sideshow packs, the script); the work can be anything. The analogy names the
  rooms; it never wires them.
- Two outward planes exist or are near: control (operator to marvel) and the
  beginnings of service and event planes (agent to marvel). Two inward fabrics are
  the pieces built without their footings: the **backplane** (parts register,
  route, are supervised, and are metered) and the **measurement fabric** (parts
  emit observations). The service and event planes are facades over the backplane.
- The backplane is not the agents' message bus. It is more core: the floor under
  what marvel already built. The reconciler, adapters, and event ring are its
  first citizens, already doing register, route, and health for one kind of part
  (a tmux session). The work is to generalize the part and extract the fabric.
- Marvel already has most of the resource nouns. The work is naming and
  decomposing: rename the built Role struct to Casting (keep B14's Role as the
  job), split Endpoint (registration) from Binding (scoped resolution), and
  recognize Policy as one mode of a general Projection primitive. Budget moves out
  to a QoS resource.
- The catalog is filed by lifecycle: runtime services an agent requests (bd, kos,
  flyloft, critic, code-graph, NATS) versus provisioning capabilities that shape
  the workspace before the agent exists (sideshow, callbook, curtain).
- Marvel does not store credentials, but it brokers them, holds session state on
  the agent's behalf, and connects agents to a vault that mints short-lived
  credentials. The one thing that crosses the wall from the agent is its minted
  identity.
- Buy, do not build, the transport and the measurement store. Keep the seam
  generic, the way "it is SQL" keeps a database swappable.

## Companion knowledge in kos

This is speculative, so in the graph it lives as a frontier question and an idea,
not as findings:

- `_kos/nodes/frontier/question-marvel-service-provider-shape.yaml`
- `_kos/ideas/marvel-service-provider-architecture.md`

If a probe here produces evidence, it becomes a finding and the frontier node is
updated. The overlap between this doc set and the kos artifacts is intentional
and reinforcing.

## Related material (may need updating to fit; see 03 for the full list)

- Platform: `vision.md` (theater framing, bd `aae-orc-vxn8`), `SOUL.md` section 3
  (credential boundary, bd `aae-orc-dqhf`), `docs/roadmap.md` (rev 3),
  `decisions/adr-005`, `adr-007`, `adr-008`.
- marvel: `marvel/charter.md` (B14 wording; F8, F10, F11, F12), the built model in
  `internal/` (api, events, runtime, admission, usage, session, team, daemon).
- Components: `critic/charter.md`, `sideshow/charter.md`, `callbook/charter.md`,
  `flyloft/charter.md`, `kos/KOS-charter.md`, and
  `docs/research/2026-08-08-code-graph-briefing.md`.
- Open bd: `aae-orc-dqhf`, `aae-orc-vxn8`.
