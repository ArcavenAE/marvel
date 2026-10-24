# Seat fault accumulation: concerns, fused evidence, and replacement as a proposal

- **Status:** idea (pre-hypothesis, no design commitment). Raised by the
  operator on 2026-10-24 and relayed by director.
- **Subject:** marvel. The idea is about seat lifecycle: when a session is
  replaced, and what casting learns from it. It is filed in the marvel graph
  under the subject test.
- **Tracking:** none yet. The closest existing item is bd aae-orc-nwqi9 (fuzzy
  sensor fusion and confidence buckets, open since 2026-09-21). This idea is one
  concrete site for that research.
- **Feeds:** the fleet health and fault management party of 2026-10-24,
  which places the score in its detect, propose and act split.

## The operator's words

> "I think we had an old kos idea marvel about a way for agents or users to
> issue a concern about an agent session, begin accumulating demarits or fault
> reports, and that at some point we could decide to simply replace any agent
> session that gets too many of these for it's position, a way to vote them off
> the island, possibly accumulate error rates against other commonly held
> systems, like pentalizing a model or model/effort for specific purposes,
> maybe? If so let's improve that thinking some and if not let's create it. I
> want to use ML ideas or sensor fusion fuzzy math to accuminate faults and at
> some point when the faults are too high we replace it, we learn from it, and
> we avoid whatever combination of conditions (high age, specific models,
> specific roles)"

## Is there an old idea?

No file states this idea [measured]. The search covered the orc, marvel,
director and the role library, plus a bd search over every issue's title,
description and notes. The terms searched were concern, demerit, strike, vote
off, island, fault report, reputation, trust score, penalize, health score,
error rate and per-model. As a positive control, `max_restarts` was found in 10
marvel files.

The memory probably blends three items [inferred]:

1. **bd aae-orc-nwqi9.** Fusion and confidence over noisy seat signals, with
   "should it rotate, with a confidence, not a boolean" named as a decision
   [read].
2. **`_kos/ideas/max-session-age.md`.** A per-role lifetime that ends in a
   replacement. Age is one of the three conditions the operator names [read].
3. **marvel's crash accounting.** This is the one fault accumulator the fleet
   has built [read]:
   - it counts crashes per role (`internal/team/controller.go:832-851`);
   - it resets the count after 10 minutes healthy (`:1719-1773`, `:172`);
   - it freezes a role at `max_restarts` until the operator runs reset-health.

Two parts are new. One is a channel for agents and people to file a concern
against a seat. The other is learning which conditions to avoid when casting.
So this file creates the idea rather than improving one.

## What marvel already gets right, and keeps

The crash accumulator already carries three rules a fault score needs [read]:

- **Charge one event once.** finding-019 records how a three-replica external
  kill once spent a `max_restarts=3` budget in a single event. Now one crash is
  charged per role per reconcile tick (`_kos/nodes/bedrock/question-healthchecks.yaml:15-53`).
- **Decay on shown health, not on the clock alone.** The operator's ruling of
  2026-08-29 made `max_restarts` "a within-window cap, not a lifetime cap".
- **A refusal is not a fault.** "A refusal must never touch crash bookkeeping"
  (`internal/admission/doc.go:52-57`). The same rule extends to classifier
  denials and readiness stops: they are controls doing their job, or operator
  dependencies. Neither is the seat's fault.

Director's rule bounds the decision [read]: "a seat is never retired over one
episode" (director `skills/director/SKILL.md:400`).

## The idea

### 1. Who files a concern, and how much it weighs

A **concern** is one structured record against a seat:
- the seat, keyed by role and replica slot, not session name, because names
  change at every shift;
- the reporter;
- a kind from a closed list;
- a severity;
- an evidence pointer (a path, an event seq, a PR review, a bus message id);
- a time.

It never carries free text as the evidence.

Candidate reporters:

| reporter | example | starting weight |
|---|---|---|
| marvel's own events | crash, crashloop-backoff, watchdog verdicts | full, typed |
| a reviewer's verdict on the seat's PR | CHANGES_REQUESTED with a named defect | per reviewer, learned |
| the supervisor of the seat | missed ack, wrong output form, scope drift | per supervisor, learned |
| a peer seat | "asked twice, no answer" | low until learned |
| a person | the operator or a colleague | high, never auto-scored down |
| the seat itself | "I am stuck", a self-report at handoff | as reported |

These are never counted as concerns: classifier denials, permission or
folder-trust prompts, usage-limit pauses, admission refusals, age expiry, and a
shift the supervisor called. Each one answers some other question.

Reporter reliability starts equal. It is learned only once confirmed outcomes
exist, in the Dawid-Skene sense: each reporter's error rate is estimated from
agreement with other reporters and with later confirmed outcomes [read,
outside 6, 8]. Two reporters cast from one prompt or one rubric count as one
voice until they are shown to differ [read, outside 8].

### 2. Accumulation

