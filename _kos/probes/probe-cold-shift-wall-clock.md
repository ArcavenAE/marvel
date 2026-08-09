# probe: what does a cold shift cost, end to end, in the same units as compaction

**Ticket:** `aae-orc-r4q6`
**Date opened:** 2026-08-08
**Question:** `question-shift-triggers`; bears on
`question-interactive-context-pressure`.
**Status:** RUN and closed 2026-08-08. Result in
`_kos/findings/finding-018-cold-shift-wall-clock.md`.

Everything under the "pre-registration" heading was written before the
rig was started and has not been edited since. The outcome, including
the one deviation from the plan, is appended at the end.

## Why

finding-016 measured one side of the shift-versus-compact tradeoff and
only one side: **compaction takes a median of 154 s, max 275 s**, over 63
auto events. The other side has never been measured. Every policy in this
project that prefers a shift to a compaction rests on an intuition that a
shift is cheaper on latency, and that intuition has no number under it.

The intuition may be inverted. A cold shift discards the successor's
cache and its working context, so it plausibly costs the same order or
more. If it does, the set of policies worth offering changes.

## Pre-registration

Written before the first run. Nothing below this heading was revised
after data existed; corrections are appended in a dated section instead.

### The two hypotheses, and what separates them

- **H1 (prevailing intuition).** A cold shift completes in materially
  less than 154 s, so a shift is the cheaper remedy on latency and
  "shift rather than compact" is defensible on wall clock alone.
- **H2 (the suspected inversion).** A cold shift costs the same order as
  154 s or more, so the latency argument for shifting does not hold and
  the case for shifting has to be made on something else (finding-016
  already argues that the surviving case is knowledge fidelity, not
  cost).

**The discriminator is not the whole-shift number by itself.** It is the
number for a role whose successor must reach *harness* readiness, not
merely *pane* readiness. Stated in advance because the two are different
measurements and the cheap one cannot answer the question:

- A simulator or shell successor reaches readiness in process-start time.
  That measures marvel's control-plane floor. **A floor cannot falsify
  H2.** If arm A or B returns 5 s, that is consistent with H1 and H2
  both, and I will say so rather than reporting it as the shift cost.
- A floor that already exceeds 154 s would kill H1 outright. That is the
  only verdict the cheap arms can deliver on their own.
- Only an arm whose readiness gate is tied to a real harness coming up
  can put a number on the side of the comparison that matters.

### Arms

| Arm | Runtime | Readiness gate | What it measures |
|---|---|---|---|
| A | simulator, heartbeat healthcheck | first heartbeat RPC | control plane + a real readiness gate |
| B | generic (`sh`), no healthcheck | pane Running | floor: reconcile quantization + pane spawn |
| C | simulator, 2 roles (worker, supervisor) | first heartbeat, per role | multi-role serialization cost |
| D | claude, real harness | to be determined by what the adapter actually gates on | real harness cold start |

### Stages to decompose

The ticket asks for five stages. Recorded here in advance, with the
prediction that some of them do not exist in this codebase, because that
absence is itself a result:

1. drain time for the departing session
2. spawn to harness-ready for the successor
3. configuration and pack resolution
4. handoff artifact read
5. time to first useful output

### Success signals

The probe produces a **finding** only if all of these hold. Otherwise it
produces a probe record and says so.

1. At least 5 clean runs per attempted arm, clean meaning the daemon
   reported `team.shift-completed` with no `team.shift-timed-out` and no
   abort.
2. The whole-shift interval decomposed into at least: initiate to
   successor created, created to ready, ready to old-generation deleted,
   deleted to shift complete.
3. Variance reported as median with min and max, never a bare mean.
4. An explicit statement, per arm, of which hypothesis that arm's
   evidence could have excluded and which it could not.

### Cache state at cutover, and the trap I am pre-committing against

The ticket asks for the successor's cache state, because a cold
successor re-warms at input price rather than cache-read price.

The trap, named in advance: **`cache_read_input_tokens = 0` does not by
itself establish "cold".** A row with both cache classes at zero is the
same arithmetic under two different identities: a genuinely cold
successor that will pay a re-warm, and a run where prompt caching was
never in play at all. I will report both `cache_read_input_tokens` and
`cache_creation_input_tokens`, and I will only call a successor cold on
a row where creation is non-zero while read is zero. A both-zero row will
be reported as undetermined.

### Known confounder, recorded before measuring

The reconciler ticks every 2 s (`daemon.ReconcileInterval`), and the
shift state machine advances at most one phase per tick. Any interval I
measure is quantized to that, so a measured 6 s could be three ticks of
near-zero work. I will report tick counts alongside seconds.

## Rig

- `HOME` under the session scratchpad, per the isolation requirement.
- `MARVEL_SOCKET` set explicitly to a short `/tmp` path. Not a
  preference: `sun_path` is 104 bytes, the scratchpad prefix alone is
  109, and finding-013 records that a layout-derived socket under it is
  rejected before it is created.
- `MARVEL_TMUX_SOCKET=marvel-steady-lynx`, a name in use by nothing else
  on this machine (verified absent before start).
- Four sibling agents are live. Nothing in this probe touches
  `tmux -L default` or any other `marvel-*` socket.

---

## Outcome (appended 2026-08-08, after the runs)

**Success signals: met for the control-plane arms, so finding-018 is a
finding rather than a candidate catalog.**

1. At least 5 clean runs per arm: MET. 26 shifts across four arms
   (7 / 7 / 7 / 5), 26 of 26 clean.
2. Stage decomposition: MET, with one gap that became a result. Marvel
   emits no event at the readiness transition, so that stamp came from
   a 50 Hz poll while every other stage is server-stamped.
3. Variance as median with min and max: MET. IQR under 0.1 s on the
   headline column in every arm.
4. Per-arm statement of what the evidence excludes: MET, tabulated in
   the finding.

**Deviation: arm D did not run in its pre-registered form.** The
isolated HOME carries no credentials, so a harness launched under it
failed at `authentication_failed` before any model call, and running the
daemon under the real HOME was refused because that is the operator's
live fleet state with four other agents on the machine. Arm D ran
standalone instead. The finding reports it separately and marks the
composed total INFERRED.

**Arms added that were not pre-registered.** Arm E (4 roles) was added
after arm C, because two role counts determine a line but cannot test
whether the scaling is linear. Three points now do, with residuals under
10 ms.

**Hypothesis verdict.** H2 is excluded at the control plane for teams up
to four roles: a shift costs 8.92 s at one role and scales at 7.99 s per
role, against compaction's 154 s median. H1 versus H2 END TO END remains
undetermined, because the successor's turn count is not measurable while
no handoff artifact exists to define when it has caught up. The
pre-registered warning that a floor cannot falsify H2 held, and the
finding does not claim otherwise.

**The pre-registered cache trap fired, unplanned.** The failed
isolated-HOME harness attempt produced a row with both cache classes and
total cost at zero, which reads as a maximally cold successor and was in
fact an authentication failure with no model call. The commitment
written above is what caught it.
