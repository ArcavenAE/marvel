# Probe brief: rework as the degradation proxy for the reliability knee

**Status:** OPEN (brief only; designed, not run).
**Question:** `question-interactive-context-pressure`, `question-shift-triggers`.
**Medium:** mining the local Claude Code transcript corpus. No model calls, no
harness sessions, no daemon.
**bd:** aae-orc-tzzn.
**Prior work it follows:** `probe-context-operating-points-and-axes.md` SP1
(attempted, inconclusive), `probe-compaction-ground-truth-mining.md` (method and
the compaction corpus), finding-016.

## Why this probe exists

SP1 tried to locate the reliability knee (operator observation: degradation above
roughly 600k occupancy for fable and opus) by pairing per-turn occupancy against
`tool_result.is_error` across the local corpus. It came back flat: 2.7 to 5.3
percent across every band, no trend, and the two highest bands showing the lowest
rates.

SP1 named four reasons it could not answer. Three are structural and this probe
must confront all three:

1. **The proxy measures the wrong thing.** A tool error is mostly a command that
   legitimately failed. That noise floor is large and roughly constant and would
   swamp a change in reasoning quality.
2. **Sample size collapses exactly where the question lives.**
3. **Survivorship.** A session only reaches high occupancy by not having gone
   badly wrong. Sessions that degrade get compacted, abandoned, or taken over,
   which removes them from the high-occupancy sample.

Rework is the proposed replacement proxy. **Rework is what degradation looks like
when the model is still succeeding mechanically**: it reverts its own edit,
re-reads a file it already read, re-runs a command it already ran, rewrites a file
it just wrote. Every one of those is a successful tool call. None of them shows up
in `is_error`.

That is the affirmative case. The rest of this brief is mostly about whether it
survives contact with the same three problems, and my assessment is that it
survives the first cleanly, survives the third partially, and does NOT survive the
second at the occupancy the question is about. That last point changes the design,
so it is stated up front rather than buried in limitations.

## Corpus census

**MEASURED** on kinu, 2026-08-08, by parsing every line of
`~/.claude/projects/**/*.jsonl` and filtering on record fields. No string
matching was used for any count in this document.

```
 1,545  transcript files across 109 project directories
252,124  lines parsed, 0 unparseable
   415  distinct sessionIds
111,614  assistant records (111,660 carrying message.usage)
 55,207  tool_use blocks
 55,316  tool_result blocks
```

Tool distribution across the whole corpus:

```
Bash 37,282   Read 6,099   Edit 5,144   Write 1,984
Agent 754     Grep 43      Glob 11
```

`tool_result.is_error` across all tool_result blocks: `True` 1,706, `False`
36,045, **key absent 17,757**. Absence is a third state, not a false, and a
detector that reads absence as success is making an assumption it should declare.

**The corpus is live and it grew while I measured it.** Two full passes minutes
apart returned 111,614 and 111,660 assistant records. That is the same
self-contamination property `probe-compaction-ground-truth-mining.md` recorded,
and this document is itself being written into one of the 109 directories. Every
count above is a snapshot; the probe must snapshot before mining and state the
snapshot time beside every number.

### Two corrections to prior briefs, both measured

- **`isSidechain: true` assistant records DO exist.** The compaction-mining brief
  states "No `isSidechain: true` assistant line exists in the local corpus, so
  subagent context is not minable this way." Measured now: 36,510 sidechain
  assistant records, 22,437 sidechain user records, 2,612 attachments. That is
  roughly a third of all assistant turns. Whatever was true when that line was
  written, it is not true of the corpus today, and it is load-bearing for this
  probe (see the partitioning rule below).
- **The compaction corpus is 79 records, not 77**, by the same `subtype ==
  "compact_boundary"` filter. Consistent with growth, not with a method
  disagreement.

## The signals, and how each is detected from real fields

Field availability was verified by parsing, not assumed. Tool inputs are recorded
verbatim in the transcript, which is what makes this probe possible at all:

```
Read    file_path (6,095)  limit (1,014)  offset (893)
Edit    file_path (5,144)  old_string (5,144)  new_string (5,144)  replace_all (5,144)
Write   file_path (1,987)  content (1,987)
Bash    command (37,300)   description (35,997)  timeout (3,489)
```

Two independent corroborating channels also exist and neither was used by SP1:

- **`toolUseResult`** (dict on 45,449 records) carries `filePath` (6,223),
  `structuredPatch` (6,220), `originalFile` (6,220), `userModified` (6,220), and
  `oldString`/`newString`/`replaceAll` (4,417). `userModified` is the important
  one: it flags edits where the operator changed the file, which are not agent
  rework and must be excluded.
