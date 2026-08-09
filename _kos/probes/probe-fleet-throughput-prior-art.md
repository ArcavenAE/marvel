# Probe brief: fleet throughput, the prior art and what it would cost to use

**Status:** OPEN (literature pass complete; no measurement taken, no design
proposed).
**Question:** raised by `_kos/ideas/fleet-throughput-as-the-objective-function.md`,
which is itself pre-hypothesis.
**Medium:** literature and prior art. No model calls, no fleet runs, no code.
**bd:** aae-orc-hfc0.

## Scope, stated narrowly

The idea file argues that the agentic resource matrix is seventeen rows of
CONSTRAINT, that constraints are not an objective, and that marvel therefore has
no quantity it is maximizing and cannot tell whether a scheduling decision
helped. That framing is accepted here and not re-argued.

This document does one job: take the five bodies of prior art the idea file
names, state what each actually says, what it requires as input, what it would
predict for an agent fleet, and where it breaks down for this workload. Then it
assesses the measurement problem and answers one question: could an interim
proxy detect the EXISTENCE of a throughput peak even if it cannot locate one.

**This is not an implementation design.** No instrument, schema, or scheduler
behavior is proposed. Every number below was read from a cited source; none was
measured on this fleet.

## 1. Little's Law

**What it actually says.** For a stationary system, the long-run average number
of items in the system equals the long-run average effective arrival rate times
the average time an item spends in the system: `L = λW`. Proved in general form
by John D. C. Little in 1961. Its power is what it does not assume: the relation
is independent of the arrival distribution, the service distribution, the number
of servers, and the service order. The one real requirement is that a steady
state exists, which in practice means arrivals and departures balance over the
window of interest and both λ and W are finite.

**Required input.** Any two of the three terms, measured over the same window,
with a consistent definition of "in the system." Nothing else.

**What it would predict for an agent fleet.** Very little on its own, and that is
the point: it is a consistency check, not a model. If marvel claims 12 concurrent
agents, work arriving at 3 units per hour, and a mean time-in-system of 90
minutes, Little's Law says those three numbers are mutually inconsistent
(3 × 1.5 = 4.5, not 12) and one of them is wrong or the system is not in steady
state. The idea file is right to call it the cheapest possible sanity check on
any throughput claim.

**Where it breaks down here.** Three places, and the third is the serious one.

- *Steady state is doubtful.* A fleet that runs in bursts around an operator's
  working day, with 3 to 5 concurrent sessions and no arrival process to speak
  of, may never reach the stationarity the law needs. Little's Law applied to a
  non-stationary window is not wrong so much as meaningless.
- *"In the system" is undefined.* An agent blocked on operator approval is in the
  system by wall clock and out of it by any productive reading. Row 13 of the
  matrix (human attention) sits exactly on this ambiguity, and the definition
  chosen changes W by a large factor.
- *It needs a unit of work.* λ is arrivals of WHAT. This is the measurement
  problem below, and Little's Law does not escape it; it inherits it.

**Verdict.** Free to apply, useful as an audit, incapable of finding a peak.
Little's Law tells you your numbers are inconsistent. It never tells you what to
do.

## 2. The Universal Scalability Law (Gunther)

**What it actually says.** Gunther's USL models relative capacity as a function
of concurrency N:

> **X(N) = γN / (1 + α(N − 1) + βN(N − 1))**

with γ the ideal single-unit throughput (it sets the y-axis scale only), α the
CONTENTION parameter (the serialized fraction, queueing for a shared resource),
and β the COHERENCY parameter (the cost of keeping parties consistent, which is
quadratic because it grows with pairwise exchange). Amdahl's law is the special
case β = 0: with contention alone, throughput saturates at a ceiling. Adding the
coherency term is what makes throughput RETROGRADE, and it is why the USL is the
right family for the idea file's claim 1.

When β > 0 the peak has a closed form:

