# Seat modes: when a seat runs headless, sits on hot standby, or stands

- **Status:** idea (pre-hypothesis, no commitment). Captured, not designed.
- **Date:** 2026-10-22
- **Origin:** the operator, relayed by director.
- **Subject:** marvel (how a seat's mode is chosen and changed), with director as the likely router. Filed in the marvel graph per the subject test.
- **Related:** [supervisors-as-load-balancers.md](supervisors-as-load-balancers.md), which is about how many workers a team has. This note is about what kind of seat each one is.

## The operator's words

> "kos idea marvel, director perhaps there is some way to leverage supervisor-as-loadbalancer to also route and decide when some agents should spin up as headless, and when they should be transitioned into standing/hot-standby agents to reduce the churn, maybe pair a reviewer and builder on a single PR to loop fast, to coordinate over nats directly?"

## Why a sibling and not an extension

The load-balancer note asks how many seats a team should have and who may change that. This one assumes the count is settled and asks what each seat is while it exists. The two share a router (a supervisor) and several signals, so each links to the other, but they can be accepted or dropped independently.

## The three modes

- **Headless on demand.** A seat is started for one piece of work, runs to completion and ends. Cheap to hold, expensive to start: every run pays the cold start and reloads its context. Completion semantics for a headless run are in ADR-010 and `_kos/findings/finding-015-headless-completion-is-reported-as-a-crash.md`.
- **Hot standby.** A seat is up and holds its context but has no turn in flight. It answers quickly and costs the standing context, not a cold start. `_kos/ideas/context-pressure-is-an-operating-point-not-a-fill-level.md` already discusses a hot standby as a replica that exists before it is needed, and whether its carrying cost is worth it.
- **Standing.** A seat that stays up across many items as a long-lived role, with its own shift and context cycle.

The idea is that a seat's mode is a choice made from signals, not a fixed property of the role. A role that is always headless today could stand when its work arrives in bursts, and a standing seat that idles for hours could drop to headless.

## Churn this aims to cut

- Cold starts: a headless seat started and ended many times in a day for small items.
- Reload cost: context rebuilt each time instead of kept.
- Hand-offs: work that waits for a seat to be started, or a seat that is started for one item and ended before the next arrives.

## Signals a supervisor could use

Each is a candidate, not a chosen rule.

- **Queue depth.** Items waiting for the role. A run of items favors standby or standing; an empty queue favors headless.
- **Freebusy.** Whether a seat is free, busy, waiting or unknown. The director repo's `skills/director/scripts/freebusy` builds that table read-only from `marvel get sessions` and `marvel get teams` on each cluster.
- **last_turn.** How long since the seat last took a turn: the LAST-TURN column of `marvel get sessions`, which the same freebusy script carries into its seat rows. A long gap says a standing seat is wasting its context.
- **Quota.** What a standing or standby seat spends while it waits, against usage limits and declared budgets (`docs/design/usage-limit-pause.md`).
- **Arrival pattern.** Bursts against a steady trickle, which none of the signals above state on their own.

## Pairing a reviewer and a builder on one PR

The operator also asks whether a reviewer and a builder could be paired on a single PR to loop fast, and talk over NATS directly. The aim is to cut the round trip through a human or a supervisor for each review comment. The director repo has a sibling note on that pairing, in ArcavenAE/director#395 (`_kos/ideas/paired-reviewer-builder-over-nats.md`, not merged yet).

## Open risks

- **Who gates ready and merge.** Seats never merge. Director merges, and a PR goes ready only after a non-author review approves it. A fast builder-reviewer loop must stop at those gates and must not become a way around them.
- **Quota spend.** Standby and standing seats spend while idle. A mode change that saves cold starts can cost more overall.
- **Run conditions.** A seat that moves between modes keeps its run conditions: scratch home, no live socket, localhost only. A longer-lived seat has more time to drift from them.
- **Who decides.** A supervisor may propose a mode change; who confirms it is the automation-boundary question of ADR-007.
- **Load-balancer overlap.** Mode choice and count choice may both move a seat in the same tick. Which decides first is open.

## Pointers

By id or number only, since the orchestrator that holds them is private:

- aae-orc-8yag2
- director messages 01M5JYJK3X (the headless-candidates list) and 01M5JYKF4Q (the scheduler evaluation)
- ADR-010 (headless completion semantics), ADR-007 (automation boundary)

## Open questions

- Is a seat's mode declared in the role, chosen by a supervisor, or chosen by marvel from the signals?
- What does a mode change cost, and is it visible in `get sessions`?
- Does the router live in a supervisor, in director, or in marvel's reconciler?
- How does a standing seat's shift cycle interact with the standby window?
