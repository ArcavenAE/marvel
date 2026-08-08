# Bound context by construction instead of measuring it

**Status: idea. Pre-hypothesis. Nothing here has been built or tested, and
the central claim contradicts how two live tickets are currently sequenced.**

Raised during a design review of the CTX% acquisition channels, 2026-08-08.
Recorded because it argues that a quarter of measurement work may be
optional, and an argument of that shape deserves to be tested rather than
absorbed or ignored.

## The provocation

Before cataloguing one more channel, ask what the consumer needs. The
consumer is `aae-orc-hpeu`, automatic shift triggers. Does it need a
percentage, or does it need the answer to "can this session still afford a
handoff?"

Those are not the same measurement. One is a ratio that must be extracted
from a stranger. The other is a remainder, computable the moment you know
where the wall is, and the accountant already carries `ContextLimit` and
`ContextTokens` as a pair **before it divides**.

## Four claims, each testable

### 1. The consumer wants a remainder, not a ratio

A shift costs a drain, a launch, and a handoff artifact. The real predicate
is `remaining tokens > cost of a clean handoff`. If that holds, the trigger
reads an absolute remainder plus a per-role handoff-cost estimate, and CTX%
stays an operator display quantity that got mistaken for the actuator's
input.

*Confirms:* measure the token cost of one real handoff. A stable
few-thousand-token constant means the predicate is well posed.
*Kills:* handoff cost varies wildly with the work.

### 2. Precision was never the constraint, because of an asymmetry

For a binary actuator with an operator-set conservative threshold,
precision beyond roughly which-decile buys nothing. You cannot shift more
usefully at 94.7 percent than at 90.

The asymmetry is the interesting half, and it is not the one usually
assumed:

- **Firing early** costs KV-cache locality (a resource-matrix row, real
  dollars), a handoff, and an interruption mid-task. Recoverable, priced in
  tokens, and **visible**.
- **Firing late** does not crash. The harness auto-compacts and you get an
  uncontrolled summarization where you wanted an authored handoff. That is
  F25's own thesis at session scale, and the cost is **silent knowledge
  loss**.

Late is worse than early not because it fails harder but because it fails
invisibly. Which argues for firing early and eating the cache cost, which
argues in turn that precision was never the binding constraint.

### 3. Therefore the minimum viable signal may be an EVENT, not a level

"This session just compacted" is a defensible moment to shift, because the
harness has just told you it ran out and did the lossy thing on its own.
Detecting that is far cheaper than measuring occupancy: a downward step in
any token series, a transcript compaction marker, an epoch row. **Shift on
first compaction needs no denominator at all** and would work on every
runtime including ones with no channel.

Evidence that the signal is real and legible across harnesses, from the
phase 1 sweep: codex's occupancy level drops to zero at compaction
(observed three times in one session); Crush writes `prompt_tokens = 0` on
sessions carrying a summary message; gemini emits a `PreCompress` hook and
a `chat_compression` metric with before and after token counts; opencode
has a `session_context_epoch` table and compaction events in its protocol.

*Note against it:* the crude proxies (wall clock, turn count) are monotone
and never reset, while occupancy resets at every compaction, so crude
proxies over-fire exactly on the long-lived sessions the trigger exists
for. That is an argument FOR the event and against the cheap proxies, and
it should not be read as support for the budget in claim 4.

### 4. The stronger version: bound it, do not measure it

`Role.MaxTurns` or `Role.MaxAge` forcing a shift works on every harness
today, has no version fragility, no schema pinning, no consent question,
and no per-runtime probe program. marvel already owns every piece:
manifest-declared budgets with admission refusal, a shift state machine
with generations, timeout, and rollback.

The strongest argument for it is one nobody made until late: a handoff path
exercised every N turns is a path that **works**, while one that first
fires at 90 percent context will be debuted during the worst session of the
quarter.

**What it honestly loses**, stated by its own proposer:

- Adaptivity. A cheap session rotates as often as an expensive one.
- Cache locality, systematically rather than occasionally.
- It is a rotation policy, not a health signal. It cannot catch the runaway
  session ballooning context fast, which is the memory-pressure trigger the
  shift-triggers node names.
- It gives the operator nothing. CTX% has a second consumer: the human
  reading `marvel get sessions`.

## The consequence for sequencing, which is the actionable part

If claims 2 and 3 hold, **`hpeu` was never blocked on `dc1j`.** Automatic
shift triggers could ship on a turn or age budget plus compaction detection,
on every runtime, without resolving a single remaining channel question,
and `dc1j` proceeds on its own merits at its own pace.

That is a claim about two live tickets and it has not been tested. It is
recorded on `aae-orc-hpeu` as a claim to test rather than a ruling.

## The honest resolution offered

Not either-or: bound the trigger by construction **and** continue the
measurement program, but for the operator column and for calibrating the
budgets. Two workstreams with different urgency and different acceptance
bars, and treating them as one is what made this feel blocked.

## What would settle it, none of which has been done

- Measure the token cost of one real handoff.
- Capture one long session crossing auto-compaction with the occupancy
  series and compaction metadata. This is already the top named follow-up
  in finding-007 and is still unrun, which is itself worth noticing.
- Run a fixed budget and log occupancy at shift time. If it clusters low,
  the budget is burning window and cache for nothing.
- Test the trigger's behavior when CTX% is absent. Today that branch is
  untested, and an actuator reading unresolved as zero percent would admit
  everything.

## A related inversion, filed here rather than separately

If the auto-compaction threshold and effective window are operator-settable
environment variables, and marvel constructs the process environment at
spawn (enforcement locus 1, shipped), then marvel has been trying to
measure a denominator it is entitled to **assign**. `Runtime.context_window`
is that move already made once, timidly. If it holds, the resolution ladder
gains a rung above every harness report.

*Confirms:* set the override, drive past it, observe compaction where you
said it would happen. *Kills:* the harness ignores the variable, which
would prove cooperation was never optional.

## Provenance

Party-mode design review, 2026-08-08, problem-solving lane, with the
cache-locality and operator-display objections from the architecture and
maintenance lanes. Empirical inputs in
`probe-interactive-ctx-remainder-sweep.md` phase 1.

Related: `marvel-agentic-resource-matrix.md`,
`ctx-channel-consent-and-fidelity.md`, `permission-through-environment.md`,
`question-shift-triggers`, `question-interactive-context-pressure`.
