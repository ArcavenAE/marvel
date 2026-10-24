# Non-LLM team members that publish to the bus, and a watchdog that watches them

- **Status:** idea (pre-hypothesis, no commitment). Exploration only: no
  frontier node, probe or ticket, and it may sit in tension with the nodes
  and designs it links.
- **Date:** 2026-10-24
- **Seed (operator's words, via director):** "what if we used marvel to add
  non-llm tooling that could put messages in the bus for it's llm agent team
  members? the github tooling for example that listens for requests for
  resources to monitor and generate event pub/sub? and what if we had a
  watchdog agent that got health and heartbeat/status messages when those
  tools were healthy, but acted when they disappeared or were reporting
  functional faults?"
- **Subject:** marvel (what a team may contain, how a cluster supervises it,
  and who watches the supervisor's helpers). GitHub, bd and director are
  objects here. Filed in the marvel graph per the subject test.
- **Related:** [[services-catalog]] (the ratified service record: class,
  provider, mode, caller identity), [[service-directory]] (how a seat finds
  a service), [[health-signal-taxonomy]] (the signals a watchdog may read),
  [[tmux-harness-state-watchdog]] (a watchdog that classifies a seat's
  state), [[deaf-seat-status]] (a seat that is live and not hearing its
  bus), [[marvel-operational-diagnostic-battery]] (intended versus working
  versus broken), [[mcp-service-provider-and-credential-custody]] (who holds
  a credential a tool needs), and `docs/design/bus-as-service.md`,
  `docs/design/internal-bus.md`, `docs/design/wake-service.md`,
  `docs/design/scheduled-runs.md`.
- **Sister studies, by name only:** the 5ptdd study (headless
  retrospectors, adaptive cadence and flex seats) and the virtual NOC study
  (the same prober and watchdog question, for a network). This idea does not
  repeat either; where they settle something, this one defers.

## The idea, in two sentences

Some team members need no model: a process that watches one thing and says
what it saw on the bus, so the LLM members read events instead of polling an
API and spending quota to find out nothing changed. A second non-LLM member,
the watchdog, listens for those publishers' heartbeats and status, stays
quiet while they are healthy, and acts when one goes silent or reports that
it cannot do its job.

## Part one: tools that publish for the team

### The shape

A non-LLM member is a seat or service whose job is to turn something outside
the bus into messages on it. The worked example is a GitHub watcher:

1. An agent asks for a watch by sending the watcher a request on the bus,
   such as "watch PR 123 in repo X" or "watch repo X for label Y".
2. The watcher holds the subscription, polls or receives webhooks from
   GitHub once on behalf of everyone who asked, and publishes an event when
   something it was asked about changes: a review, a check result, a label,
   a merge.
3. The requester reads one event instead of running `gh` on a loop. Ten
   seats waiting on one PR cost one GitHub query, not ten.

The gain is quota and noise. Agents that poll `gh` today spend API rate
limit and model tokens to learn that nothing changed, and each poll that
finds something is a turn the agent paid for. An event arrives only when
there is something to read.

### What is not decided

- **The request language.** A subscription needs a name, a target, a filter
  and a lifetime ("until merged", "for two hours", "until I cancel"). The
  smallest form that works for PRs and labels is open. Whether a request can
  name something the watcher has not been told to watch, or only entries in
  a list an operator maintains, is part of the authority question below.
- **Delivery.** An event can go to the requester's inbox, to the team's
  stream, or to a subject any member may subscribe to. The bus designs
  already cover addressing and replay, so this idea should reuse them
  rather than invent a channel. See `docs/design/internal-bus.md` for the
  ring and its watch seam, and `docs/design/wake-service.md` for how marvel
  already publishes states it tracks.
- **The first publisher.** GitHub is the example because the cost is
  visible. A bd watcher, a CI watcher, a disk or quota watcher and a clock
  that publishes "the hour" are the same shape and may be better first
  steps. The point of the idea is the pattern, not the one tool.
- **Where the line is with scheduled runs.** `docs/design/scheduled-runs.md`
  puts a schedule on a headless role. A watcher is close to that: a role
  that wakes, looks, and publishes. The difference is that a scheduled run
  usually starts a model and a watcher never does, and whether that
  difference earns a separate kind is an open question.

## Part two: a watchdog for the publishers

### The shape

Each non-LLM member emits two kinds of message, both on the bus:

- a **heartbeat**: "I am here", at a cadence it declares;
- a **status**: "I am doing my job" or "I am not, and this is the fault",
  such as GitHub returning authorization errors, rate limit exhausted, the
  subscription table full, or its own input closed.

The watchdog subscribes to both and keeps one row per publisher: last
heartbeat, last status, declared cadence. While every row is fresh and
healthy, it publishes nothing. It acts on two triggers:

1. **Silence:** a heartbeat is missing for longer than the member's declared
   cadence plus a tolerance.
2. **Fault:** a status reports that the member cannot do its function even
   though it is alive.

Silence and fault are different failures. A process that is alive and
reporting a fault needs its cause fixed, and restarting it may only repeat
the fault. A process that has gone quiet may be dead, wedged, or cut off
from the bus. [[deaf-seat-status]] makes the same point for a seat: from
inside, quiet and broken look the same, so the evidence has to come from
outside.

### What "acts" could mean (left open)

Three candidates, which are not exclusive and are probably tiered:

- **Alert:** publish a message to an agent or a supervisor that names the
  publisher, the trigger and the evidence, and let a model or a person
  decide.
- **Restart:** ask marvel to restart the seat or service, through the same
  lifecycle path an operator would use. This is the cheap response to
  silence and a poor one to a fault that will recur.
- **Escalate:** after a restart fails or a fault repeats, move up: tell the
  supervisor, then the operator, with the history attached.

Which of these happens for which publisher, and how many restarts come
before an escalation, looks like a declaration on the publisher's own
record, not a rule in the watchdog. That keeps the watchdog small and keeps
the policy next to the thing it concerns.

### Overlap with what exists

Marvel already has the pieces of this in other forms, and the idea may turn
out to be mostly a name for connecting them:

- [[tmux-harness-state-watchdog]] classifies a seat's state from its pane.
  This watchdog reads declared messages instead of scraping, which is a
  stronger signal and only available for members that cooperate.
- [[health-signal-taxonomy]] already separates what any process offers
  (alive, exit code) from what a cooperating one offers (structured status).
  A non-LLM member written for marvel is the cooperating end of that
  spectrum by construction.
- `docs/design/bus-as-service.md` defines a structural-health contract for
  the bus. A watcher's heartbeat is the same idea one level up: structure
  of the member, not content of its work.
- `docs/design/wake-service.md` already publishes states marvel observes
  and says who may act on them. The watchdog's alert is a consumer of that
  surface, or a second kind of wake.

## Open questions

1. **Identity and authority of a non-LLM publisher.** An agent's identity
   on the bus says which seat spoke. A watcher is not a seat in the usual
   sense and holds no model, but its events will be believed: a false
   "PR merged" can change what an agent does. What does it sign with, who
   minted that, and can a consumer tell a watcher's event from an agent
   claiming to be one? Related, and still open: the same question for any
   service that publishes (see [[services-catalog]], the caller-identity
   field, and [[mcp-service-provider-and-credential-custody]]). The watcher
   also needs a credential for GitHub. Marvel may broker a short-lived one
   but must not hold a long-lived grant (SOUL section 3, ADR-009), so
   where the watcher's GitHub token lives is not marvel's to answer alone.
2. **Seat, catalog service, or headless role.** Three homes are plausible,
   and each pulls in different machinery:
   - a **seat** gets presence, restart and the existing health columns, but
     seats are shaped around a harness and a pane, and a watcher has
     neither;
   - a **services-catalog service** gets class, provider and mode, and a
     driver that reports health, but the catalog today describes things a
     cluster depends on (a broker), not things that serve a team;
   - a **headless role** gets a clock and the completion semantics of
     ADR-010 and `docs/design/scheduled-runs.md`, but a watcher is meant to
     stay up, and a finished run holds its replica slot, which is the
     opposite of a long-lived listener.
   It may also need a fourth kind. Which one is chosen decides who restarts
   it and what `marvel get` shows for it.
3. **Who watches the watchdog.** If the watchdog goes silent, nothing
   notices, and the silence it was built to catch moves up a level. Options
   to weigh: the watchdog heartbeats to marvel, which is already outside
   every seat and can restart it; two watchdogs watch each other; or a
   supervisor seat treats the absence of the watchdog's own heartbeat as
   the first alert. Each ends somewhere, and the end is a human or marvel's
   own process supervision. Where it ends is part of the answer.
4. **Subscription state.** A watcher that restarts has lost the list of what
   it was asked to watch. Whether requests are replayed from the bus,
   re-sent by requesters, or held in a store the watcher owns changes how
   much a restart costs, and so changes what the watchdog may do on silence.
5. **Cost of the watchers themselves.** One watcher polling GitHub for many
   requesters can still exhaust a rate limit, and a heartbeat per member per
   cadence is traffic. Cadence belongs with the 5ptdd study's adaptive
   cadence question and is left there.
6. **Trust in a heartbeat.** A heartbeat proves the process can publish, not
   that it can do its job. The status message exists for that gap, but a
   member that reports healthy while broken is the failure the
   [[marvel-operational-diagnostic-battery]] exists to catch. A positive
   probe (the watchdog asks for a canary subscription and checks that the
   event arrives) is the stronger form and costs more. The virtual NOC study
   has the same prober question for a network, and this idea defers to it on
   probes.

## Why this might not be worth doing

- Agents may tolerate polling if the quota cost stays small. The case for a
  watcher is strongest when several seats wait on the same target.
- A watchdog is a new place for policy to hide. If restart-on-silence
  becomes a default, it may restart a member whose silence was correct.
- Adding non-model members to a team changes what a team is. Today every
  member is an agent, and tooling that is not may belong beside the team,
  as a service it uses, rather than in it.
- The idea can be built in parts. A publisher with no watchdog is useful;
  a watchdog with no publishers is not.

## What to do with this

Nothing yet. It is here so the operator's question has a place and so the
two sister studies can cite it. If the pattern survives them, the next step
is a frontier question on the home of a non-LLM member (open question 2),
since that choice decides most of the rest.
