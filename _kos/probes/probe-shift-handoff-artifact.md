# probe-shift-handoff-artifact: what a successor reads, when its predecessor writes it, and what that costs

**Date:** 2026-08-08
**Status:** DESIGN PROBE. Not a finding. Nothing here is measured against a
running handoff, because none exists. The parts I read out of this
repository or ran are marked MEASURED; the parts I reasoned to are marked
HYPOTHESIS; the parts I took from a document rather than from software are
marked INFERRED.
**Question:** `question-shift-triggers` (this is its blocking measurement,
per finding-018 consequence 2). Bears on `question-marvel-graceful-stop`
and `question-agent-communication-broker`.
**Designs within:** `elem-handoff-schema-ownership` (bedrock, ratified
2026-08-01): marvel owns the schema, meaning, location, lifecycle, and
generation binding; the departing agent owns the content. That split is
settled and is not reopened below.
**Prior:** finding-018 (cold-shift wall clock, price classes),
finding-016 (compaction median, what survives a compaction),
finding-011 (the statusline side channel), finding-024 (policy
projection), finding-015 (a finished headless role reads as a crash).

## Why this is the blocking item and not a nicety

finding-018 measured a one-role shift at 8.92 s median against
compaction's 154 s median (finding-016), then said plainly that the two
numbers do not measure the same thing. A compaction preserves the working
context, compressed. A shift preserves nothing. Marvel today hands the
successor an empty desk, and the shift is fast partly because packing the
desk is work it never does.

So the end-to-end comparison is undetermined, and it stays undetermined
until a successor has something to read. This document designs that
something, prices it, and pre-registers the experiment that would settle
the comparison.

## What I established from the code, before designing anything

| fact | how |
|---|---|
| `grep -ri handoff --include="*.go"` returns 0 hits repo-wide | MEASURED, re-run in this worktree |
| `shiftDrain` deletes one old-gen session per tick via `sessMgr.Delete`, which calls `retireInstance` then `KillPane`. No signal, no wait, no ack | MEASURED, `internal/team/controller.go`, `internal/session/manager.go:868` |
| `instanceTeardownGrace = 3 * time.Second` bounds marvel's own stream teardown, not the agent's turn | MEASURED, `internal/session/manager.go:809` |
| `ReconcileInterval = 2 * time.Second`; `reconcileShift` advances at most one phase per tick | MEASURED, `internal/daemon/daemon.go:48` |
| Generation already exists and is already in the session name: `<team>-<role>-g<gen>-<index>` | MEASURED, `shiftLaunch` |
| Marvel already constructs the pane environment: `MARVEL_SESSION`, `MARVEL_ROLE`, `MARVEL_TEAM`, `MARVEL_WORKSPACE`, `MARVEL_SOCKET` | MEASURED, `internal/runtime/adapter.go:160` |
| An in-pane process already calls the daemon back over `MARVEL_SOCKET` using those env vars: `marvel ctx-forward` posting a `heartbeat` | MEASURED, `cmd/marvel/ctxforward.go:206` |
| Marvel already writes a per-session file the harness reads, verbatim, and already grades adapters on whether they can read one (`ProjectionTarget.Supported`, `StatuslineFeeder`) | MEASURED, `internal/session/projection.go`, `internal/runtime/adapter.go` |
| Marvel does not set a pane working directory: no `-c` flag anywhere in the tmux driver, so pane cwd is inherited and marvel does not own it | MEASURED, `internal/tmux/driver.go` |
| `marvel capture` returns literal pane scrollback, and `NewSession` raises `history-limit` above tmux's 2000-line default | MEASURED, `internal/tmux/driver.go:197`, `handleCapture` |
| The event ring already carries per-turn token counts (`agent.turn.completed ... tokens in=19235 out=5`) | MEASURED, quoted transcript in finding-015 |
| Marvel emits no event when a successor becomes ready | MEASURED by finding-018, re-confirmed against `allReady` |

Two of these are load-bearing and easy to miss. Marvel already owns a
one-way file contract with the harness (projection) and already owns a
one-way callback contract from the pane (ctx-forward). A handoff needs
exactly those two contracts and one thing neither provides: a way for
marvel to ask.