The method follows the outside research's pick [inferred from outside 3, 4,
13, 14, 16, 23]:

- **A decayed Beta-Binomial fault rate per seat.** The count is faults out of
  work units, never wall time, so a busy seat is not penalized for being busy.
  Both counts decay with a half-life, so old faults fade.
- **A prior from the seat's group.** The group is role, model and effort,
  partially pooled. A young seat with three reports borrows its group's rate
  instead of being judged on three points.
- **"Too high" is a shift, not a line.** A one-sided CUSUM of the seat's
  reports against its group rate raises the flag. CUSUM and SPRT trade false
  alarms against detection delay through average run length, which is the
  calibration a fixed threshold lacks [read, outside 13, 14].
- **Per position.** A builder and a supervisor fail differently, so the group
  rate and the CUSUM reference value are per role. "Too many for its position"
  is the role's own baseline, not one number for the fleet.
- **Envoy's outlier-ejection rules, adapted** [read, outside 23]:
  - compare against peers;
  - require a minimum volume before judging;
  - cap the share of a team proposed for replacement at once;
  - lengthen the re-cast cool-down for a condition that keeps failing.

Methods that wait until the shadow log shows base rates:
- **Dempster-Shafer.** It fits only when reports name competing causes, and
  undiscounted it turns two weak conflicting reports into certainty (Zadeh's
  example) [read, outside 1].
- **A fuzzy membership layer.** It is a presentation of the posterior, not new
  evidence [read, outside 10].
- **Bandit casting.** This is section 4.

### 3. Attribution: the seat's own fault against its conditions

The fault odds on a report are modeled as:

> seat effect + model + (model x effort) + harness + role + host + backend + f(seat age)

The seat effect is the seat's own fault. The condition effects are what the
fleet learns [inferred from outside 5, 17]. Two framings help:

- **Item difficulty, as in IRT.** A seat handed hard work is not faulty for
  that reason. The task's difficulty sits on the same scale [read, outside 17].
- **A hazard by age.** Fit a Weibull shape to time-to-first-confirmed-fault by
  seat age [read, outside 18, 19]:
  - k above 1 is the measured case for age-based replacement, which is
    max-session-age's backstop;
  - k below 1 says young seats are the risk.

  Measured context and turn effects exist outside the fleet: degradation with
  input length that differs by model [read, outside 31], and a 39 percent
  average multi-turn drop [read, outside 33]. Inside the fleet, one corpus found
  tool-error rate flat at 2.7 to 5.3 percent across context occupancy
  (`question-shift-triggers.yaml:73,88`) [read]. Age is a candidate factor,
  not a given.

**The caution that comes first.** The orchestrator's finding-054 hand-classified
169 bad-outcome chains from 4,656 dispatches. It found that "84% of real
(non-by_design) bad outcomes ... are not model capability". A version split
read as a model effect was "phase, not model", and the finding warns that "any
naive fleet grouping by model manufactures a fake model effect" [read]. In a
small fleet, model, host, role and harness are tangled: one model may run only
on one host in one role. That breaks the multilevel assumption that conditions
are uncorrelated with the seat effect [read, outside 5]. So no condition effect
is reported until its cell has enough independent groups, and every report
names the confound it could not separate.

One more gap: marvel records the model a seat declares, not the one that
served it (finding-058) [read]. A model key built from marvel's record alone
could be wrong. Transcripts carry the served model [inferred].

### 4. The replace decision and the learning loop

- **The proposal.** When a seat's CUSUM flags it, the score drafts one
  confirm-or-override proposal to the seat's supervisor, never to the seat. The
  draft carries the evidence pointers, the group rate, the seat's rate, and the
  conditions it ran under. The supervisor confirms, overrides or asks for
  more. A lame-duck step is offered first: stop new assignments and let current
  work finish. It costs less than a kill [read, outside 25].
- **What is learned at replacement.** The handoff and the replacement record
  carry the seat's score, its confirmed concerns, and the conditions it ran
  under. The condition cells update. A refused proposal is also data: a
  supervisor who overrides most proposals is a signal to stop the scorer, not
  to tune it until it agrees (the task-judge's stop rule, director
  `_kos/ideas/task-judge.md:125-127`) [read].
- **Avoiding risky combinations when casting.** With a Beta posterior per
  role, model and effort cell, Thompson sampling at cast time draws one rate per
  cell and proposes the lowest [read, outside 35, 36, 37]. It also answers the
  feedback loop in the next section: a cell with few observations still gets
  occasional casts, so its estimate does not freeze high. Casting stays the
  supervisor's choice. The bandit proposes a cell; it does not cast.

### 5. Honest limits

- **Small samples.** With a handful of casts a week per role, posteriors stay
  wide for months [inferred]. The minimum-volume rule means many seats are
  never scored.