> **N_max = √((1 − α) / β)**

This is the single most useful equation in this document, because it says the
peak's LOCATION depends only on the two penalty parameters. γ does not appear.
Buying a faster model, or a bigger plan, moves the height of the curve and not
the position of its maximum.

**Required input.** Throughput measured at several distinct concurrency levels,
enough of them and spread widely enough to fit two parameters. Gunther's own
guidance is that a handful of well-spread points suffices; the fit is a
regression, not a physics experiment. Critically, it requires the SAME unit of
work at every point, which is again the measurement problem.

**What it would predict for an agent fleet.** Exactly the shape the idea file
claims and cannot currently see: output rises with agent count, peaks, and falls.
The mapping is unusually clean, cleaner than most systems the USL gets applied
to:

- **α, contention** is the shared per-account rate limit. Input and output TPM,
  TPD, and request rate are per-account and shared across the whole fleet. That
  is a serialized resource in the USL's exact sense.
- **β, coherency** is supervision, handoffs, review, and inter-agent messaging.
  The idea file's mechanism 4 (coordination overhead grows superlinearly) is a
  restatement of the coherency term, and the quadratic form has a concrete
  justification here: coordination cost that grows with pairwise agreement is
  what the M2 bus and the supervisor pattern are for.

The USL has been applied to human organizations on exactly this reading, with α
as failure to delegate and β as decision-making overhead requiring pairwise
agreement. That application is informal (a blog treatment of Gunther's own paper,
not a peer-reviewed result), and I record it as suggestive rather than as
evidence.

**Where it breaks down here.** Four places, in descending severity.

- *It needs a throughput number at multiple concurrency levels.* This is not a
  small ask. It is the entire measurement problem, multiplied by the number of
  operating points you need to fit. A fleet that runs 3 to 5 agents cannot fit a
  two-parameter curve over that range with any confidence.
- *N is assumed homogeneous.* The USL's N is interchangeable units. A marvel team
  is heterogeneous by construction (marvel B5: per-role replicas, supervisor plus
  reviewers plus devs). Adding a supervisor and adding a reviewer are different
  interventions with different α and β contributions, and the USL has one N.