## 1. What the artifact is

### The envelope

Marvel owns every field except two. Proposed v1, and `schema_version` is
required because every declarative contract in this fleet carries one:

```json
{
  "schema_version": 1,
  "workspace": "mixed",
  "team": "matrix",
  "role": "analyst",
  "predecessor": {
    "session_key": "mixed/matrix-analyst-g2-0",
    "generation": 2,
    "runtime": "claude",
    "model": "claude-haiku-4-5"
  },
  "successor_generation": 3,
  "requested_at": "2026-08-08T17:03:11Z",
  "finalized_at": "2026-08-08T17:03:19Z",
  "reason": "shift",
  "completeness": "authored",
  "content_origin": "agent",
  "content_type": "text/markdown",
  "content_tokens_estimate": 1840,
  "content": "...verbatim, agent-authored, never read by marvel..."
}
```

`content` and `content_type` are the departing agent's. Marvel writes
everything else and never parses, summarizes, rewrites, or truncates
`content`. That is the ratified split, and section 5 argues it is also the
technically correct one under this project's own measured position on
summarization.

Three envelope fields carry the design's honesty and deserve naming:

- **`completeness`**: `authored` (the agent finished), `partial` (the
  agent acknowledged and a partial write landed before the deadline),
  `absent` (nothing arrived), `involuntary` (marvel attached observations
  because the agent could not be asked). Marvel owns this field because it
  is a fact about the transaction, not a judgment about the content. The
  agent may declare `authored` or `partial`; marvel overwrites with
  `absent` or `involuntary` when it must.
- **`content_origin`**: `agent` or `observed`. The involuntary path
  (section 2) attaches pane scrollback and the session's event slice,
  which are props rather than an account. A successor must be able to tell
  a predecessor's reasoning from a transcript of its keystrokes.
- **`model`**: finding-016's ruling that model identity is part of a
  reading's identity applies here too. A handoff authored under one model
  and read under another is not obviously comparable, and a successor that
  can see the discontinuity can act on it.

The content field is untrusted data, not instruction. Marvel cannot
enforce that, and section 2 FM-D says so plainly.

### Location

Requirements, in order of how much they constrain:

1. A successor must find it without being told in prose.
2. It must survive a daemon restart, because a session outlives the daemon
   that spawned it (adopt-on-restart is shipped).
3. Marvel must not write it into the user's working tree. Marvel does not
   own a pane's cwd (MEASURED: no `-c` in the driver), and the repo rule
   is that marvel never deletes user files. A retention policy over files
   in someone's project directory would collide with both.

Proposal: `~/.marvel/handoff/<workspace>/<team>/<role>/g<N>.json`, resolved
through `paths.Layout` alongside `StateDir()` and `RunDir()`, dirs 0700 and
files 0600 per the layout's existing mode discipline.

Not the projection directory, and the reason is recorded in the code
already. `defaultProjectionDir` carries a comment explaining that a
pid-keyed temp directory broke adoption: a restarted daemon computed a
different path, rewrote a file no agent was reading, and a policy edit
appeared to apply while changing nothing. A handoff is more durable than a
projected settings file, not less, so it belongs under the layout's stable
home rather than a temp directory.

Discovery is two environment variables added to `BaseEnv`, which is the
only discovery channel that is both harness-agnostic and already shipped:

- `MARVEL_HANDOFF_IN`: absolute path to the predecessor's artifact.
  **Set only when a bound predecessor artifact exists.** Absence is the
  signal that the successor is starting with nothing.
- `MARVEL_HANDOFF_OUT`: absolute path this session writes to when asked.

Setting `MARVEL_HANDOFF_IN` to a path that does not exist would be the
silent-wrong failure `internal/usage/doc.go` already refuses in the
denominator case. Absent means absent.

### Generation binding, and how a stale read is prevented

The rule is strict predecessor binding: marvel sets `MARVEL_HANDOFF_IN`
for a session at generation N only if an artifact exists whose
`successor_generation == N`. Not "the newest artifact for this role", and
not "the highest generation below N".

