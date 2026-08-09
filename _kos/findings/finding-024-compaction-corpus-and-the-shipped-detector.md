# finding-024: the shipped compaction detector survives 63 labelled events, and three other things in the same corpus report LOW pressure at HIGH pressure

- **Date:** 2026-08-09
- **Status:** measured. SP1 and SP4 answered; SP5 answered for gemini and
  opencode; two claims in the probe brief refuted.
- **Probe:** `_kos/probes/probe-compaction-ground-truth-mining.md`, SP1,
  SP4, SP5. SP2 and SP3 were answered by finding-016 and are not
  recomputed.
- **Medium:** mining local artifacts on kinu. Zero model calls, no harness
  started, no daemon.
- **Question:** `question-interactive-context-pressure`; bears on
  `question-shift-triggers`.
- **bd:** aae-orc-0tnf. Adjacent: aae-orc-sj34, aae-orc-hpeu, aae-orc-mob4.
- **Scripts:** `scripts/mine_claude_compactions.py` (SP1, SP4),
  `scripts/mine_other_harness_compactions.py` (SP5). Fixture:
  `internal/usage/testdata/compaction_series_claudecode.json`.

## Summary

The measurement SP4 exists for came out in the detector's favor, and it is
the smallest result here. Over 63 labelled Claude Code compactions with an
occupancy sample on each side, `internal/usage/accountant.go`'s reasoned
hysteresis fired on **63 of 63** with **zero false negatives**, and the
tightest event cleared the guard by **4.03x**. The comment at
accountant.go:14-31 says the bound was "chosen to be un-fireable by
ordinary variation rather than calibrated"; against this corpus that
choice is now measured rather than reasoned, and the n-of-1 geometry it
cites can be retired.

The corpus then produced three separate ways for marvel to report LOW
pressure while a session is at HIGH pressure, which is the direction that
silently disables shift rotation:

1. **19 of 82 boundaries produce no post-boundary sample at all** in
   marvel's series, so the detector cannot fire on them. Two have no usage
   record after them; the other 17 have records marvel's own guards route
   away.
2. **The primary-model latch freezes CTX% permanently.** 19 sessions
   switch model and never switch back. `fold` latches the first model it
   sees and never re-latches, so 3,563 later samples go to spend, the sink
   is never written again, and the stored level stays at its pre-switch
   value. Worst case froze at 79,246 tokens while the session went on to
   reach 751,169.
3. **Zero-occupancy records exist on Claude too.** 68 non-sidechain
   assistant records carry all three prompt classes at zero. 57 are kept
   out of the series by the non-primary-model guard, which was written for
   a different purpose; the exclusion is incidental, not designed.

Two claims in the probe brief are refuted. Subagent context IS minable
(1,186 subagent transcript files exist, and two of the 82 boundaries are
inside them), and transcript file order is NOT chronological.

## Method, and the counting the brief asked for

Snapshot taken 2026-08-09T06:12:36Z, `~/.claude/projects`, walked
recursively. Records are selected by PARSING each line and testing
`type == "assistant"` with a `message.usage` object, or
`subtype == "compact_boundary"`. Never by matching a raw string: the brief
already records why, and this document is itself now in the corpus.

```
1,609  transcript files          423 session files, 1,186 subagent files
   82  compact_boundary records  73 auto, 9 manual; 82 of 82 carry compactMetadata
77,711 non-sidechain usage lines
       -> 42,244 dropped by the shipped consecutive-message.id dedupe
       ->  4,884 routed away as non-primary model
       -> 30,583 samples in the occupancy series
   23  distinct Claude Code versions, 2.1.220 the plurality
```

The corpus is live and grew during the work: three runs an hour apart
gave 1,608 / 1,608 / 1,609 files and 30,547 / 30,563 / 30,583 series
samples, with the boundary count stable at 82 throughout. The script
stats every file before and after reading it and reports any that changed
size (one did, on the final run). Every figure below is from the final
run; re-running will not reproduce them exactly, and that is a property
of the corpus rather than of the method.

Only token counts, timestamps, model names, message ids and compaction
metadata were read. Paths appear in artifacts only as a salted digest.

### Both corpora were tested for cumulation before anything was read as a level

Added after the fact, prompted by the Crush arm finding a third instance
of the same class: `.crush/stats/index.html` carries cumulative sums
across sessions, `codex exec --json`'s `turn.completed` is a running
session total (finding-017), and reading either as a level is the defect
`internal/usage` exists to prevent. Inheriting "this field is a level"
from an earlier finding is exactly the move that produced two of those
three.

