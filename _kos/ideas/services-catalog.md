# Services: the catalog of what marvel may configure or supervise, mapped to the ratified record

- **Status:** idea (pre-hypothesis, no commitment). Operator request relayed
  via director, 2026-09-26.
- **Subject:** marvel (what it may configure, supervise or reach, and how a
  seat asks for it). Each service's own project is an object. Filed in the
  marvel graph per the subject test.
- **Related:** `question-marvel-service-provider-shape` (the plane, fabric
  and resource shape; this idea is a catalog under it, not a second frame),
  `elem-agentic-resource-matrix` (services are how several matrix rows get a
  marvel surface), [[question-bd-managed-extended-service]] (bd is one entry
  here, worked through in depth), `docs/design/services-list.md` and
  `docs/design/adopted-services-2026-09-16.md` (the ratified record shape),
  `docs/design/bus-as-service.md`.

## Premise check: the word is already taken, and taken well

"Services" is already marvel's ratified name (services-list.md, direction
ratified 2026-09-16; code in `internal/service`). A cluster carries a
`services:` list. Each entry has a `class` (the interface a consumer depends
on, never a provider's API), a `provider` (the driver that renders, spawns,
health-checks and mints), a `mode` (`managed`, `adopted`, `external`;
`internal` reserved for an in-process provider) and a `caller_identity`
defined by the class contract (`none`, `api-key`, `session-cert`). Health is
a status the driver reports, not a config field. Registered today: class
`message-bus`, provider `nats-server`. Reserved, unregistered:
`inference-gateway` (litellm) and `model-endpoint` (PAIR, Switchyard); dolt
sql-server is also a name on record.

So this idea adopts that vocabulary rather than coining a parallel one.
"Add-ons, extensions and plugins" all land in one record: an add-on is an
entry, an extension is a class marvel adds behavior to (as bd would be), and
a plugin running inside the daemon is the reserved `internal` mode. The
parent node's Binding (a per-team or per-session attachment to an entry)
remains the unit a seat sees.

## The observation: a catalog, mapped

| Operator's name | Proposed class | Likely mode | Status in the record |
|---|---|---|---|
| nats (the marvel bus) | `message-bus` | managed | registered, entry one |
| litellm (router) | `inference-gateway` | adopted now, managed gated on counts | reserved |
| pair (backend) | `model-endpoint` | adopted | reserved |
| switchyard (router) | `model-endpoint` | external, by URL | reserved; the operator calls it a router, the record files it as an endpoint, worth reconciling |
| bd (tasks) | `task-graph` | managed or adopted, per topology | name on record (dolt sql-server); see the bd question |
| vault | `secret-manager` | open | the open sub-question in services-list.md section 4 |
| stagekeeper | undecided | undecided | today an error-patterns store; its class is not yet named |
| a scheduler | `scheduler` (new) | managed | not on record |
| push notifications, plus registration of special services for efficient notification | `notification` (new) | managed or external | not on record |
| a cluster-wide GitHub and GitLab event watcher (a future Rust tool) | `forge-events` (new) | managed | not on record |

The last row carries the plainest payoff. Seats today poll `gh` and `glab`
to learn that a review landed or CI finished, and each poll is tokens spent
on a question whose answer is usually "no change". One watcher per cluster,
subscribed to forge events and publishing them on the bus to the seats that
asked, turns N pollers into one subscriber. That is a metered-resource win in
the matrix's own terms (token spend, attention), not a convenience.

## Tensions and open questions

Some of the operator's questions are already answered by the record; the
idea should not reopen them:

- **What a service declares.** Answered for the record: name, class,
  provider, mode, caller identity, provider body; health is driver status.
  Still open: what each NEW class's contract promises a consumer (a
  scheduler's delivery semantics, a notifier's at-least-once or not, a forge
  watcher's replay window).
- **Credentials.** Answered in principle: `caller_identity` sits on the
  class, and SOUL section 3 with ADR-009 bounds custody. Open where the
  record says it is open: the secret-manager target, which is also where a
  vault entry would land.

Genuinely open:

- **Which manifest enables a service.** Today services are a cluster-level
  client-config list. Is a team manifest ever allowed to ask for one, or
  only to bind to one the cluster already declares?
- **How a seat discovers and requests one.** Binding as a resource is
  undecided (services-list.md section 5). The parent's dropbox and Binding
  are the candidate path; a seat asking for forge events for one repo is the
  first request shape that is not a static URL.
- **Managed versus external, per entry.** The mode column above is a guess
  per row, and for bd it depends on the unruled topology (aae-orc-k41dg).
- **Every service is optional.** marvel runs with an empty list, and a seat
  whose service is absent sees the parent's two honest projections
  (unprovisioned, or bound-but-down). This should be a stated invariant
  with a test, not an accident of `omitempty`.
- **Class names.** `task-graph`, `scheduler`, `notification` and
  `forge-events` are proposals. Registering any of them is a structural
  change with a driver ticket, per services-list.md.
- **Stagekeeper.** Its role is not decided, so it has no class yet. If it
  stays a pattern store that seats write errors to, it may be a consumer of
  `notification` rather than a service of its own.