That distinction is the whole answer to "cannot read a stale one". If
generation N-1 left nothing, the newest artifact on disk is from N-2, and
handing it forward would give the successor a confident account of a state
two generations gone. A gap must read as a gap.

Three cases the binding has to survive, all reachable with shipped code:

- **Rollback.** `abortStuckShift` rolls a failed shift back to
  `OldGeneration`. If the aborted generation wrote an artifact, that
  generation never operated, so its artifact must not be handed to the
  next attempt. Marvel marks it `superseded: true` rather than deleting it
  (the no-deletion posture applies to a file whose content the agent
  authored), and superseded artifacts are never bound.
- **Multiple replicas.** A role with N replicas produces N artifacts per
  generation. The filename needs the replica index, and binding is
  index-to-index. Whether index-to-index is even meaningful when
  `nextIndex` does not gap-fill is an open sub-question; I have not
  resolved it and it is listed in section 8.
- **Restart, not shift.** A restarted replica keeps its generation. It is
  not a successor to itself, so a restart must not bind. Binding on
  `successor_generation` rather than on session key gets this right by
  construction.

### Lifecycle

Requested at the moment `allReady` currently flips a shift to draining.
Finalized at write or at deadline. Read once, at the successor's spawn.
Retained per role as a bounded ring (three generations is a starting
number, no evidence behind it) so an operator can read what was lost.
Never mutated after finalization except for the `superseded` flag.

Bounded retention follows this codebase's own habits rather than
inventing one: the event ring is bounded, and `ReapDead` keeps at most one
crashed marker per role.

## 2. When it is written

### The change to the state machine

Today: `ShiftLaunching` -> (`allReady`) -> `ShiftDraining` -> kill.

Smallest change that creates a window: one phase and one deadline.

```
ShiftLaunching -> (allReady) -> ShiftHandoff -> (all finalized OR deadline) -> ShiftDraining
```

- New `ShiftPhase` constant `ShiftHandoff`.
- New `ShiftState.HandoffDeadline time.Time`, set on entry.
- New `Role.HandoffGrace` (duration) and `Role.Handoff` (`best-effort` |
  `off`), following how `MaxRestarts` and `RestartPolicy` already sit on
  `Role`.
- `reconcileShift` gains one case. `shiftDrain` is untouched.
- `abortStuckShift` gains the new phase in its rollback handling. This is
  a real touchpoint, not a formality: an abort inside handoff must not
  leave a half-written artifact bound.

`Handoff = off` reproduces today's behavior exactly, which is what keeps
every existing manifest's timings intact.

### What it costs against the measured 8.92 s

The measured per-role cost is four reconcile ticks (7.99 s at the 2 s
default). The handoff phase adds one tick to observe, plus the agent's
authoring time rounded up to a tick.

MEASURED baseline, INFERRED additions:

| case | added ticks per role | added seconds at 2 s tick |
|---|---|---|
| `Handoff = off` | 0 | 0 |
| predecessor pane already gone | 1 | 2.0 |
| no acknowledgement within one tick | 1 | 2.0 |
| agent authors in 8 s | 5 | 10.0 |
| agent authors in 30 s | 16 | 32.0 |
| agent acks then never finishes, 60 s grace | 31 | 62.0 |

**This is the design's largest liability and I will not soften it.** A
three-role team whose agents each burn a 60 s grace costs 3 * (7.99 + 62)
= 210 s, which loses to the 154 s compaction that finding-018's headline
beat by an order. The handoff does not merely add cost; at a long grace it
can invert the finding it was built to complete.

Two consequences follow. First, the grace default must be short, and I
have no evidence for a number, so section 7 pre-registers measuring it
rather than guessing it. Second, the unresponsive cases must short-circuit
rather than wait the grace out, which is what the two-step protocol below
buys.

### The two-step protocol, and why the second step is cheap and the first is not

