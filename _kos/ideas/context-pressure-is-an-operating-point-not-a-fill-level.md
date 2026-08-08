# Context pressure is an operating point on a cost-and-reliability curve, not a fill level

**Status: idea, but with one measured result inside it.** The redefinition is
pre-hypothesis. The carrying-cost measurement is real and is stated as such.

Raised by the operator 2026-08-08, after finding-016 established that the
denominator marvel divides by is not the denominator that predicts
compaction. This goes further: it argues the whole quantity is defined wrong.

## The complaint

CTX% treats occupancy as a FILL LEVEL: how much of a container is used, with
an implied goal of not overflowing. That framing is wrong in three ways at
once, and a fleet makes each of them worse.

1. **A fleet runs combinations simultaneously.** Different harnesses,
   models, backends, entitlements, and settings, all at once. A column of
   percentages computed against six-dimensionally-varying denominators
   (finding-016) is a column of incommensurable numbers presented as
   comparable. Sorting by it is meaningless; alerting on it is worse.
2. **Occupancy is a price multiplier, not a capacity consumption.** You do
   not spend context once. You re-read the entire context at cache-read price
   on EVERY subsequent turn. Occupancy is therefore the per-turn carrying
   rate for all remaining work, and a percentage discards exactly the
   information that makes it actionable.
3. **Models behave differently at high occupancy, and some get worse.** Above
   roughly 600k, fable and opus become less reliable (operator observation).
   So there would be a region where you pay more per turn AND fail more
   often, which is strictly dominated: no goal is served by operating there.

   **Status of this claim, updated 2026-08-08: operator observation only.**
   One attempt to measure it against the corpus failed to see it (SP1 of
   `probe-context-operating-points-and-axes.md`): tool-error rate is flat at
   2.7 to 5.3 percent across every occupancy band. That neither confirms nor
   refutes, because the proxy is weak, the sample thins exactly where the
   question lives, and high-occupancy sessions are survivors by construction.
   Until a better signal exists this must not be built into a default.

## The one measured thing here

Across 139 local Claude Code sessions with 50 or more assistant turns,
carrying cost measured as cache-read tokens paid per output token produced:

```
sessions with median occupancy BELOW 100k :  107x   (n=13)
sessions with median occupancy ABOVE 300k :  261x   (n=26)
                                    ratio:  2.4x
```

Individual large sessions range from 180x to 571x. One paid 943 million
cache-read tokens to produce 5.2 million output tokens.

This is the structural confirmation of the operator's figures (roughly 30%
more at an auto-compact window of 500k than 250k, and roughly 3x if allowed
to run to 1M). It is not a derivation of them: the operator's numbers compare
doing the SAME WORK at different auto-compact settings, which folds in
compaction frequency and re-warm cost, and this measurement is the simpler
carrying-rate component underneath. Same direction, same order, arrived at
independently.

**The mechanism is worth stating plainly because it makes the rest obvious:
context is rent, not purchase.** You pay it every turn until something
resets it.

## What a better definition would carry

Not one number. At minimum three, all comparable across heterogeneous
sessions in a way a percentage is not:

- **Carrying rate.** What the next turn costs at this occupancy, in tokens
  and in money. Comparable across models because money is. This is the
  quantity that makes "should this agent keep going" answerable.
- **Distance to the next boundary**, in tokens AND in expected turns, where
  the boundaries are plural and policy-selected: the auto-compact point, the
  reliability knee, the team's spend budget, the account's rate limit. Turns
  matter more than tokens for an actuator, because "three turns from
  compaction" is actionable and "31k tokens from compaction" requires knowing
  the growth rate.
- **Position relative to the model's own curve**, which is what makes two
  sessions comparable at all. 400k is comfortable for one model and past the
  knee for another.

Plus a piece of metadata finding-016 already argues for: **which quantity the
denominator is.** A percentage of a context window and a percentage of an
auto-compact window differ by 2x and only one predicts the event.

## Policy is the operator's, and it is per-situation

The goal is not the same in every case, and marvel should not encode one:

- **Never auto-compress.** Shift or replace the agent before the harness
  summarizes anything. Correct when the session's accumulated context is the
  valuable artifact and an unauthored summary would lose it. This is the
  knowledge-fidelity argument finding-016 supports.
- **Allow auto-compression.** Correct when the model performs well at high
  occupancy, the work is naturally episodic, or a shift costs more than a
  compaction. If a provider demonstrates consistently good performance at
  high occupancy, letting it run is the cheaper choice.
- **Hold below a cost ceiling.** Correct when carrying rate rather than
  capacity is the binding constraint, which the measurement above suggests is
  common and under-recognized.
- **Refuse the dominated region.** Above the reliability knee you pay more
  and fail more. This one may not need to be a policy at all: a scheduler
  should arguably decline to enter a Pareto-dominated operating point by
  default, with an explicit operator opt-out, rather than requiring every
  operator to discover the knee independently. **Blocked on measuring the
  knee**, per the status note above; a default built on an unmeasured
  discontinuity is exactly the silent-wrong failure this arc keeps ruling
  against.

So the manifest surface is per-role rather than global, and it is a
POLICY selection plus its parameters, not a threshold percentage. Something
in the shape of "context_policy = shift-before-compaction" or
"= hold-below-cost" or "= allow-compaction", with the numeric limits attached
to the chosen policy.