- *Contention here is not just concurrency-driven.* Row 2 of the matrix
  interacts with row 1: an agent at 400k occupancy consumes roughly four times
  the input TPM per turn of one at 100k (the idea file's mechanism 2). So the
  shared resource is consumed in proportion to occupancy, not in proportion to
  agent count. The USL's α is a fraction of the workload, not a per-unit
  variable cost that changes with each unit's internal state. This is the
  cleanest place where the analogy strains.
- *Retrograde behavior may be masked by a hard limit.* When the account is rate
  limited, requests fail or back off rather than slowing gracefully. The USL
  models degradation; a quota models refusal. What an operator would observe past
  the peak might be errors, not a smooth decline, and the fitted β would be
  meaningless.

**Verdict.** The right model family, and the only one in this list with a peak
built in. Also the most expensive to use, because fitting it honestly requires
solving the measurement problem first and then running the fleet at several
concurrency levels. The closed-form N_max is worth carrying even without a fit,
because it makes one claim testable in principle: **if you can estimate α and β
by any means, you get a peak location without ever running the fleet at the
peak.**

## 3. Theory of constraints (Goldratt)

**What it actually says.** From *The Goal* (1984). Five focusing steps: identify
the constraint, exploit it (get maximum throughput from what you already have),
subordinate everything else to it, elevate it (invest to increase its capacity),
and repeat, because once you elevate a constraint something else becomes
binding. The governing insight is that improvement anywhere other than the
constraint is illusory.

**Required input.** Only the identity of the binding constraint. This is the
cheapest of the five to apply, by a wide margin: it needs no curve, no fit, and
no unit of work. It needs to know which resource is currently saturated.

**What it would predict for an agent fleet.** That most scheduling effort is
wasted. If human attention (row 13) is the binding constraint, then optimizing
context pressure, cache locality, and placement changes nothing measurable, and
the only intervention that matters is reducing approvals or coalescing
interrupts. TOC's specific contribution here is a warning about the idea file's
own list: admission, shift scheduling, cache-locality batching, placement, and
degradation policy are five candidate interventions, and TOC says at most one of
them is worth building at any given time.

**Where it breaks down here.** The constraint moves, and the idea file already
says so: rate limit at one operating point, human attention at another, host
memory at a third. TOC handles a moving constraint by iterating, but each
iteration assumes you can IDENTIFY the current constraint, and marvel currently
cannot. Identifying a constraint requires seeing utilization against capacity
per resource, and of the seventeen rows marvel meters compute (procstat), some
of spend, and nothing else. Rate-limit headroom in particular is invisible:
the idea file names it as the one genuinely missing input, because it lives in
API response headers the harnesses receive and marvel never sees.

**Verdict.** The highest ratio of usefulness to cost in this list, and the one I
would reach for first. It converts "build a fleet objective function" into
"instrument enough to name today's binding constraint," which is a far smaller
job and is a prerequisite for every other item here anyway.

## 4. Queueing theory and the utilization knee

**What it actually says.** For a G/G/1 queue, Kingman's formula (Kingman 1961)
approximates mean waiting time as the product of three factors, conventionally
written as the VUT form:

> **W ≈ ( ρ / (1 − ρ) ) × ( (c_a² + c_s²) / 2 ) × τ**

with ρ the utilization, c_a and c_s the coefficients of variation of interarrival
and service times, and τ the mean service time. It is a heavy-traffic
approximation, exact asymptotically as ρ → 1, and generally accurate near
saturation.

Three consequences matter here, and only the third is widely appreciated.

- The `ρ/(1−ρ)` term is the knee. At 50% utilization the factor is 1; at 80% it
  is 4; at 90% it is 9; at 95% it is 19. Waiting time is not linear in load and
  the last increments of utilization are catastrophically expensive.
- **Variability multiplies the knee, it does not shift it.** The VUT form is a
  product, so halving variability halves waiting time at every utilization. For
  a workload as variable as agent turns, this term is not a footnote.
- There is therefore a principled target utilization rather than a guessed one,
  and it depends on how much variability you have. The informal organizational
  guidance derived from this is 60 to 80 percent, weighted toward 60 when
  variability is high. I record that as practitioner guidance, not as a result.

**Required input.** Utilization of the constrained resource (so, its capacity,
which for a rate limit means reading headers marvel does not see), plus the two
coefficients of variation, plus mean service time.

**What it would predict for an agent fleet.** That agent turn times blow up well
before the account's rate limit is nominally exhausted, and that they blow up
worse than a naive reading suggests because agent service times are wildly
variable. c_s for agent turns is plausibly large: a turn may be one short tool
call or a twenty-minute reasoning-and-edit cycle. A high c_s means the knee
arrives early.

**Where it breaks down here.** Two structural mismatches.

- *There may be no queue.* Kingman describes a queue with a server. Agent work is
  mostly not queued today; it is dispatched by an operator, runs to completion,
  and the "waiting" is a rate-limit backoff rather than a queue discipline.
  `internal/admission` refuses over-budget work rather than queueing it, so
  marvel does not currently even have the queue Kingman's formula would describe.
  The formula would become applicable if admission started queueing, which makes
  it prospective rather than current.
- *Utilization of what?* Rate limits are multi-dimensional (input TPM, output
  TPM, TPD, requests per minute) and a fleet can be at 30% of one and 95% of
  another. Single-ρ models do not handle that, and which dimension binds first
  is exactly the TOC identification problem above.

**Verdict.** The right intuition (nonlinear cost of high utilization, variability
as a multiplier), applied to a system that does not yet have the queue it
describes. Its most useful export today is the variability term, because it says
that reducing VARIANCE in agent turn cost is worth as much as reducing the mean,
and nothing in marvel currently treats variance as a target.

## 5. Goodput versus throughput

**What it actually says.** From networking: throughput counts all bits moved,
including protocol overhead, retransmissions, and data later discarded. Goodput
counts only useful data delivered to the application layer. The distinction
exists because a link can show high throughput while delivering little, and users
experience goodput.

**Required input.** A rule for classifying output as kept or discarded. That is
all, and it is the whole difficulty.

**What it would predict for an agent fleet.** That the naive numerator is
inflated. Rework, retried turns, abandoned branches, and superseded edits are
all throughput and none of it is goodput. The idea file's framing is right and
worth restating precisely: under generate-and-select, discarded variants are
SCREENING cost, not waste, but only if they are counted as screening rather than
as production. Goodput gives the vocabulary for that distinction without
requiring a judgment about whether the discards were worthwhile.

**Where it breaks down here.** The classification rule is the hard part and
networking does not supply one. In a network, a retransmitted packet is
unambiguously overhead; in agent work, deciding whether a discarded variant was
screening or waste is precisely the judgment that critic exists to make. So
goodput is a frame rather than a method, and it borrows its difficulty back from
the measurement problem.

**Verdict.** Keep as vocabulary. It sharpens claim 3 of the idea file (cost per
unit of DELIVERED work) and contributes no measurement apparatus.

## 6. One piece of prior art the idea file does not name

Bartolucci and Vivo, *Queue & AI: When Faster Tasks Slow Down the Workflow*
(arXiv:2605.27202, submitted 26 May 2026), models this workload class directly
and reaches a conclusion the idea file would want to know about.

From the abstract: mean-based per-task productivity metrics (tasks per
worker-hour, mean handle time) "can misrepresent AI's effects in workflows where
tasks accumulate and compete for scarce human attention." They name the failure
the **variance wedge**: average completion times fall because AI supplies a fast
first draft, while workflow-level performance deteriorates when a subset of AI
errors escapes review and returns as costly downstream REWORK. They formalize it
as a queueing model and derive two implications: under congestion, reviewers
rationally raise the risk threshold for checking AI outputs, reducing scrutiny
exactly when it matters most; and AI assistance stabilizes an overloaded workflow
only when the AI-handled fraction exceeds a critical threshold AND the human
attention required for review plus expected rework is lower than the attention
for manual completion, which they describe as "substantially more stringent than
faster draft generation."

Three reasons this matters more than its late position suggests.

1. **It is an independent derivation of the idea file's central claim.** Adding
   AI capacity to a workflow can reduce workflow-level output. Same shape as the
   USL peak, reached from queueing rather than from scalability modeling.
2. **It identifies human attention as the binding constraint**, which is matrix
   row 13 and vision Gap 1, the row with no owner. The idea file already
   suspects Gap 1 and the throughput gap are the same gap seen from two sides.
   This paper argues they are.
3. **Its mechanism is rework**, which is exactly the proxy the sibling probe
   brief (`brief-rework-detection-reliability-knee.md`) proposes measuring for a
   different question. If rework is both the degradation signal for the
   reliability knee and the goodput leak for fleet throughput, one measurement
   serves two open questions.

I have read the abstract in full and verified title, authors, and date on the
arXiv listing. **I have NOT verified the paper's analytical results or any
quantitative claim in its body**; the PDF fetch returned structure without the
numeric content. Treat everything above as the authors' stated claims, not as
confirmed results.

## 7. The measurement problem

The idea file names four candidate units and rejects three. I agree with the
rejections and add the reason each survives rejection anyway in some contexts.

| Unit | Failure mode | Salvageable as |
|---|---|---|
| Output tokens | rewards verbosity; twice the words for the same content scores double | a COST term, never a numerator |
| Turns or sessions | rewards churn; penalizes getting it right first time | a denominator for per-turn cost |
| Tasks closed | game-able; needs comparable task granularity across teams | the interim proxy, with conditions (below) |
| Per selected outcome | needs critic, which is pre-code | the honest unit, unavailable |

Two observations the idea file does not make.

**Every rejected unit fails in the same direction.** Output tokens, turns, and
sessions all reward MORE ACTIVITY. That is not a coincidence; it is what happens
when you measure an agent's exhaust rather than its product. So any proxy built
from activity counts will overstate throughput exactly when the fleet is
thrashing, which is the regime where the peak lives. A proxy that is biased
upward past the peak cannot find the peak; it can only hide it. This is the
strongest argument in this document for why the interim proxy question needs a
careful answer rather than a hopeful one.

**Goodput and the per-selected-outcome unit are the same idea at different
prices.** Both ask "how much of what was produced was kept." Critic supplies the
judgment; goodput supplies the vocabulary. If critic is the dependency, then the
cheapest thing that resembles critic is a rule that classifies an output as kept
or discarded without a judge. Reverted commits, superseded edits, and abandoned
branches are all mechanically detectable and all are discards.

## 8. Can an interim proxy detect the EXISTENCE of a peak?

This is the question worth answering, because claim 1 of the idea file (there is
a peak, and running past it is worse than stopping short) needs existence only.
Locating it precisely is a later and much more expensive problem.

**My assessment: yes in principle, no with the proxy the idea file proposes, and
the difference is worth stating.**

The idea file suggests "tasks closed per unit spend, per team, per day." That
proxy has one fatal property and one fixable one.

*Fatal:* it is an activity-derived numerator, so it inherits the upward bias
named in section 7. Past the peak, a thrashing fleet closes tickets that are
rework of tickets it closed earlier. Both closures count. The numerator rises
while goodput falls, which is precisely the variance wedge Bartolucci and Vivo
describe, and it is the case where the proxy reports the opposite of the truth.

*Fixable:* it needs task granularity comparable across teams. Restricting to
within-team comparisons over time removes this, at the cost of not being able to
compare teams.

**What detecting existence would actually require**, in ascending cost, none of
which I am proposing to build:

1. **A discard-aware numerator that needs no judge.** Tasks closed MINUS tasks
   reopened, or commits landed MINUS commits reverted, over a window long enough
   for the rework to surface. The subtraction is what breaks the upward bias:
   past the peak, both terms rise and the difference falls. This is goodput with
   a mechanical classifier standing in for critic, and it is the only cheap
   candidate I would defend. The load-bearing parameter is the window: rework
   that surfaces after the window is invisible, and a window too long makes the
   measurement useless for scheduling.
2. **A natural experiment over concurrency.** Fleet output against concurrent
   agent count, where concurrency varies for reasons unrelated to output. Marvel
   already records concurrent agent count over time, or could trivially. The
   catch is severe and it is the same catch as SP1's survivorship problem in
   `probe-context-operating-points-and-axes.md`: an operator who runs many agents
   only on days when there is a lot of work confounds concurrency with demand,
   and the resulting curve measures the operator's habits.
3. **Deliberate concurrency variation.** Fix the workload, vary agent count, use
   the USL fit. The only design that escapes confounding, and it costs quota and
   operator time. If a peak matters enough to schedule around, this is the honest
   price of finding it.

**The negative result worth recording in advance.** Two of the idea file's own
kill conditions can be tested far more cheaply than the peak can be found, and
both should be tested first:

- *Does the rate limit ever actually bind?* If it does not, mechanisms 1 and 2
  evaporate and α is near zero, which by N_max = √((1−α)/β) pushes the peak
  outward and makes it a capacity-planning curiosity. This is checkable by
  reading the rate-limit headers the harnesses already receive, which is one
  channel away rather than one system away.
- *How far out is the peak?* If α and β can be estimated at all, the closed form
  gives a location without running the fleet there. A peak at N = 40 on a fleet
  that runs 3 to 5 agents settles the whole question against the idea file, and
  costs an estimate rather than an experiment.

Running the kill conditions before the measurement is the cheapest sequencing
available, and it is the one this document recommends over any instrument.

## 9. Where each body of prior art actually lands

| Prior art | Cost to apply | What it gives | Verdict for marvel |
|---|---|---|---|
| Little's Law | free | consistency audit | use as a check on any throughput claim; never as a model |
| USL | high | a peak, with a closed-form location | right model family; blocked on the unit of work |
| Theory of constraints | low | which resource to care about now | **do this first**; blocked only on naming the constraint |
| Kingman / queueing knee | medium | nonlinear cost of utilization; variance as a target | prospective, once admission queues rather than refuses |
| Goodput | free | vocabulary for kept versus produced | keep the vocabulary; it supplies no method |
| Bartolucci and Vivo | free to read | independent derivation, rework mechanism, attention as constraint | strongest single pointer; ties this question to Gap 1 and to rework detection |

## What this probe did NOT establish

- **No number was measured on this fleet.** Not concurrency, not rate-limit
  headroom, not tasks closed, not reverted work. Every quantity in this document
  came from a cited source about some other system.
- **Whether the peak exists on this fleet at all.** Section 8 says what would
  detect it; nothing here detects it.
- **Whether throughput is an eighteenth matrix row or a different kind of thing.**
  The idea file raises this and I have no basis to rule. My reading leans toward
  different-kind-of-thing (resources are spent, objectives are maximized), but
  that is an opinion about vocabulary, not a finding.
- **Bartolucci and Vivo's analytical results.** Abstract verified, body not read.

## Sources

- Little's Law: [Little's law (Wikipedia)](https://en.wikipedia.org/wiki/Little%27s_law); [Sigman, Notes on Little's Law, Columbia](http://www.columbia.edu/~ks20/stochastic-I/stochastic-I-LL.pdf); [Whitt, Notes on Little's Law, IEOR 4615, Columbia](https://www.columbia.edu/~ww2040/4615S15/LittlesLawNotes012715.pdf)
- Universal Scalability Law: [Gunther, How to Quantify Scalability (Performance Dynamics)](https://www.perfdynamics.com/Manifesto/USLscalability.html); [PDF synopsis](https://www.perfdynamics.com/Manifesto/USLscalability.pdf); [usl R package vignette (CRAN)](https://cran.r-project.org/web/packages/usl/vignettes/usl.pdf)
- USL applied to organizations: [Colyer, Applying the Universal Scalability Law to organisations, the morning paper, 2015-04-29](https://blog.acolyer.org/2015/04/29/applying-the-universal-scalability-law-to-organisations/)
- Theory of constraints: Goldratt, *The Goal* (1984); [Lean Production, Theory of Constraints](https://www.leanproduction.com/theory-of-constraints/); [Umbrex, Five Focusing Steps](https://umbrex.com/resources/frameworks/organization-frameworks/theory-of-constraints-five-focusing-steps/)
- Kingman's formula: [Kingman's formula (Wikipedia)](https://en.wikipedia.org/wiki/Kingman%27s_formula), citing Kingman, "The single server queue in heavy traffic" (1961); [AllAboutLean, The Kingman Formula](https://www.allaboutlean.com/kingman-formula/)
- Goodput: [Packet Pushers, What is the difference between throughput and goodput?](https://packetpushers.net/blog/what-is-the-difference-between-throughput-goodput/); [Obkio, Goodput vs Throughput](https://obkio.com/blog/goodput-vs-throughput/)
- Queueing model of AI-assisted workflows: Bartolucci, S. and Vivo, P., *Queue & AI: When Faster Tasks Slow Down the Workflow*, [arXiv:2605.27202](https://arxiv.org/abs/2605.27202), submitted 2026-05-26. Abstract read in full; body not read.