**Agent to marvel is already solved in shape.** `marvel handoff write`,
invoked from inside the pane, reads `MARVEL_SOCKET`, `MARVEL_WORKSPACE`,
`MARVEL_SESSION` from the environment and posts to a new daemon method
alongside `heartbeat`. This is `marvel ctx-forward` with a different verb.
No new transport, no new trust boundary, and the daemon's request
dispatcher is a switch over method names (MEASURED, `handleRWC`).

**Marvel to agent is not solved, and this is where the design stops.**
What ships today is `marvel inject`, which is `tmux send-keys` and works
only against an interactive pane, plus nothing at all for headless. The
clean channel for "marvel asks an agent to do something" is the M2 per-team
bus, which is unbuilt and ruled to be external NATS supervised by marvel.
**I name that dependency and stop. I am not inventing a transport.**

What can ship before the bus is a capability ladder that mirrors one this
codebase already has. `ProjectionTarget.Supported` grades adapters on
whether the harness reads a settings file marvel chooses, and marvel's
ruling for adapters that cannot is to log the policy as advisory rather
than write into the user's own config. The same grading applies:

| adapter capability | request delivery | artifact |
|---|---|---|
| harness exposes a per-turn hook surface marvel already writes (the projection path) | marvel drops a request file; the hook checks it at a turn boundary and runs `marvel handoff write` | `authored` |
| no hook surface, pane alive | none available pre-bus | `involuntary` |
| pane gone | none possible | `involuntary` or `absent` |

I have NOT verified that any harness has a hook that fires at a usable
point. finding-011 established that Claude Code exposes `statusLine` and
`subagentStatusLine` and that marvel writes its settings verbatim, which
establishes the mechanism but not the event. Asserting a specific hook name
here would be reading a document and calling it software. Section 7
registers verifying the hook surface as the first step of the follow-on
probe, and section 8 records it as unestablished.

### The involuntary path, which is the answer to the motivating case

The prompt's objection is correct and it deserves a direct answer: a
mechanism that works only when the agent is healthy does not serve the case
that motivates shifts.

Two halves to the answer.

**Half one: the primary trigger's agent IS healthy.** The trigger
`question-shift-triggers` is actually about is context pressure. An agent
at 95% occupancy is not sick, it is full. It can still author. So the
cooperative path does serve the leading trigger, and saying otherwise
overstates the problem.

**Half two: every other trigger gets an involuntary artifact, built only
from things marvel already holds.** When the agent cannot be asked, marvel
attaches, verbatim:

- the last N lines of pane scrollback (`marvel capture` is shipped, and
  `NewSession` already raises `history-limit` above tmux's default), and
- the session's slice of the event ring, which already carries the 12
  `agent.*` kinds including per-turn token counts.

Marvel is copying, not summarizing, which is why this does not violate
section 5's discipline and does not violate the ownership ruling either:
marvel is not writing the departing agent's account, it is attaching a
transcript, and `content_origin: observed` says which one the successor is
holding.

HYPOTHESIS, and it is testable as arm A1 in section 3: an involuntary
artifact is worth strictly more than nothing and strictly less than an
authored one. If it turns out to be worth nothing, the fallback should not
be built, and the experiment is designed to say so.

### Named failure modes

- **FM-A, predecessor already dead.** Pane gone; `ReapDead` has marked the
  session crashed, or `HasPane` reports false. No request is possible.
  Artifact is `involuntary` if scrollback survives the pane (it does not,
  once the pane is destroyed, which means the involuntary capture must be
  taken at the moment of the request, not at drain), else `absent`. Cost:
  one tick. **This ordering constraint is real and easy to get wrong: the
  observed content must be captured before anything kills anything.**
- **FM-B, alive but not listening.** No adapter courier, or the harness is
  mid-tool-call. No acknowledgement within one tick, so marvel classifies
  the session `unreachable` and proceeds. This is the mechanism that keeps
  the common unhealthy case at one tick instead of a full grace, and it is
  the reason the protocol is ack-then-write rather than a single write.
- **FM-C, acks then misses the deadline.** `partial` if bytes landed,
  `absent` otherwise. Costs the full grace. This is the expensive case in
  the table above.
- **FM-D, writes something wrong or hostile.** Marvel does not interpret
  content, so marvel cannot detect this. A predecessor's authored text
  becomes a successor's read text, which is exactly the agent-to-agent
  injection hazard finding-016 already records as an ordinary rather than
  exotic event. The envelope can label content as data; it cannot make a
  model treat it that way. Recorded as a limit, not solved.
- **FM-E, shift times out inside the handoff phase.** `abortStuckShift`
  fires and rolls back. The default `ShiftTimeout` is 10 minutes
  (MEASURED, `defaultShiftTimeout`), so a grace long enough to trip it
  would have to be pathological, but the phase must still be handled in
  the abort path.
- **FM-F, a headless role that already finished.** finding-015 established
  that a headless role which completes its job reads as `crashed`. Such a
  session has no turn to be asked at, and treating its exit as a handoff
  failure would be wrong. A finished headless job is not a shift's
  predecessor in any interesting sense; it should be excluded from the
  handoff phase rather than counted as `absent`.

### The relationship to graceful stop

`question-marvel-graceful-stop` asks for exactly this window under a
different verb: announce, signal, wait a per-role grace, then destroy. That
node has been open since 2026-05-13 and is explicit that nothing in marvel
sends a graceful-shutdown signal or waits out a grace period.

These should be one mechanism with two callers, not two mechanisms. The
per-role grace field, the ack protocol, and the capability ladder are the
same in both. If handoff ships its own private drain, graceful stop will
later ship a second one and they will disagree. Naming this now is cheaper
than reconciling it later.

## 3. What the successor does with it, and how that becomes measurable

### The read

The successor's adapter reads `MARVEL_HANDOFF_IN` and places `content`
into the session's opening context. Delivery is per-adapter, same ladder as
the request: an adapter with a settings or system-prompt surface can do it
at spawn; the generic fallback cannot, and its sessions get the env var
pointing at a readable file with nothing that makes the model look at it.

That last clause is a genuine hole and I am not going to paper over it.
Setting an environment variable makes the path available to the process. It
does not make the model read the file. Whether the model acts on it is a
per-adapter prompt question, and marvel's independence requirement means
the CONTRACT (envelope, location, binding) is harness-agnostic while the
DELIVERY is graded. That is the same shape as policy projection, where
marvel's answer for an adapter with no settings surface is to log the
policy as advisory rather than write into the user's own config.

### The experiment

finding-018 said its measurement could not distinguish "one turn suffices"
from "fifteen turns needed" because the artifact that would define
sufficiency did not exist. The metric that closes that gap:

**Turns to recovery (TTR).** Set a task with a pre-declared, machine
checkable completion predicate. Run a predecessor to a fixed state short of
the predicate. Shift. Count the successor's turns until it satisfies the
predicate.

Arms:

| arm | what it is | what it establishes |
|---|---|---|
| A0 | shift, no artifact (today) | baseline; the number finding-018 could not obtain |
| A1 | shift, involuntary artifact (scrollback + event slice) | whether the non-cooperative fallback is worth building |
| A2 | shift, authored artifact | the cooperative case |
| A3 | no shift, predecessor continues | control; TTR must be 0, proving the predicate is satisfiable and the task is not the problem |
| A4 | compaction instead of a shift | the actual comparator, and the arm that settles finding-018's open verdict |

The end-to-end comparison the record calls undetermined is A2 against A4,
priced in both wall clock and dollars.

Instrumentation is already built. Turn counts and token counts come from
`agent.turn.completed` in the event ring; wall clock comes from the ring's
RFC3339 stamps; dollars come from the price classes finding-018 derived.
The one instrument that is missing is the readiness event finding-018
already asked for, and the handoff phase makes that worse rather than
better: it adds three more unobservable instants (requested, acknowledged,
finalized). The phase should ship with its events from the first commit.

Confounds to pre-register, because the first two can silently produce a
null result that looks like a finding:

- **The task must have private working state.** If the predecessor's state
  is recoverable from the filesystem, a coding task with the work on disk,
  then TTR is near zero in every arm and the experiment measures nothing.
  The task needs state that lives only in the session: a plan, a
  ruled-out list, a hypothesis under test, a judgment about which of three
  approaches is dead. This is the single largest design risk in the
  experiment.
- **Authoring quality is a free variable.** The same predecessor prompt
  must author in every A2 run, and cross-run variance must be reported
  rather than averaged into a median.
- **The cache identity trap.** finding-018's three-field rule applies:
  cold requires `cache_creation > 0` AND `cache_read == 0` AND
  `is_error == false`. Both-zero is undetermined.
- **Model identity.** Per finding-016, every number is per-model and a
  mid-run model change invalidates rather than averages.
- **Credentials.** finding-018's arm D could not run a real harness under
  an isolated HOME because the isolated HOME carries no credentials. This
  experiment needs a real harness under marvel, which is exactly the
  combination that failed. Solving it is a prerequisite, not a detail, and
  it is the most likely reason this probe's follow-on does not run as
  designed.

## 4. How much it may cost

All prices from finding-018's derived classes (claude-haiku-4-5, five
runs, reproduced to 0.15%): output $5.011/MTok, cache read $0.245/MTok,
cache write $2.145/MTok, write:read ratio 8.77x. Everything in this section
is INFERRED arithmetic over those MEASURED prices, and per finding-016 it
is model-dependent.