- **`file-history-snapshot`** (3,882) and **`file-history-delta`** (1,806)
  records, carrying `messageId`, `snapshotMessageId`, `trackingPath`, `backup`,
  and `timestamp`. This is the harness's own file-version tracking and it is a
  candidate direct oracle for "this file changed back to what it was." Its
  semantics are unverified and reading them is the probe's first job.

### R1. Self-revert

An edit that undoes an earlier edit to the same path.

*Detection.* For each `Edit` on a path, record `H(old_string)` and
`H(new_string)` where H is sha256 over the exact bytes. A revert is an edit whose
`H(new_string)` equals the `H(old_string)` of an earlier edit on the same path in
the same session. Also catch the weaker form: an edit whose `H(old_string)`
equals the `H(new_string)` of an edit made within the last k turns, meaning the
agent is immediately re-touching what it just wrote.

*Exclusions.* Drop any pair where the intervening `toolUseResult.userModified` is
true on that path. That is the operator editing, not the agent reverting.

### R2. Redundant re-read

Reading a file that was already read and has not changed since.

*Detection.* For each `Read`, key on `(file_path, offset, limit)` with absent
offset/limit meaning whole-file. A redundant re-read is a second read of an
overlapping range in the same session with **no intervening Write or Edit to that
path and no intervening `file-history-delta` on that `trackingPath`**. The
intervening-change qualifier is the whole detector; without it this measures
normal behavior.

*Known gap.* Reads also happen through Bash (`cat`, `sed -n`, `head`). With Bash
at 37,282 calls against Read at 6,099 in this corpus, a path-only Read detector
sees a minority of file reads. Either parse file paths out of Bash commands
(fragile, and it means inspecting command text) or declare the coverage limit.
I recommend declaring it.

### R3. Repeated invocation

The same tool called with byte-identical arguments twice in one session.

*Detection.* Canonicalize the `input` dict (sorted keys, stable separators),
hash it, and count repeats per session. Report separately per tool name, because
the base rates differ by an order of magnitude and pooling them would be
meaningless.

*This is the highest-volume signal by a wide margin*, and per the power analysis
below it is the only one with any sample where the question lives.

*Base-rate caution.* A repeated `git status`, `just test`, or `ls` is normal
polling, not rework. The probe must either exclude a pre-registered list of
idempotent inspection commands or, better, report the repeat rate per command
hash so that high-frequency benign repeats are visible as their own population
rather than silently inflating the numerator.

### R4. Rewrite churn

Writing a file that was already written in the same session, or a burst of edits
to one path in a short window.

*Detection.* Count `Write` calls per `(session, file_path)`; anything above one is
a rewrite. For edit churn, count `Edit` calls per `(session, file_path)` inside a
sliding window of k turns and compare against the session's own distribution.

### R5. Abandonment and censoring events

Not rework, but the thing rework leads to, and the reason it is here is stated
under survivorship below.

*Detection.* A session file whose last assistant turn is at high occupancy and
which carries no terminal `result` record; a `compact_boundary` immediately
following a rework burst; a large gap in `timestamp` followed by a session
ending. These are countable, and counting them converts survivorship from an
unmeasured bias into an observed quantity.

## Method rules the probe inherits and one it adds

Inherited from `probe-compaction-ground-truth-mining.md` and finding-016:

1. **Parse, never grep.** Filter on record fields. A previous count in this arc
   was wrong because a raw string match caught this review's own prose about
   compaction, written into the directory being mined.
2. **Snapshot before mining.** The corpus grows during the work; see the census.
3. **Read counts, tokens, timestamps, and metadata only, never message content.**

Added here, because this probe needs to compare strings it must not read:

4. **Hash, do not read.** `old_string`, `new_string`, `content`, and `command`
   are compared only as sha256 digests. The extractor never stores, prints, or
   branches on plaintext, and no artifact it produces contains any. File paths
   are metadata and may be used for joining, but any published table carries
   counts only, never paths.

Rule 4 is what makes R1 and R3 possible without violating rule 3, and it should
be stated as a technique the graph keeps: equality of content is derivable
without access to content.

## Partitioning: three cuts that must happen before any rate is computed

**Sidechain.** Subagent turns run in their own context windows. Their occupancy
is a different series and mixing it into a main-thread occupancy band is a
category error. Partition on `isSidechain` and report main-thread results as
primary. **MEASURED and consequential:** above 500k occupancy the corpus contains
zero sidechain assistant turns, so the region the question is about is entirely
main-thread and this cut costs nothing where it matters.

**Ordering.** Use the `uuid`/`parentUuid` chain, not file line order. Sidechains
interleave into the same file and line order does not reflect causal order.

**Occupancy attribution.** Attribute each tool call to the occupancy of the
assistant turn that issued it, computed with marvel's own additive rule
(`input_tokens + cache_read_input_tokens + cache_creation_input_tokens`), the
same convention SP1 used, so the two results are comparable.

