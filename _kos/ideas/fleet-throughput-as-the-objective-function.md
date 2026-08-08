# Fleet throughput: the objective function the resource matrix is implicitly serving

**Status: idea. Pre-hypothesis. Nothing here is measured.**

Raised 2026-08-08 out of the context-pressure work, where mapping operating
points against operator priorities surfaced one axis that had no instruments,
no owner, and a failure mode the others do not share: **a per-session
actuator optimizing context pressure can reduce fleet throughput.**

## The gap, stated plainly

marvel's governing frame is the agentic resource matrix: schedule agents by
what actually constrains them (context, spend, cache locality, access,
authority, attention) rather than by CPU and memory. Seventeen rows of
constraint.

**Constraints are not an objective.** A scheduler needs something it is
maximizing, and every instrument built so far is per-session: occupancy, CPU
and RSS, token counts, health, restart policy. There is no quantity in marvel
that answers "is the fleet doing more useful work this hour than last hour,"
and therefore no way to tell whether any scheduling decision helped.

That is tolerable at three agents, where the operator is the scheduler. The
vision's own framing is that past three or four concurrent agents one person
cannot do this by hand, and throughput is precisely what degrades first and
least visibly.

## Why per-session optimization can make it worse

This is the part that makes throughput urgent rather than merely absent. Four
mechanisms, all reachable with what marvel ships or plans:

1. **Correlated shifts burst a shared resource.** Rate limits (input and
   output TPM, TPD, request rate) are per-ACCOUNT and shared across the whole
   fleet. A context-pressure trigger that fires on several agents at once
   consumes a burst of exactly the resource it does not model. The remedy for
   one session degrades all of them.
2. **High occupancy consumes shared budget faster per unit of work.** Since
   context is rent paid every turn, an agent operating at 400k spends roughly
   four times the input-TPM per turn of one at 100k. So occupancy is not only
   a per-session cost, it is a per-fleet CONCURRENCY limit: the same account
   supports fewer simultaneous agents when they run hot.
3. **Cache locality is a fleet property being managed per session.** The
   vision already names batching corpus-sharing dispatches inside the cache
   TTL as converting directly to dollars. That is a scheduling decision about
   WHICH agents run WHEN and TOGETHER, and no per-session view can make it.
4. **Coordination overhead grows superlinearly.** Supervisors, handoffs, and
   review all consume the same shared resources as productive work, and they
   grow with fleet size rather than with fleet output.

Mechanism 4 is why adding agents does not add throughput indefinitely, and it
is the one with well-developed prior art (below).

## Candidate use cases, roughly in order of how soon they bite

- **Admission.** Should this agent start now, or queue? `internal/admission`
  already refuses over-budget work at the operator verbs, so the seam exists;
  what it lacks is a fleet-level signal to refuse ON. This is the shortest
  path from idea to shipped behavior.
- **Shift scheduling.** Stagger versus simultaneous, and how much jitter.
  Directly addresses mechanism 1, and marvel's shift state machine already
  has the ordering machinery (supervisor-last, generations).
- **Cache-locality batching.** Group work that shares a corpus so it lands
  inside one cache TTL. Mechanism 3, and the one with the clearest direct
  dollar value.
- **Placement.** Which host, backend, and model for a given unit of work,
  once M5 makes more than one host real. Backends have independent rate
  limits, so placement is also a way to buy concurrency.
- **Degradation policy.** When the account IS rate-limited, who yields? A
  fleet without a policy degrades by whoever retries hardest, which is the
  worst available allocation.
- **Capacity planning.** How many agents does this account actually support
  at this operating point? Currently unanswerable, and it is the first
  question anyone asks before scaling a fleet.

## Value proposition

Three claims, each falsifiable:

1. **There is a peak, and running past it is worse than stopping short of
   it.** If contention and coordination overhead are real, fleet output rises
   with agent count, peaks, and then FALLS. An operator adding agents to a
   fleet past its peak is paying more for less. Nobody can currently see
   that happening.
2. **The peak is movable by scheduling, not just by buying more.** Staggering
   shifts, batching for cache locality, and placing across backends all raise
   the peak without raising spend. That is the value marvel adds over running
   the same agents by hand.
3. **Cost per unit of delivered work is the number an operator actually
   cares about**, and it is not derivable from any per-session metric. It
   needs a fleet numerator and an honest denominator (below).

## Methodologies worth stealing rather than inventing

The failure mode here is inventing a scheduling theory. These are mature and
map cleanly:

- **Little's Law** (`L = λW`). Work in progress equals arrival rate times
  time in system. Gives the relationship between how many agents are running,
  how fast work arrives, and how long each unit takes, and it is the cheapest
  possible sanity check on any throughput claim.
- **Universal Scalability Law** (Gunther). Extends Amdahl with a COHERENCY
  term, and it predicts exactly the shape claimed above: throughput rises,
  peaks, and declines as concurrency grows, because contention (shared rate
  limits) and coherency (coordination, supervision, handoffs) both grow. If
  any single model is worth fitting to fleet data, it is probably this one,
  because it has a peak and marvel needs to find one.
- **Theory of constraints.** Identify the binding constraint, subordinate
  everything to it, elevate it, repeat. Directly applicable because the
  binding constraint here MOVES: rate limit at one operating point, human
  attention at another, host memory at a third.
- **Queueing theory, the utilization-latency knee.** Explains why running a
  shared resource near saturation destroys latency, and gives a principled
  target utilization rather than a guessed one.
- **Goodput versus throughput.** From networking, and the honest framing for
  agent work: output produced is not output KEPT. Discarded variants are
  screening cost, not waste (the vision's selection-based engineering already
  says this), but only if they are counted as screening rather than as
  production.

## The measurement problem, which is the hard part

Throughput needs a unit of work, and every obvious candidate is wrong:

- **Output tokens.** Rewards verbosity. A model that writes twice as much to
  say the same thing scores double.
- **Turns or sessions.** Rewards churn and penalizes agents that get it right
  the first time.
- **Tasks closed.** Better, but game-able, and it needs task granularity to
  be comparable across teams.
- **Per selected outcome.** The vision's own accounting for
  generate-and-select: cost is attributed to the variant that was KEPT, with
  discards priced as screening. This is the most honest unit available and
  it is exactly what critic is designed to produce.

**So throughput likely cannot be measured without critic**, which makes the
dependency worth naming early rather than discovering it halfway through.
An interim proxy (tasks closed per unit spend, per team, per day) may be
enough to detect the peak's EXISTENCE even if it cannot locate it precisely,
and detecting existence is what claim 1 needs.

## What marvel would have to instrument

Nothing exotic, and most of it is one aggregation away from data already
collected:

- Per-account rate-limit headroom, which requires reading response headers
  the harnesses receive and marvel currently does not see. This is the one
  genuinely missing input, and it may be the strongest argument yet for a
  channel that carries API response metadata rather than just token counts.
- Fleet-wide concurrent-agent count over time, trivially available.
- Aggregate spend rate, available from the same samples occupancy comes from.
- Queue depth and time-to-start for admitted work, which admission would
  produce as a side effect of refusing anything.
- A work-completion signal, which is the critic dependency above.

## Relationship to what already exists

- **Resource matrix**: this is the objective those seventeen constraint rows
  are implicitly optimizing. Worth deciding whether throughput is an
  eighteenth row or a different kind of thing entirely, because it is not a
  resource, it is what resources are spent ON.
- **Gap 1, operator attention**: attention is a shared resource that binds
  fleet throughput exactly as rate limits do, and it has no owner either. The
  two gaps may be the same gap seen from different sides.
- **Gap 5, cost and quality instrumentation**: the denominator problem above
  is Gap 5's problem, and solving it once serves both.
- **critic**: supplies the honest unit of work. Probably a hard dependency
  for the precise version and not for the existence version.
- **`internal/admission`**: the shipped seam where a fleet-level signal would
  first do useful work.

## What would kill this idea

- If the peak turns out to be far above any fleet size an operator actually
  runs, then throughput is a capacity-planning curiosity rather than a
  scheduling input, and the per-session view is sufficient.
- If rate limits turn out never to bind in practice on the plans this fleet
  uses, mechanisms 1 and 2 evaporate and only cache locality remains, which
  is a much smaller idea.
- If no honest unit of work can be defined, throughput cannot be measured and
  the right move is to instrument the CONSTRAINTS well and let the operator
  hold the objective in their head, which is the status quo made deliberate.

## Provenance

Fell out of `probe-context-operating-points-and-axes.md`, where fleet
throughput was the one axis with no instruments and the only one where a
per-session actuator can actively do harm. Operator asked for it to be
studied in its own right, 2026-08-08.

Related: `marvel-agentic-resource-matrix.md`,
`context-pressure-is-an-operating-point-not-a-fill-level.md`,
`probe-context-operating-points-and-axes.md`, `question-shift-triggers`,
vision Gap 1 (attention routing) and Gap 5 (cost and quality).