**Writing.** Three components, and the one that dominates is the one that
is easy to miss.

| component | tokens | class | cost |
|---|---|---|---|
| predecessor re-reads its context to author | 467,446 (finding-016 p50 occupancy at compaction) | cache read | $0.1145 |
| predecessor emits the handoff | 2,000 | output | $0.0100 |
| **write subtotal** | | | **$0.1245** |

The authoring turn's re-read is 92% of the cost of writing a handoff. That
is not a handoff-specific cost, it is what any turn at that occupancy costs,
which is the point: **asking for a handoff at the pressure point costs
approximately one ordinary turn.**

**Reading.** The successor is cold, so the artifact lands in cache-write
class on its first request.

| handoff size | cache write cost | as % of the measured cold re-warm ($0.1663 for 77.5k) |
|---|---|---|
| 2,000 tokens | $0.0043 | 2.6% |
| 4,000 tokens | $0.0086 | 5.2% |
| 20,000 tokens | $0.0429 | 25.8% |

**Total for one cooperative handoff at the pressure point: about $0.13.**

**What it has to save to pay for itself.** A successor turn at 95k
occupancy costs 95,428 * $0.245/MTok = $0.0234 in cache read. So the
artifact breaks even if it saves roughly five successor turns. At the
occupancy where shifts actually get triggered, a turn costs $0.1145, and
the artifact breaks even if it saves **one turn**.

That is the useful shape of the answer, and it holds regardless of where
the true TTR lands: the cost of the handoff is bounded by roughly one turn
at the trigger point, so the artifact is cheap exactly in the regime where
it is needed. HYPOTHESIS, since the saving is the unmeasured quantity the
section 3 experiment exists to obtain.

**On a ceiling.** A manifest-declared token cap is tempting and I recommend
against a bespoke one. Truncating is interpreting-adjacent, and refusing an
over-cap write discards everything the agent just spent a turn producing.
Marvel should record `content_tokens_estimate` and let a hard ceiling live
where ceilings already live: `internal/admission` and the declared team
budget, which is the shipped surface for refusing over-budget work.

## 5. What the stage-rig discipline implies

Orc charter F25 rules that backdrops are AUTHORED and never auto-summarized
from props, and cites the measured basis: 97.5% recall with verbatim
artifacts against 19% under recursive summarization (CogCanvas, arXiv
2601.00821).

**A handoff is exactly a backdrop.** Shape preserved, detail absent,
written so a successor can orient. Four things follow, and the third and
fourth are not obvious.

1. **Marvel must never generate the handoff by summarizing the
   predecessor's context.** This is the strongest constraint in the whole
   design, and it is what rules out the otherwise attractive option of
   having marvel read the session stream it already parses and produce a
   summary with no agent cooperation at all. That option would work in
   every failure mode, require no bus, and cost nothing at the pressure
   point. It is ruled out by the project's own measurement, not by taste.