## The power problem, measured, and why it changes the design

This is the part that should be read before anything is built. Main-thread
assistant turns and tool calls by occupancy band, **MEASURED**:

```
        band   main turns   all tool_use   file-touching tool_use
     100,000       10,174          4,482                      757
     200,000       10,089          4,791                    1,030
     300,000        8,445          3,988                      875
     400,000        5,745          2,610                      551
     500,000        1,285            531                       94
     600,000          680            282                       42
     650,000          589            227                       46
     700,000          423            160                       26
     750,000          145             51                       17
     800,000+         234             85                       11
```

At and above 600k, where the reported knee lives, the entire corpus holds
**2,071 main-thread turns, 805 tool calls, and 142 file-touching tool calls,
spread across 12 session files.**

Rework detectors R1, R2, and R4 all need PAIRS of file-touching calls on the same
path in the same session. With 142 such calls above 600k across 12 files, the
number of qualifying pairs is small enough that no band-wise rate will have a
usable confidence interval. **Rework does not escape SP1's problem 2.** It
inherits it, and for the file-based signals it inherits it worse, because pairing
squeezes the sample further.

Three consequences, and they are the design:

- **R3 (repeated invocation) is the only signal with sample above the knee**,
  because Bash accounts for roughly 663 of the 805 calls in those bands. Any
  band-wise analysis must be R3-primary.
- **A band table is the wrong primary analysis.** Repeating SP1's shape will
  repeat SP1's outcome, and the honest prediction is another flat result driven by
  the top bands having no power.
- **The primary analysis should be the compaction natural experiment below**,
  which pools across the whole occupancy range instead of relying on the thin
  tail.

## The confound SP1 did not name, and the design that answers it

**Occupancy and session age are nearly collinear.** Within a session, occupancy
rises monotonically with turn index until a compaction. So any quantity that
rises later in a session correlates with occupancy for reasons that have nothing
to do with the model.

Rework is exactly such a quantity, and plausibly so on innocent grounds: the hard
part of a task tends to come later, and iterating on a nearly-finished artifact
looks like churn. A regression of rework on occupancy cannot separate "the model
is degrading" from "the work got harder." **This confound would make a positive
result uninterpretable, which is worse than SP1's negative one.**

The answer is already on disk. At a `compact_boundary`, occupancy drops
discontinuously while session age, turn index, and task difficulty continue
rising. The specimen in the mining brief goes from 467,021 to 13,774 tokens in
one record. That is a within-session intervention on the independent variable
with the confounds held fixed.

**The compaction natural experiment.** For each of the 79 boundaries, compute the
rework rate over a window of w turns before and w turns after, on the same
session, same task, same operator, same model. Then:

- If rework is driven by **occupancy**, it falls after the boundary.
- If rework is driven by **age or task difficulty**, it does not fall, and may
  rise.
- If rework RISES after compaction, that is a third and separately interesting
  result: the summarization itself degraded the agent, which bears directly on
  `bound-context-instead-of-measuring-it.md` and on the shift-versus-compact
  argument.

Each session is its own control, which is what neither SP1 nor a band table has.

*Two threats to it, stated so they are not discovered later.* Compaction is
triggered BY high occupancy, so the pre-window is systematically high-occupancy
and the post-window systematically low; that is the point, but it means the
comparison is confounded with anything else that changes at a boundary (the
system prompt changes, the tool result history is gone). And the post-window
agent may re-read files it can no longer see, which is R2 firing for a mechanical
reason rather than a degradation reason. **That last one is severe enough to
require pre-registering R2 as excluded from the post-compaction window**, leaving
R1, R3, and R4 for the natural experiment.

## Survivorship: does this proxy escape it?

**Partially, and less than one would want. It does not escape it.**

Where rework is genuinely better than tool errors:

- A session heading for abandonment produces rework on the way there, and those
  turns are in the corpus up to the abandonment point. Rework is a *leading*
  indicator recorded before the censoring event, where a fatal error is the
  censoring event. So the signal has a better chance of appearing in the
  surviving record.
- The bias direction is favorable. Sessions that would have shown the most rework
  at high occupancy were truncated before producing their worst turns, so
  measured high-occupancy rework is **understated**. An effect that appears
  despite a bias pointing against it is more credible, not less.

Where it does not escape:

- The 12 files above 600k are still selected for having gone well enough to get
  there. Conditioning on the outcome is not repaired by changing the outcome
  measure.
- Asymmetric censoring is worse than symmetric noise. If degrading sessions are
  removed preferentially at high occupancy, the high bands contain a cleaner
  population than the low bands, which can produce a *downward* trend in rework
  with occupancy from selection alone. **A negative result is therefore not
  evidence against the knee**, and this must be stated in the result rather than
  discovered in review. Note that SP1's two highest bands did show the lowest
  error rates, which is the signature this mechanism would produce.