**Claude's per-assistant-message `usage` is a level. Excluded by three
orders of magnitude.** In the largest session, 1,654 deduplicated
requests carry per-request occupancies summing to 491,457,832 tokens,
while the largest single value anywhere in the corpus is 967,915, under
a 1M window. A cumulative series cannot sit below the window at request
1,654 and it cannot fall, and this one falls at all 63 boundaries. This
confirms rather than inherits finding-007's addendum, which established
the same thing from the other direction (the terminal `result` line IS
cumulative and the per-message one is not).

**Gemini's per-turn `input` is not SESSION-cumulative. Per-turn
accumulation is not excluded.** 12 of 22 gemini sessions with three or
more token rows contain a decrease in `input`, and a session accumulator
cannot decrease. A per-TURN accumulator that resets at each turn
boundary is not excluded and would be indistinguishable from a level on
any single-request turn, which is what codex's open question is
(finding-017 leaves the same distinction unsettled). The SP5 step count
below is stated under the level reading and is void under the per-turn
one, which is the honest scope for it.

## SP4: would the shipped detector have caught these?

The replay ports `Accountant.fold` exactly: occupancy is
`input + cache_read + cache_creation` per assistant message, deduped
against the immediately preceding `message.id` (which is what
`parser.go` does, comparing against `lastRequestID` rather than a set),
non-primary models routed out, and a compaction booked when
`prev - occ > max(2048, 0.10 * prev)`.

```
82  labelled boundaries
63  with a series sample on both sides   -> 63 detected, 0 false negatives
19  with no series sample after them     -> the detector cannot fire
 2  with no series sample before them

drop at the boundary        min  59,305   p50 368,272   max 459,472
guard at that level         min  12,049   p50  46,540   max  53,452
drop / guard                min    4.03   p50    8.09   max   10.0
```

No event came within 4x of the guard, so the detector is not close to a
miss anywhere in this corpus. The margin is structural rather than lucky:
compaction takes a session from roughly 467k to roughly 25k, and a 10%
fractional guard cannot be near that.

**False positives: 5 in file order, 3 in timestamp order**, across 30,583
samples. They are not one phenomenon:

- **Two are ordering artifacts** (see below) and disappear when the series
  is sorted by each record's own timestamp.
- **Two are real context drops with no compaction record anywhere in the
  file**: 572,515 -> 481,858 and 257,006 -> 206,151, roughly 16% and 20%.
  Both sit immediately after an `away_summary` system record. Whatever
  produces them, "the level fell" and "a compaction happened" are separate
  claims, and marvel's `Compactions` counter currently conflates them.
- **One is a drop to a zero-occupancy record**, covered below.

**The 19 undetectable boundaries are the result that matters more than the
63.** Two of them have no usage record after them at all, which is a
session that ended at the boundary. The other 17 have raw usage records
that marvel's guards route out of the series. In the clearest case a
single session carries eight boundaries: the last sample naming its
latched primary model sits at line 3,110 of 5,283, and the six
compactions after that line produce no series sample at all. Marvel would
have detected two of that session's eight compactions and held a frozen
CTX% for the remaining 41% of the file.

## SP1: does marvel's occupancy formula predict preTokens?

Close, consistently low, and never exact. Comparing the chronologically
newest sample before each boundary against the boundary's own
`preTokens`, over the 56 events where that sample is within ten minutes
of the boundary:

```
n = 56 contiguous events
ratio (marvel occupancy / preTokens)   p50 0.9958   47 of 56 within 1%
|delta|                                p50 1,975   min 194   max 12,494
exact matches                          0 of 80
```

The formula tracks. The residual has an ordinary explanation: `preTokens`
is counted at the moment compaction fires, which is after the last
assistant response plus whatever user turn and tool results triggered it,
so marvel's level is one request stale by construction. It is a LAG, not
a formula error, and it is small: half a percent at the median, under 3%
at the 90th percentile.

**What this cannot decide.** Every contiguous event in the corpus has
`preTokens` between 466,321 and 969,333, because compaction fires near
the effective window and this machine's window is set to 500,000
(finding-016). So a fixed-token lag and a proportional lag predict nearly
identical data here. The correlation between `|delta|` and `preTokens` is
-0.089, which leans toward fixed, but over a 2x range with n=56 that is
not a result. Varying the setting, which finding-016 already names as the
cheap next probe, would separate them.

**Two derived numbers worth carrying:**

- The 24 boundaries whose nearest preceding sample is more than ten
  minutes old scatter from 0.14x to 18.6x. Across a resume gap the level
  marvel holds is not related to the harness's count at all. A shift
  trigger reading a session that has been idle overnight is reading a
  stale number, and nothing in the current path says so.