2. **The departing agent authoring its own handoff IS an authored
   backdrop.** So the 2026-08-01 ownership ruling and F25 agree, and the
   ruling is not merely a governance preference: it is the arrangement
   F25's evidence points at. That is worth stating because it means the
   design has no live tension with F25 on the cooperative path.
3. **The involuntary path is props, not a backdrop, and that is correct.**
   When nobody can author a backdrop, the F25-consistent move is to hand
   forward the props verbatim rather than have marvel paint one. Pane
   scrollback and the event slice are verbatim by construction.
   `content_origin` exists so a successor is never confused about which it
   holds.
4. **The open risk F25 does not cover: chained authorship.** Each
   generation authors its backdrop from a context that already contained
   the previous backdrop. The CogCanvas figure compares verbatim against
   summarized once; it says nothing about drift across eight authored
   generations, where each author is faithful and the chain still walks
   away from the ground truth. This is a genuinely new sub-question raised
   by putting F25 and shift mechanics together, and section 7 pre-registers
   a chained arm to test it.

## 6. Dependencies named, and where I stopped

- **M2 per-team bus.** The general marvel-to-agent request channel. Ruled
  to be external NATS supervised by marvel; unbuilt. Named and stopped. No
  transport invented here. Pre-bus, delivery degrades along the adapter
  capability ladder in section 2.
- **`question-marvel-graceful-stop`.** Same drain window, different verb.
  Should be one mechanism with two callers.
- **The readiness event** (finding-018 consequence 3). Already missing; the
  handoff phase adds three more unobservable instants. Ship the events with
  the phase.
- **Harness credentials under an isolated HOME.** finding-018's arm D could
  not run for this reason, and section 3's experiment needs the same
  combination that failed. This is the most likely blocker on the follow-on.
- **`internal/admission`** for a hard token ceiling, rather than a bespoke
  limit.

## 7. Pre-registered success signals for the follow-on probe

Written down now so a later run can fail honestly against them. Each is
stated as a bar, with the reading that follows from missing it.

- **S0 (gate, run first).** Establish whether any adapter's harness exposes
  a hook that fires at a turn boundary and can run a command. If none does,
  the cooperative path has no pre-bus delivery at all, and arm A2 must be
  run by hand-injecting the request, reported as a deviation the way
  finding-018 reported its arm D.
- **S1.** TTR(A0) minus TTR(A2) is at least 3 turns on a private-state
  task. If A0 and A2 land within 1 turn, the authored artifact is
  decoration for this task class and the probe must report that rather
  than re-running until it is not.
- **S2.** A2's end-to-end total (control plane + handoff grace + TTR turns
  x measured turn duration) beats 154 s for a one-role team. **If it does
  not, finding-018's headline inverts once the handoff is included, and
  that inversion is the finding.** This is the pre-registered way for this
  design to lose.
- **S3.** A1 beats A0 by at least 1 turn. Below that bar the involuntary
  fallback is not worth building, and the design should drop to
  cooperative-or-nothing.
- **S4.** Authoring completes within 30 s in at least 80% of trials at
  400k+ occupancy. Missing this bar means the grace default must rise, and
  section 2's cost table says the design gets materially worse when it
  does. This is the number that decides whether the whole approach is
  affordable.
- **S5 (chained, section 5 item 4).** Across a chain of eight shifts on one
  task, generation-8 TTR must not exceed generation-1 TTR by more than 2x.
  If it does, chained authored handoff drifts, and the design needs a
  props-anchored variant where each generation forwards the original
  verbatim anchor alongside its own backdrop.
- **Reporting commitment.** A null or inverted result on any of S1 through
  S5 is reported as the result. No arm is re-run with a changed task after
  seeing its number.

## 8. What I could not establish

- **Whether any harness can be asked at all before the bus.** No hook name
  is asserted here because I did not exercise one. This is S0 and it gates
  the cooperative path.
- **Whether the model reads what the env var points at.** Marvel sets pane
  environment (MEASURED). That the model inside the harness attends to
  `MARVEL_HANDOFF_IN` is a per-adapter prompt question with no evidence
  behind it.