What partially repairs it:

- The compaction natural experiment is within-session, so the survivorship
  selection applies equally to both windows and largely differences out.
- R5 makes censoring observable. Counting abandonment events by the occupancy at
  which they occur gives a hazard-shaped view, and a rising abandonment rate with
  occupancy would be corroborating evidence for the knee even if the rework rate
  itself stayed flat. **This is the cheapest thing in the brief and it should be
  computed first**, because it is the one measurement whose interpretation
  improves rather than degrades under survivorship.

## Success signal

The probe has answered if it produces either of these, with the analysis
pre-registered before the extractor runs:

**Primary (compaction natural experiment).** A stated change in rework rate
across the 79 boundaries, per signal (R1, R3, R4), with a paired test across
sessions and a confidence interval. Success is a difference whose interval
excludes zero in either direction. A measured drop supports occupancy-driven
degradation; a measured null rules out the leading explanation for the reported
knee at the occupancies the corpus actually reaches; a measured rise redirects
the arc toward compaction quality.

**Secondary (R3 band-wise).** The repeated-invocation rate per occupancy band,
main-thread only, per tool name, with intervals. Success is a monotone trend
whose interval excludes flat somewhere in the range. This is the signal with
enough sample above 600k to be worth attempting, and it is secondary because it
carries the age confound.

**Supporting (R5).** Abandonment and censoring events by occupancy band. Success
is a stated hazard by band, with the count of sessions at risk in each.

The probe also succeeds, in a smaller way, if it establishes that
`file-history-snapshot` and `file-history-delta` are a usable revert oracle. That
would be a reusable instrument for any later question about agent self-correction
and is worth the read regardless of the knee.

## Failure signal

Any of these means stop, write the negative, and do not iterate on the proxy:

- **The natural experiment is flat within intervals**, AND the R5 hazard is flat.
  Two independent views, both null, at the occupancies this corpus reaches. That
  does not falsify the knee (survivorship, above) but it does exhaust what
  observational mining of this corpus can say, and the next step becomes the
  controlled experiment SP1 already named.
- **The base rate swamps the signal.** If benign repeats (polling commands,
  post-edit re-reads) dominate the rework counts to the point that the
  qualification rules are doing all the work, the proxy has the same defect as
  `is_error`: a large, roughly constant noise floor. The tell is that the result
  moves substantially when a qualification threshold moves. Pre-register the
  thresholds and report a sensitivity check; if the result is threshold-dependent,
  it is not a result.
- **Rework rises after compaction across the board.** This would mean the
  detector is reading mechanical re-establishment of context rather than
  degradation, and R2's exclusion did not go far enough.
- **Fewer than roughly 20 boundaries survive the window requirements.** Some of
  the 79 will sit too close to a session start or end to have w turns on both
  sides. If the usable set is very small, the primary analysis has no power and
  the probe should say so rather than report a fragile number.

## Excluded scope

- **Building anything.** No detector ships from this probe. It produces
  measurements and, if `file-history-delta` proves usable, a fixture.
- **Spending quota.** No model calls, no sessions run.
- **Content reading.** Rule 4 above. If a signal cannot be built from hashes,
  counts, timestamps, and paths, it is out of scope, and the correction-turn
  detector SP1 floated is out for exactly this reason.
- **The other harnesses.** codex (~209 session files) and gemini (26 files, 477
  messages with a per-turn token series) may carry comparable structure. Worth a
  pass after the claude corpus, not before.
- **Locating the knee.** This probe asks whether a degradation signal moves with
  occupancy at all. Locating a threshold is downstream of establishing one exists.

## Handling note

The corpus is the operator's own working history across 109 project directories,
including private repositories. This probe reads tool names, argument hashes,
file paths, timestamps, token counts, and record metadata. It has no reason to
read message content and must not. Any artifact it produces carries counts and
derived numbers, never transcript text and never file paths.

## What would change the read

If the natural experiment shows a clear drop in rework after compaction, the
reliability knee stops being an operator report and becomes a measured
occupancy-driven effect, and the dominated-region argument in
`context-pressure-is-an-operating-point-not-a-fill-level.md` gains the evidence
it currently lacks. SP1 recorded, against the case it was making, that the argument
rests entirely on operator observation and should not be built into a default
until a better signal exists. That standard applies to this probe's output too:
a positive result here supports a default, and a null result leaves the existing
recommendation exactly where SP1 left it.

The result that would most change the arc is the third one: rework RISING after
compaction. That would say the harness's own summarization costs more capability
than the occupancy it relieves, which is an argument for shifting rather than
compacting, and it is measurable from data already on disk.