- **Gaming.** Once replacement follows reports, the reports become targets
  (Goodhart, Campbell) [read, outside 28, 29]. A seat can avoid work that draws
  reports, and a reviewer can report a rival. A score therefore never becomes a
  reward, and reporters' weights are learned against outcomes, not volume.
- **One noisy reporter.** It is handled by its own learned error rate, not by a
  rule that names it. Until rates are learned, one reporter cannot move a seat
  past the flag alone (minimum volume plus a two-reporter rule) [inferred].
- **Feedback loops.** A condition that stops being cast stops producing data,
  and its estimated risk freezes. Exploration in section 4 is the counter, and
  it has a cost: real work done on a worse setting [read, outside 35].
- **Censoring.** Replacing seats cuts off their age data, so any hazard fit must
  handle censoring [inferred].
- **The confirm step decays.** Bainbridge: a person left to monitor a quiet
  display cannot hold attention for long, and a confirm queue that rarely fires
  gets confirmed without reading [read, outside 27]. The design goal is that
  each confirm is cheap and rare, with its evidence attached.

## The automation boundary

SOUL section 8, ADR-007 and `diagnostic-not-gate.md` apply:

- **Allowed now:**
  - computing the score;
  - drafting a replacement proposal;
  - proposing a casting cell;
  - showing a seat's score to its supervisor.

  All of these remind, check and propose.
- **Stays human or supervisor:**
  - confirming a replacement;
  - deciding that a concern is valid;
  - closing a concern;
  - changing a role's baseline.
- **Needs a ratified written decision before it may happen:**
  - a replacement without a confirm step;
  - a cast that skips a condition cell without a person choosing it;
  - any score that blocks dispatch, merge or a shift.

  Each would make a vital sign a gate. None is proposed here.

## Smallest first probe: a shadow scorer over existing records

These read-only records could feed it today [measured on corporate,
2026-10-24]:

| source | what it gives |
|---|---|
| marvel session records | role, generation, predecessor, state, health, restart and failure counts |
| harness transcripts | served model, effort, typed API errors, classifier denials (to be excluded, and counted only as a check) |
| director route records | 4 files |
| bd | 903 closed tickets, 898 with a close reason |
| PR review states | from GitHub |

Missing on this host: introspect records, the relay log and retrospector
output. No record anywhere holds a concern against a seat, so the first probe
scores marvel events and review outcomes only.

**The probe:**

1. Build one table of seat episodes for the last 14 days, keyed by role and
   replica slot. Columns: model, effort, harness, host, age at each event,
   work units, and outcomes.
2. Score each episode with the decayed Beta-Binomial and the per-role CUSUM.
   Use priors from the role, model and effort group, and exclude denials,
   readiness stops and refusals.
3. Write the would-propose list and the condition table, with every cell's
   group count and the confound it could not separate. Send nothing, replace
   nothing.
4. The operator, or a supervisor the operator names, reviews the would-propose
   list blind to the scores against their own memory of those seats. Report:
   - agreement;
   - false flags;
   - seats the scorer missed;
   - whether any condition effect survives the confound check.

**Pass:** the scorer flags at least one seat that a person agrees should have
been replaced, and its false-flag rate is low enough that a supervisor would
read every proposal. **Fail:** agreement is near chance, or every flagged seat
is one marvel's crash accounting already froze. Either way the finding
records the base rates that the later methods (Dempster-Shafer, fuzzy layer,
bandit) need.

## Open questions

1. Where does a concern get filed: a marvel RPC, a bus subject, or a bd label?
   And who may file one without a fleet identity?
2. What is a work unit per role: turns, tool calls, PRs, or tickets?
3. Is the score marvel's (in the daemon, beside crash accounting) or a
   reader's (the fleet watcher prototype or director)? The fleet-health party
   of 2026-10-24 places it.
4. Should the condition table be shared across clusters, given that hosts are
   one of the confounds?
5. What does a seat see of its own score, if anything?

## Related

- bd aae-orc-nwqi9; `_kos/ideas/max-session-age.md`;
  `_kos/nodes/bedrock/question-healthchecks.yaml`; finding-019.
- director `_kos/ideas/task-judge.md` (shadow, propose only, a different model
  family); director `skills/director/SKILL.md:394-400`.
- The orchestrator's finding-053 and finding-054 (model attribution);
  finding-190 (a second harness changed answers, with no same-harness control).
- The role library's retrospector, which grades merges, not seats.
- Outside sources, fetched 2026-10-24: Dempster-Shafer and Zadeh's example;
  empirical Bayes and partial pooling; Dawid and Skene; Snorkel; CUSUM and
  SPRT; Envoy outlier detection; the bathtub curve and Weibull; software
  rejuvenation (Huang et al. 1995; Garg et al. 1995); Bainbridge; Goodhart and
  Campbell; context rot (Chroma 2025); multi-turn loss (Laban et al. 2025);
  Thompson sampling. The full list of 37 sources, with URLs, is kept with the
  research record for this idea.
