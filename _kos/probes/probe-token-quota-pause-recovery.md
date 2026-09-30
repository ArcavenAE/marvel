# probe-token-quota-pause-recovery

**Date:** 2026-09-30
**Asked by:** the operator, via director (commission 01M3R6YG47), routed by
arcaven-supervisor-g5-0.
**Parent:** `probe-backend-usage-limit-observability.md` (finding-050). That
probe asked what limit state marvel can *observe*; this one asks what happens
when the limit is *reached* and how the fleet comes back.
**Related node:** `question-shift-triggers` (a usage limit is a candidate
trigger that node does not yet list).
**Status:** complete. Harvested to finding-056.

## Question

The operator, verbatim: "how well can we recover from a token quota pause?
what impacts are there and how do we get restarted?"

Specimen: the 2026-09-28 to 2026-09-30 pause. The operator's account reached
99% of its weekly token allowance, director told every seat to protect its
work and start nothing new, and director later lifted the order and told the
seats to resume.

## Hypotheses, pre-registered

- **H1 (recovery is human-driven today).** Nothing in marvel sees the pause.
  Seats keep their panes, the reconciler reports them healthy, and restart
  happens only because a human relays "resume" through director. Falsified
  if any marvel component recorded the pause or the resume on its own
  (an event, a state change, a log line tied to a limit response).
- **H2 (the losses are unpushed work and dropped messages, not sessions).**
  Sessions survive a pause. What is lost is work that was never committed or
  pushed, and messages that arrived while a seat could not act. Falsified by a
  seat that died or was respawned because of the pause itself, or by a pushed
  branch that was lost.
- **H3 (protect-your-work orders collide with repo gates).** Director's
  protect order asks for a push, but a red-first commit cannot pass a
  pre-push hook that runs the tests. Seed evidence: this seat's #335 commit
  (2026-09-28), refused by lefthook `test-race` on the intended red test and
  left local. Falsified if that case is isolated across the fleet.
- **H4 (the successor restarts from inbox plus handoff, and the inbox is the
  bottleneck).** A resumed seat rebuilds its state by draining its director
  inbox; stale requests read as live ones. Seed evidence: this seat drained 37
  messages on resume, the oldest from 2026-09-27, and several were already
  handled or superseded.

## Success signal

The finding answers three things, each claim graded MEASURED, INFERRED or
HYPOTHESIS, with the alternative the evidence excluded:

1. A graded answer to "how well do we recover today", per seat class
   (supervisor, builder, reviewer, codex seats), not one fleet-wide grade.
2. The impacts: work lost, work stranded locally, messages dropped or
   stale, time from lift to first useful action, and any seat that needed a
   human to unstick it.
3. The restart procedure as it stands (who did what, in order), and what
   marvel should do. Three candidates to evaluate, not presumed: read the
   usage limit per backend (finding-050's reader); a pause/resume seat state
   the reconciler understands, distinct from crashed or idle; auto-resume at
   the limit's reset time.

## Method

Read and observe only, secret-safe: names and shapes, never credential
values, never the operator's usage numbers beyond what the operator stated.

- **research-supervisor** mines the 2026-09-28 to 2026-09-30 window: director
  bus traffic (GLOBAL_TO_DIRECTOR and AGENT_INBOX, noting they are different
  streams), seat handoff files, pushed versus local-only branches, and
  per-seat time from lift to first action.
- **This seat** reads the marvel side: `marvel events` across the window,
  the daemon log (`~/.marvel/log/daemon.log`) for limit-shaped lines, and the
  session state each seat held through the pause. Code reading: where a
  limit response would surface today (`internal/api/heartbeat.go`,
  `internal/team/controller.go` rate-limit pacing, `usage.LimitSource`).
- **Positive control.** Before reading an empty event stream as "nothing
  happened", confirm the stream holds known events from the window (a
  scale, a shift, a respawn).

## Out of scope

Building any of the candidates. Changing the budget or admission code.
Anything about how director itself behaved beyond what marvel could have
observed; that belongs in director's graph.
