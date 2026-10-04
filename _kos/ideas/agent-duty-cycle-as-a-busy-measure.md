# Agent duty cycle: a busy fraction over a window as a second measure of free and busy

- **Status:** idea (pre-hypothesis, no commitment). Captured, not designed.
- **Date:** 2026-10-04
- **Origin:** the operator, verbatim: "what if we could track the agent duty cycle as an additional measure of free/busy to help improve resource allocation and replica/queue balancing?"
- **Subject:** marvel (how it knows whether a seat is free or busy, and what it does with that). Filed in the marvel graph per the subject test.
- **Related:** [[fleet-throughput-as-the-objective-function]] (the objective a placement signal would serve), [[health-signal-taxonomy]] (the signals marvel can read), `question-session-state-observation` (what a session's state should be derived from), `question-shift-triggers`.

## The idea

Today a seat reads as free or busy from a snapshot. A duty cycle would be the fraction of a window a seat spent busy, so allocation and replica and queue balancing could read a rate and not a single instant.

## Raw material from the director, UNCHECKED

None of these was verified when this idea was filed. Each is a claim to test, not a fact.

- Presence is a binary idle or busy snapshot.
- `marvel get sessions` shows CPU% and CTX% per session. (The README lists CPU% and RSS per session and CTX% is a documented column; the combination was not re-read against the code.)
- Seats read "idle" while sitting at a usage-limit menu.
- Builders sat idle behind a queued item on 2026-10-03 (a product team), so a busy fraction over a window may say more than a snapshot.

## Guardrail

Per SOUL section 8, a duty-cycle number informs placement and scaling. It does not gate. A number that blocks a spawn or a merge becomes the target and stops measuring (see the Goodhart note behind ADR-007).

## Questions this leaves open

Posed as questions, none prescribed.

- What counts as busy: a turn in flight, a tool running, tokens flowing, or CPU above a floor? Each is a different signal with a different cost to read (see `question-channel-observation-cost`).
- Which window, and is it a rolling window or per shift generation?
- How does a seat parked at a limit menu, or blocked behind a queued item, count? Idle by the snapshot, but not free in any useful sense.
- Who reads the number: the reconciler for replica counts, a supervisor for queue balancing, or only an operator display?
- Does it need a channel marvel does not have for an interactive seat (the same gap as CTX% for interactive claude)?

## What would make this testable

A probe that records, for a few seats over a day, the candidate busy signals marvel can already read, and compares each fraction with what the seat actually did. Nothing here needs code first.