## Why this cannot be a static table, restated for this quantity

finding-016 established the denominator varies with six independent inputs.
The curve varies with the same six plus workload. A knee measured for opus on
one backend at one release is not a knee for fable, or for opus through a
different provider, or after the next release.

So the same conclusion applies with more force: **measure, learn, key by the
full tuple, invalidate on any change, and never guess.** The difference is
that a wrong window produces a wrong percentage, while a wrong knee produces
money spent on output that is worse than cheaper output would have been.

## The other side of the ledger: handoffs are not free either

Every policy above turns on shifting instead of compacting, and the argument
so far has priced only one side. A handoff costs too, and in a different
currency.

**It costs TIME.** The departing agent drains, the successor spawns, loads
its configuration, reads the handoff artifact, and orients before it does any
useful work. That is dead time on the team's clock, and unlike money it
cannot be recovered by spending more.

The comparison is at least computable, because one side is now measured:
**compaction takes a median of 154 seconds and up to 275** (finding-016). A
cold shift plausibly costs the same order or more. So "shift instead of
compacting" is not obviously a latency win, and may be a latency loss, which
inverts one of the intuitions this arc has been carrying. Nobody has measured
a real handoff end to end, and that measurement is now the highest-value
missing number in the whole ledger.

**Standby tiers trade money for latency, and marvel already owns the
machinery.** The time cost is not fixed; it is a function of how much of the
successor exists before it is needed:

- **Cold.** Spawn on demand. Cheapest to hold, slowest to cut over, and the
  successor starts with an empty cache, so it pays a full re-warm at input
  price rather than cache-read price.
- **Warm.** Process alive, harness started, workspace and packs resolved,
  context not yet loaded. Pays a small idle carrying cost; removes spawn and
  configuration latency.
- **Hot.** Context pre-loaded and the prompt cache already warm. Cutover is
  near-immediate, and the re-warm is paid in advance rather than on the
  critical path. Most expensive to hold, because a warm cache is maintained
  by paying for it, and cache entries expire.

That maps directly onto shipped concepts: replicas, generations, and the
supervisor-last shift ordering. A hot standby is a replica that exists before
it is scheduled, which is a reconciler question rather than a new subsystem.
The interesting design question is whether standby warmth is a role-level
declaration (`standby = "warm"`) or something the reconciler chooses from the
observed shift rate, and whether a hot standby's cache carrying cost is worth
it at the occupancies the measurement above shows teams actually running at.

**And a handoff changes behavior, not only timing.** A successor is a
different instance with different loaded state, and it will not behave
identically. The expectation is that the change is usually POSITIVE: a fresh
agent carrying an authored handoff should outperform a degraded agent
carrying an unauthored auto-summary, particularly past the reliability knee
where the incumbent is both expensive and unreliable.

But "most likely positive" is a hypothesis, and it is the one that decides
whether shifting is a cost to be minimised or a benefit to be sought. If a
handoff systematically improves output, the policy question inverts: the
right operating point may be to shift MORE often than cost alone would
justify, treating the handoff as a quality intervention rather than a
capacity remedy. If it is neutral, shifting is pure overhead to be minimised.
If it is occasionally negative (a successor misreading intent, or losing an
unstated thread), that risk has to be priced too.

So the shift-versus-compaction comparison is four-way, not two-way: token
cost, wall-clock cost, standby carrying cost, and quality delta. Three of the
four are measurable with instruments this arc has already built, and the
fourth is what critic exists for.

## The parallel constraint class this still ignores

Rate limits (TPM in and out, TPD, request rate, plan quota) bind per-ACCOUNT
and are shared across the fleet, recover on a clock, and are not remediated
by a shift. A context-driven actuator that rotates several agents at once
consumes a burst of exactly the resource it does not model. Any scheduler
built on this idea needs both, and shifting is not a remedy for one of them.

## What would make this real

- Measure the reliability knee rather than accepting it. The corpus has
  per-turn occupancy; pairing it with an outcome signal (retries, tool-call
  errors, corrections) would locate the knee per model instead of taking it
  on report.
- Measure the operator's actual comparison: same work, different auto-compact
  settings. The carrying-rate measurement above is the component, not the
  answer.
- Decide whether carrying rate belongs in `internal/usage` beside occupancy,
  or in a separate accountant. It is derived from the same samples and
  answers a different question.
- **Measure one real handoff end to end.** This is now the single highest-
  value missing number: compaction is measured at 154s median, and a cold
  shift is unmeasured. Until both exist in the same units, every policy above
  is being chosen on intuition.
- Price the three standby tiers: idle carrying cost per tier against the
  cutover latency each buys, at the occupancies teams actually run.
- Test the "handoffs are usually positive" hypothesis, because it decides
  whether shifting is overhead to minimise or an intervention to seek. This
  is a critic question (compare successor output against incumbent output
  past the knee), not a marvel one.

## Provenance

Operator framing 2026-08-08. Carrying-cost measurement over 139 local
sessions, same date, same method note as finding-016 (parse, never grep;
counts and token fields only, no message content).

Related: `finding-016-effective-autocompact-window-is-the-predictive-denominator.md`,
`bound-context-instead-of-measuring-it.md`,
`ctx-channel-consent-and-fidelity.md`,
`marvel-agentic-resource-matrix.md`, `question-shift-triggers`.
