# Probe brief: the operating points along context, and the axes an operator might prioritize

**Status:** OPEN (brief only).
**Question:** `question-interactive-context-pressure`, `question-shift-triggers`.
**Medium:** mining local artifacts first, then controlled measurement for the
points the corpus cannot reach.
**Prior work:** finding-016 (the auto-compact point, measured),
`context-pressure-is-an-operating-point-not-a-fill-level.md` (the reframing
and the carrying-cost measurement).

## Why this probe exists

Two operating points have been identified so far, and both by accident while
looking for something else: the auto-compact point (measured at the effective
window minus roughly 32k) and the reliability knee (operator-reported near
600k for fable and opus). Neither was predicted; both changed the design when
found.

That is a bad way to discover the structure of a metric marvel intends to
actuate on. **The occupancy axis has multiple distinguished points where
behavior changes, and an operator prioritizing cost will care about different
ones than an operator prioritizing latency, fidelity, or fleet throughput.**
This probe is to enumerate them deliberately, measure the ones that are
measurable, and say plainly which are conjecture.

## Part 1: candidate operating points

Each is a level (or a moment) at which something discontinuous happens.
Status is stated per item; most are unmeasured.

### Measured

1. **The auto-compact point.** Effective window minus ~32k, firing at 93.5%
   of the effective window. n=63, IQR 1,609 tokens. finding-016. Note it is a
   SETTING, so it is assignable as well as observable.

2. **The carrying-cost gradient.** Not a point but the slope everything else
   sits on: 107x cache-read per output token below 100k median occupancy,
   261x above 300k. Context is rent, paid every turn.

### Reported, unmeasured, worth measuring first

3. **The reliability knee.** Near 600k for fable and opus per operator
   observation: output quality degrades while cost continues rising, making
   the region past it Pareto-dominated. This is the single most valuable
   unmeasured point, because a dominated region is one a scheduler should
   refuse by default rather than expose as a tunable. Measuring it needs an
   outcome signal paired with occupancy (retries, tool-call errors,
   self-corrections, rejected diffs), which the transcript corpus plausibly
   carries.

### Conjectured, from mechanism rather than observation

4. **Cache-rebuild events.** A first pass found 639,142,047 `cache_creation`
   tokens paid across the corpus on turns where under 20% of prior occupancy
   was served from cache. That is a large cost line paid at WRITE price
   rather than read price. **But the detector is not clean:** the median idle
   gap before such a turn is 3 seconds, so it is catching post-compaction
   rebuilds and segment boundaries alongside any true expiries. Separating
   the causes is the work.

5. **Cache TTL expiry, an operating point on the TIME axis rather than the
   token axis.** A session idle past its cache TTL pays full price to
   reload context it already had. This is qualitatively different from every
   other point here because it is triggered by the clock, and it interacts
   directly with the standby-tier question: a "hot" standby whose cache has
   expired is a cold standby that cost money to hold.

6. **Cache breakpoint exhaustion.** Prompt caching allows a bounded number of
   breakpoints. A session whose structure exceeds them stops caching some
   segment and silently moves that segment from read price to input price.
   Whether this is reachable in practice under these harnesses is unknown.

7. **Rate-limit interaction points.** Input TPM is consumed per turn in
   proportion to occupancy, so a high-occupancy session consumes the shared
   per-account budget faster per unit of work. There should exist an
   occupancy above which one session's single turn is a material fraction of
   the minute's budget, and above which two such sessions cannot run
   concurrently. This is the point where context pressure and fleet
   scheduling stop being separable.

8. **The handoff-affordability point.** Remaining headroom must exceed the
   cost of writing a handoff artifact plus the successor reading it. Below
   it, an authored handoff is no longer possible and only an unauthored
   summarization remains. This was the original framing of the shift trigger
   and it has never been measured; it is probably the lowest of the points
   here and therefore the true floor.

9. **The context-window hard limit.** Request rejection. Almost certainly
   unreachable when auto-compaction is enabled, which makes it interesting
   mainly for `DISABLE_AUTO_COMPACT` configurations.