- **The right grace default.** I have no data. Guessing one and shipping it
  would be exactly the mistake finding-016 recorded, mistaking a configured
  value for a law.
- **Replica-indexed binding.** With N replicas per role, whether
  index-to-index binding is meaningful is unresolved, and `nextIndex` does
  not gap-fill, so indices are not stable identities across generations.
- **Whether TTR is the right metric.** It measures speed of recovery, not
  quality of the successor's first decision. A successor that recovers fast
  and decides badly scores well. Quality comparison is what critic exists
  for and it is out of scope here.
- **Whether an artifact can harm.** An authored backdrop carries the
  predecessor's mistaken beliefs forward with the same confidence as its
  correct ones. Unmeasured, and A2-worse-than-A0 is a possible outcome the
  experiment must be allowed to return.
- **Prices.** One model, one machine, one day (finding-018). Section 4 is
  arithmetic over those, and every figure moves with the model.

## 9. Per claim: the alternative the evidence could have excluded

| claim | status | alternative it excludes | could it have come out otherwise |
|---|---|---|---|
| no handoff exists anywhere in the repo | MEASURED, grep re-run | that one exists under another name | yes; a single hit refutes it |
| `shiftDrain` kills with no signal-then-wait | MEASURED, read | that a grace exists somewhere in the path | yes; `instanceTeardownGrace` was checked and bounds marvel's teardown, not the agent's turn |
| marvel already sets pane env and already takes an in-pane callback | MEASURED, `adapter.go:160`, `ctxforward.go:206` | that a handoff needs new plumbing in both directions | yes; it needs new plumbing in one direction only |
| marvel does not own pane cwd | MEASURED, no `-c` in the driver | that the workspace directory is a viable artifact location | yes; one `-c` would have made cwd marvel's to choose |
| the temp-dir location is wrong for a handoff | MEASURED (the code comments the failure it caused for projection) | that reusing `ProjectionDir` is fine | yes; the comment records the adoption bug that argues against it |
| a handoff phase costs 1 tick plus authoring time | INFERRED from the measured 4-ticks-per-role structure | nothing | no; arithmetic over finding-018's structure |
| a 60 s grace on three roles loses to compaction | INFERRED | nothing | no; arithmetic, and stated because it is the design's liability |
| the write costs about one turn at the trigger point | INFERRED from measured prices | that the handoff is expensive relative to a shift | yes; a 10x price ratio the other way would have made it dominant |
| marvel-side summarization is ruled out | INFERRED from F25 and its cited measurement | that marvel could produce the artifact alone, with no bus and no cooperation | yes; and this is the cheapest option the discipline forbids |
| the cooperative path serves the context-pressure trigger | HYPOTHESIS | that no trigger is served by a cooperative mechanism | yes; if a full agent cannot author, it fails |
| an involuntary artifact beats nothing | HYPOTHESIS | nothing yet | yes; S3 is the pre-registered test |
| an artifact reduces TTR | HYPOTHESIS | nothing yet | yes; S1 is the pre-registered test, and A2-worse-than-A0 is permitted to be the answer |

## 10. Smallest first slice, if this is built

Ordered so each step is independently useful and none needs the bus:

1. Envelope type, location under `paths.Layout`, generation binding, the
   two env vars. Nothing writes an artifact yet; a hand-placed file proves
   the read path.
2. `marvel handoff write` plus the daemon method. Agent-to-marvel only, the
   `ctx-forward` shape. Now an agent that is told to can write one.
3. `ShiftHandoff` phase with `Handoff = off` as the default, the three
   events, and the abort-path handling. Zero behavior change for existing
   manifests; the phase is observable before it is load-bearing.
4. The involuntary path: capture scrollback and the event slice at request
   time, before anything is killed. This is the step that works for every
   adapter and needs no harness cooperation, which makes it the one worth
   building before the cooperative path is deliverable.
5. Per-adapter courier, gated on S0.

Steps 1 through 4 are all buildable today. Step 5 is where the bus
dependency binds.
