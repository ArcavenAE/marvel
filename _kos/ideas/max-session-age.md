# Max session age: a per-role lifetime that ends in a shift or a kill

- **Status:** idea (pre-hypothesis, no design commitment). Operator-raised
  2026-09-28, relayed through director and the arcaven supervisor.
- **Subject:** marvel (the shift machinery, restart policy, and the
  manifest's per-role fields). Filed in the marvel graph by the subject
  test.
- **Tracking:** none. Candidate input to roadmap M4 (automatic shift
  triggers).

## The operator's words

> "we should be able to set a max age on agents as a means to define a
> replace or kill auto shift change, also because agents get stuck in many
> ways, and also sometimes the harness is waiting to update"

## The idea

A role declares a maximum session age in the manifest. When a live session
passes it, marvel takes the declared action:

- **replace:** start the existing rolling shift for that session, so a fresh
  seat comes up before the old one drains. This is the default candidate,
  because it keeps the replica count whole.
- **kill:** end the session and let the restart policy decide what follows.
  This fits a role that should not outlive a bound at all, or one where a
  replacement would only meet the same wedge.

Sketch only, to make it concrete (not a proposed schema):

```toml
    [team.role.shift]
    on = "max-age"
    max_age = "8h"
    action = "replace"   # or "kill"
```

Age would run from the session's start (`CreatedAt`). A replacement is a new
session, so its clock starts over.

## Why

1. **Agents get stuck in ways liveness cannot see.** Health is liveness, not
   productivity. A seat at a login prompt, at a dead prompt a keystroke would
   answer wrongly, or in a hung tool call is a live process in a live pane,
   and it reads healthy. The tmux harness-state watchdog idea and marvel#276
   aim to detect and classify those states. A max age is the blunt,
   detection-free backstop: it bounds how long any undetected wedge can last.
   finding-003's 2026-09-27 addendum records one such case, a feedback prompt
   that held a pane for about 9 hours.
2. **A harness update applies only on restart.** A long-lived seat keeps
   running the harness binary it started with. It may also sit at an
   "update available" notice, which finding-049's 2026-09-27 addendum
   records for codex. Periodic replacement is how a fleet picks up an
   installed update without an operator visiting each seat.
3. **Context pressure is not the only reason to rotate.** The shipped
   automatic shift fires on context pressure, and only for sessions marvel
   can meter. A seat with no context feed never trips it. Age applies to
   every runtime.

## Related work and prior art

- **question-shift-triggers** already lists a scheduled trigger ("shift
  change every 6 hours") and config changes ("agent definitions need
  re-read") among its candidates, and asks whether the Schedule resource is
  the right abstraction for timed shifts. A max age is a per-session form of
  the scheduled trigger. It needs no clock that aligns shifts across the
  fleet.
- **finding-002** (shift mechanics): replace would reuse the rolling shift
  state machine, supervisor-last ordering, and the shift timeout with
  rollback.
- **finding-003** (healthchecks): liveness and restart policy. kill would
  hand off to the restart policy as a crash does, which raises the question
  below of whether an age expiry should charge backoff.
- **auto-shift-trigger-model.md** and the context-pressure monitor: the
  shipped `shift` block (`on = "context-pressure"`, `headroom_tokens`) is the
  natural home for a second `on` value. That idea's reading-age and
  anti-flapping requirement (RQ-7) and the per-tick fleet cap
  (`maxAutoShiftsPerTick`) apply here too. A fleet started together would
  otherwise expire together.
- **question-scheduled-cues** (proposed in marvel#388): a recurring cue on a
  clock. A max age is the per-session counterpart: "this seat has run long
  enough," not "it is time for everyone."
- **tmux-harness-state-watchdog.md**, **health-signal-taxonomy.md**, and
  marvel#276: detection of stuck states. A max age complements detection; it
  does not replace it.
- **elem-staged-activation-upgrades**: marvel's own binary picks up upgrades
  through reexec with agents kept. A max age is the harness-side analogue:
  seats pick up a new harness binary on their next replacement.
- marvel#186 (an orphaned agent heartbeats forever) is adjacent: an age bound
  would also end a session nothing else is tending.

## Open questions

- Should an age-triggered kill charge crash-loop backoff and count against
  `max_restarts`? It is not a crash, so probably not, but a role that wedges
  within every lifetime would then cycle without ever freezing.
- Should expiry wait for a quiet point, such as the next idle reading or the
  end of a turn, so a replace does not cut a seat off mid-task? Or is the
  bound hard?
- How does max age compose with a handoff? A replacement shift is only as
  good as the successor's handoff (aae-orc-7opc, the handoff artifact arc).
- Should the operator be able to declare age at team or workspace level, with
  a per-role override, as the auto-shift trigger model proposes for context
  pressure?
- Is age measured from session start, or from the last successful
  replacement of that replica slot? The two differ once a slot has no
  stable identity (marvel#363).