10. **Attention dilution.** Distinct from the reliability knee: retrieval
    degradation over a long context even where the model does not fail
    outright. If real and separable, it is a quality cost incurred well
    before the knee, and it would argue for lower operating points than cost
    alone justifies.

## Part 2: the axes, and which points each one cares about

The point of enumerating operating points is that different operators
optimize different things. The same session at 400k is fine on one axis and
wrong on another.

| Axis | What it minimizes or maximizes | Points it cares about |
|---|---|---|
| **Money** | total cost per unit of delivered work | carrying gradient (2), auto-compact point (1), cache rebuild (4,5,6) |
| **Latency** | wall-clock to completion | compaction pause (measured, 154s median), handoff time (unmeasured), standby tier |
| **Output quality** | correctness, fewer retries | reliability knee (3), attention dilution (10) |
| **Knowledge fidelity** | avoiding unauthored summarization | auto-compact point (1), handoff affordability (8) |
| **Fleet throughput** | total work across all agents | rate-limit points (7), standby carrying cost |
| **Predictability** | variance, not mean | cache expiry (5), model change, rate-limit proximity |

Three observations that fall out of the table and are worth testing:

- **Money and latency conflict at the auto-compact point.** Shifting early
  saves carrying cost but spends handoff time; letting the harness compact
  costs a 154s pause and an unauthored summary. Neither dominates.
- **Quality and money agree past the knee**, which is why the dominated
  region is special: it is the one place where two axes point the same way
  and no tradeoff exists. That is the argument for making it a default rather
  than a policy.
- **Fleet throughput is the axis nobody has instrumented at all**, and it is
  the one where a per-session actuator can do harm: shifting several agents
  at once to relieve context pressure consumes a burst of the shared rate
  limit.

## Sub-probes, in value order

**SP1. Locate the reliability knee.** Pair occupancy with an outcome signal
from the transcript corpus. Success: a stated occupancy band per model where
an error or retry rate rises, with its confidence interval, or a statement
that the corpus cannot support the measurement. This is first because a
dominated region changes a default rather than a tunable.

**SP2. Separate the cache-rebuild causes.** Decompose the 639M
`cache_creation` tokens into post-compaction rebuild, segment growth, TTL
expiry, and breakpoint effects. Success: a cost attribution per cause, and a
verdict on whether TTL expiry is a real operating point on this fleet or a
theoretical one.

**SP3. Measure a handoff end to end.** Wall-clock and tokens, cold, for one
real shift. Success: a number in the same units as the 154s compaction
measurement, so the shift-versus-compact comparison stops being intuition.
Also establishes the handoff-affordability floor (8).

**SP4. Find the rate-limit interaction point.** For a given plan, the
occupancy at which one turn is a material fraction of the input TPM budget,
and the concurrency at which two sessions collide. Success: a stated
occupancy-by-concurrency boundary, which is the first instrument marvel would
have for fleet-level admission on a shared resource.

**SP5. Test attention dilution separately from the knee.** Only if SP1
succeeds and suggests degradation begins before outright failure.

## Excluded scope

- Choosing a policy. This probe produces the map; the operator picks the
  operating point per role, per the idea file.
- Building the actuator. Instruments and measurements only.
- Non-Claude harnesses beyond opportunistic checks. codex has a small
  compaction corpus locally and gemini has a per-turn token series; both are
  worth a pass at the end rather than the start.

## Method note

Same discipline as finding-016: parse and filter on record type, never match
raw strings; the corpus is live and self-contaminating; read counts, token
fields, timestamps, and metadata only, never message content. Any measurement
of an outcome signal for SP1 must be designed to avoid reading content, which
may constrain what signals are usable and should be stated as a limitation
rather than worked around.

## What would change the read

If the reliability knee turns out not to exist as a measurable discontinuity,
the dominated-region argument collapses and context pressure becomes a pure
cost-versus-fidelity tradeoff with no free lunch anywhere on the curve. That
would simplify the policy surface considerably and is worth knowing early,
which is why SP1 is first.