- **`postTokens` is not the level the next request carries.** The first
  post-compaction sample runs a median **61,658 tokens above** the
  metadata's `postTokens` (min 42,237, max 189,097), because the harness
  re-primes system prompt, tools and memory that the summary figure does
  not count. Seeding a reader's level from a compaction record's post
  figure would understate by that much.

## Three ways to read LOW at HIGH

### Zero-occupancy records, the Claude analogue of the codex sentinel

68 non-sidechain assistant records (plus 11 more in subagent files) carry
`input = cache_read = cache_creation = 0`. 67 name the model
`<synthetic>`; **one names the session's real model** and sits beside a
compaction whose `preTokens` was 467,121.

That single record is the whole point. The `<synthetic>` ones are kept out
of the series by the non-primary-model guard, which exists to route
side-calls to spend and catches these by coincidence. The one carrying
`claude-fable-5` passes every guard marvel has, and in file order it is
the first sample after the boundary, so the detector fires on a drop to
zero and then holds a level of 0 until the next real request. The
detection count is right; the level is wrong, at the moment the session is
under most pressure.

`aae-orc-mob4` already rules that codex's all-zero `token_count` must be
discarded. The Claude case says the discard must key on the token values,
because the model name is not a reliable discriminator: on this harness 67
of 68 announce themselves and one does not.

One hazard checked and NOT found: if a session's first sample were a
`<synthetic>` zero, `fold` would latch `<synthetic>` as primary and route
every real sample away forever. 10 sessions do start that way, and all 10
have a series of exactly one sample, so nothing followed to be routed. The
mechanism is real and the corpus does not contain an instance.

### File order is not chronological

26 usage records across 2 of 423 sessions carry a timestamp earlier than
the record physically before them, the worst by 90,895 seconds (25.2
hours). In one case a block of older records is appended immediately
before a compaction boundary, so the newest line in the file reads 134,037
where the true level was 967,915.

A reader that tails a transcript and takes the last complete record it
finds will occasionally take one of these. Sorting the series by each
record's own timestamp removed 2 of the 5 detector false positives and
changed no true positive. The rate is low (26 in 77,711) and the
consequence is not: it is a 7x understatement at the single moment the
number is load-bearing.

### The primary-model latch, and CTX% that stops moving

`fold` latches the first model identity it sees and never re-latches.
Every later sample naming a different model is routed to spend, and
`res.write` stays false, so the sink is not updated at all.

Measured over sessions where the model changes and never changes back
(interleaved side-calls excluded, trailing `<synthetic>` records
excluded):

```
19  sessions with a permanent model switch
3,563  samples routed away after the switch

frozen level   true peak after   understatement
     79,246           751,169             9.5x
    120,496           462,243             3.8x
    231,168           756,882             3.3x
    166,735           457,001             2.7x
    254,671           465,104             1.8x
```

The store keeps rendering the frozen figure as a current reading. There is
no staleness marker on `api.SessionContext` and no counter that fires, so
this is invisible to an operator and to any admission gate reading the
same field.

finding-016 already ruled that a model change must invalidate the reading
rather than average across it. This is the measured cost of not having
done it yet, and it is larger than the denominator error finding-016 was
arguing about: freezing at 79,246 while the session runs to 751,169 is
wrong by more than the 2x that motivated `aae-orc-sj34`.

## Cluster A: a candidate explanation, offered as a candidate

finding-016 recorded five auto-compactions at or below 210k as
"unexplained by the setting", two of them with `postTokens` exceeding
`preTokens`. All five sit in one session, and four of the five carry a
`scheduled_task_fire` system record within 30 lines before the boundary,
one of those also with an `away_summary`. Both of the post-exceeds-pre
events are in that set, and each is followed immediately by a
`<synthetic>` zero record.

The base rate is what stops this being a conclusion: 15 of 68 high-`pre`
auto compactions also carry such a marker. 4 of 5 against 15 of 68 on n=5
is suggestive and nothing more. The fifth cluster-A event (`pre` 168,174)
carries no marker and is one of the two boundaries inside a SUBAGENT file,
which fits finding-016's own guess that it is a 200k window compacting
near its top.

## SP5: the other harnesses

**gemini: an occupancy series exists, no labelled compaction does.** 26
chat files, 477 messages carrying `{input, output, cached, thoughts, tool,
total}` plus a model name. No field in any message mentions compaction or
summarization. Marvel's step detector would fire 3 times across those 477
samples (two of them drops to exactly zero, which is the same hazard as
above), and there is no label anywhere in the corpus against which to
score those firings. So gemini has no compaction ground truth on this
machine, and acquiring one needs a session run, not a mine. That step
count assumes `input` is a level; see the cumulation check in the method
section, which excludes session-cumulative and leaves per-turn
accumulation open.

One discriminating result came free, in the shape finding-017 used. Over
the 418 rows with nonzero `cached`, `total == input + output + thoughts +
tool` holds on 418 of 418 and the cache-inclusive variant holds on 0 of
418. So gemini's `total` EXCLUDES `cached`, which is the opposite of
opencode. Whether `input` SUBSUMES `cached` is NOT decided: no row has
`cached` above `input`, but the largest `input` observed is 51,209 against
a window in the millions, so the window-bound discriminator that settled
codex has no force here and both readings fit every row.

**opencode: the table exists and is empty.** `session_context_epoch` is
present in the local store with 0 rows against 211 sessions, confirming
the brief. Its schema is worth recording: `session_id` is the PRIMARY KEY,
so even when populated it holds one row per session. It is a pointer to
the current epoch, not a history, and it can never supply a compaction
corpus of past events.

codex is covered by finding-017 and Crush by the k2mi arm; neither was
touched here.

## Two claims in the brief, refuted

**"No `isSidechain: true` assistant line exists in the local corpus, so
subagent context is not minable this way."** There are 1,186 subagent
transcript files, at `<project>/<session>/subagents/agent-*.jsonl` and a
workflow-scoped variant one level deeper. A 150-file sample carried 4,376
sidechain usage lines. Path and `isSidechain` agree on every one of the
77,711 lines examined, with zero disagreements. Two of the 82 compaction
boundaries are inside subagent files: subagents compact, in windows marvel
is not watching, and the artifacts are on disk.

**"1,535 files ... 77 compact_boundary records."** 1,609 and 82 at this
snapshot. The brief's own instruction (snapshot before mining, inherit no
figure including the brief's) is the right one and is why this is a
correction of a number rather than of a method.

## What was not established

- **Whether the lag in SP1 is a fixed token count or a proportion.** Every
  contiguous event fires in a narrow band near one machine's one
  effective-window setting.
- **Why 17 boundaries have post-boundary usage records that never reach
  the series.** The primary-model latch is the measured cause in the two
  sessions checked directly (5 boundaries). The rest are unattributed;
  dedupe and latch are both candidates and the corpus was not asked to
  separate them.
- **What produces the two unlabelled 16-20% context drops.** Both follow
  an `away_summary` record. That is an association across two events.
- **Whether the timestamp inversions would appear in a live NDJSON
  stream.** They are a property of the transcript file. A stream reader
  might never see the older records at all, or might see them in the same
  place. Nothing here settles it, and a codex or Crush reader should test
  its own channel rather than inherit this.
- **Whether cluster A is caused by scheduled task fires.** 4 of 5 against
  a 15 of 68 base rate.
- **Whether gemini's `input` subsumes `cached`.** The corpus cannot
  discriminate.
- **Whether gemini's `input` accumulates within a TURN.** Session-level
  accumulation is excluded; per-turn is not, and every gemini row here
  could be a single-request turn, on which the two coincide.
- **Anything about a live compaction crossing under marvel.** This is a
  mine of transcripts written by an operator's interactive sessions. The
  accountant consumes a headless NDJSON stream, and the correspondence
  between the two surfaces is assumed at the per-assistant-message level
  rather than verified. It is the same `message.usage` object in both, but
  no capture in this work proves the stream emits exactly the records the
  transcript retains.

## Consequences to carry

1. **The hysteresis constants are validated and the comment should say so.**
   4.03x minimum margin over 63 labelled events replaces one recovered
   geometry.
2. **Discard zero-occupancy samples explicitly**, on the token values, in
   the fold rather than incidentally in the model guard. The one record
   that names its session's real model is the case that already defeats
   every existing guard.
3. **Re-latch on model change, or mark the reading stale.** Freezing is
   worse than the denominator error `aae-orc-sj34` was filed for, and it
   is silent.
4. **A reading needs an age.** Both the resume-gap scatter and the frozen
   latch are invisible because `api.SessionContext` carries no observation
   time an operator or a gate can test.
5. **"Compaction detected" is not "compaction happened."** Two events here
   were real drops with no compaction, and one detection fired on a zero
   record. Any shift trigger keyed on the compaction COUNT inherits all
   three.
6. **Test every aggregate for cumulation before reading it as a level,
   including one an earlier finding already blessed.** Three instances
   across three harnesses now: codex's `turn.completed`, Crush's
   `.crush/stats/index.html`, and the Claude terminal `result` line.
   The check is usually one query, and the failure is silent and
   understating.
