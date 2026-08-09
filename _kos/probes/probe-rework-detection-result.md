# Probe result: rework as the degradation proxy for the reliability knee

**Status:** RUN 2026-08-08. Brief: `brief-rework-detection-reliability-knee.md`.
**Question:** `question-interactive-context-pressure`, `question-shift-triggers`.
**bd:** aae-orc-tzzn.
**Prior work:** `probe-context-operating-points-and-axes.md` SP1 (attempted,
inconclusive), `probe-compaction-ground-truth-mining.md`, finding-016.

**This is a probe result, not a finding.** The brief's pre-stated success
signals were not met, and one of its own pre-stated failure signals fired.
Written under the brief's rule that a negative is a real result.

---

## Verdict

**The knee is UNDECIDABLE from this corpus, and rework does not rescue it.**

Not "invisible", which would claim the measurement had the power to see an
effect and saw none. It did not have that power, and I can now say exactly
where the power went. Three of the reasons are new; the brief anticipated
only the first.

1. **MEASURED.** Rework rate is flat across occupancy bands, in the same
   shape SP1's error rate was flat. Pooled repeated-invocation runs 0.23% to
   1.59% below 600k and 1.24% (95% CI 0.68% to 2.27%) above it, an interval
   that contains the below-600k rate of 1.13%.
2. **MEASURED, new.** The compaction natural experiment, which the brief
   designated as the PRIMARY analysis precisely because it holds session age
   fixed, cannot reach the region the question is about. 67 of 78 boundaries
   fire with `preTokens` between 400k and 500k, median 467,446. Zero fire
   between 500k and 900k. The intervention sits below the reported knee, so
   even a clean result would not locate a knee at 600k.
3. **MEASURED, new.** Model mix is not constant across occupancy bands.
   `claude-opus-5` contributes 4,223 turns in the 300k band and zero turns
   anywhere above 500k. Above 600k the corpus is `claude-opus-4-8` and
   `claude-fable-5` only. A pooled band table across occupancy is therefore
   part dose-response and part between-model comparison, and within-model the
   direction disagrees: fable-5 repeated-invocation rises across the 300k to
   400k step with non-overlapping intervals, opus-5 falls, opus-4-8 is flat.
4. **MEASURED, new.** The censoring measurement the brief called "the
   cheapest thing in the brief" cannot be computed as specified. Zero of 415
   main-thread transcripts carry a terminal `result` record. All 413 `result`
   records in the corpus live in sidechain-only files. Abandoned and finished
   sessions are indistinguishable on that field.

**INFERRED.** Point 2 is the one that changes the arc. The brief assumed
compaction boundaries would sample the occupancy range. They sample one
narrow slice of it, and that slice is the harness's auto-compact threshold,
not an occupancy the operator chose. Any future within-session design that
uses compaction as its intervention inherits this ceiling.

---

## Method

### Snapshot

**MEASURED**, `~/.claude/projects/**/*.jsonl`, snapshotted 2026-08-08
20:24:06 -0500 as a manifest of path plus byte size. Every later pass reads
only the first snapshotted `size` bytes of each file, so corpus growth during
the work cannot move a count. The corpus is live and it is being written to
by the session that produced this document; the snapshot is the answer to
that, per `probe-compaction-ground-truth-mining.md`.

```
    1,570  transcript files across 110 project directories
828,693,451  bytes
  255,071  lines parsed
        0  parse failures
      415  distinct sessionIds
```

A trailing partial line (a file mid-write at snapshot time) is dropped rather
than counted as a parse failure. **MEASURED:** no file produced one.

### Parse, never grep

Every count in this document comes from `json.loads` on a whole line followed
by a filter on record fields. No raw string matching was used anywhere, for
any purpose, including to locate compaction boundaries. Boundaries are
`type == "system"` and `subtype == "compact_boundary"`.

### Hash, do not read

Rule 4 of the brief. `old_string`, `new_string`, `content`, `command`, and
every other tool input value were reduced to a truncated sha256 digest at
parse time and never stored, printed, or branched on in plaintext. File paths
were hashed and used only as join keys. This document carries counts, rates,
intervals, tool names, model names, and token counts. It carries no
transcript text and no file paths.

Equality of content is derivable without access to content. That technique is
the one durable instrument this probe produced, and it is what made R1, R3,
and R4 possible under a rule that forbids reading any of the fields they
compare.

### Partitioning

**MEASURED.** Sidechain and main thread were separated before any rate was
computed. Of 1,570 files, 411 carry main-thread assistant turns, 1,122 are
sidechain-only (subagent transcripts), and 37 carry no assistant record at
all. 113,096 assistant records exist, of which 37,364 are sidechain.

The unit of session is the FILE, not `sessionId`, because a file is one
contiguous transcript with one occupancy series while `sessionId` collides
across files (415 sessionIds against 1,570 files).

Ordering within a file follows the `uuid`/`parentUuid` chain, depth-first
with children ordered by timestamp, not file line order. 75,706 main-thread
assistant turns entered the analysis; 26 main-thread assistant records were
dropped for carrying no `uuid`, so nothing could link them into the chain.
34,907 tool calls were issued by those turns.

Occupancy is `input_tokens + cache_read_input_tokens +
cache_creation_input_tokens` on the assistant turn that issued the call, the
same additive rule SP1 used, so the two results are comparable.

### Pre-registration

Written after the snapshot and structural census, before the rework extractor
was written or run. Reproduced here so the result stands alone.

- **Signals.** R1 self-revert (an `Edit` whose sha256(new_string) equals the
  sha256(old_string) of an earlier `Edit` on the same path). R1w, the weak
  form, adds the immediate re-touch case. R3 repeated invocation (a call whose
  tool name plus canonical sorted-key input digest already appeared). R4
  rewrite churn (a `Write` to a path already written). R2 redundant re-read,
  excluded from the natural experiment per the brief, computed band-wise only.
  R5 censoring.
- **Exclusion.** A `toolUseResult.userModified` on a path clears that path's
  history, so an operator edit can never be scored as agent rework.
- **Lookback scope.** Window-local is PRIMARY for the natural experiment,
  because session-global lookback is asymmetric across a boundary: the post
  window has strictly more prior history to collide with, which would bias
  post-window rework upward mechanically. Session-global is the sensitivity
  check and is primary for band-wise work, where no paired comparison exists.
- **Grid.** w in {10, 20, 40} turns per side, min_calls in {3, 5, 10}, both
  windows must qualify. Primary cell w=20, min_calls=5.
- **Tests.** Paired difference post minus pre, 10,000-resample bootstrap 95%
  CI, plus an exact two-sided sign test. Wilson 95% intervals band-wise.
- **Base-rate control.** Repeat the band table with the 20 globally most
  repeated (tool, input-digest) pairs excluded, to check that benign polling
  is not carrying the numerator.
- **Failure.** Fewer than 20 usable boundaries, or a result that flips sign or
  significance across the grid.

---

## Result 1: rework rates across the corpus

**MEASURED**, main thread, session-global lookback.

| signal | rework / eligible calls | rate |
|---|---|---|
| R1 self-revert (strict) | 8 / 4,010 Edit calls | 0.20% |
| R1w self-revert (weak, immediate re-touch) | 122 / 4,010 | 3.04% |
| R2 redundant re-read | 29 / 1,574 Read calls | 1.84% |
| R3 repeated invocation | 394 / 34,907 all calls | 1.13% |
| R4 rewrite churn | 107 / 1,368 Write calls | 7.82% |

**MEASURED.** R1 strict fires 8 times in the entire corpus. It is not an
instrument at any occupancy; it is too rare to carry a rate. The brief's
affirmative case rested on rework being what degradation looks like while the
model still succeeds mechanically, and the specific behavior it named first,
the agent undoing its own edit, essentially does not happen here.

## Result 2: band-wise (secondary analysis)

**MEASURED.** Main thread, session-global lookback, 100k bands.

| band | turns | R3 repeat | R1 revert | R4 rewrite churn | R2 re-read |
|---|---|---|---|---|---|
| 0 | 5,723 | 6/2,630 0.23% | 0/131 | 2/43 4.65% | 9/261 3.45% |
| 100k | 21,126 | 152/9,620 1.58% | 6/871 0.69% | 17/352 4.83% | 9/514 1.75% |
| 200k | 19,482 | 96/9,203 1.04% | 0/1,244 | 25/379 6.60% | 6/374 1.60% |
| 300k | 16,147 | 57/7,711 0.74% | 0/1,060 | 40/362 11.05% | 4/262 1.53% |
| 400k | 8,837 | 63/3,974 1.59% | 1/518 0.19% | 19/169 11.24% | 1/99 1.01% |
| 500k | 2,320 | 10/964 1.04% | 1/109 0.92% | 4/27 14.81% | 0/35 |
| 600k | 1,269 | 6/509 1.18% | 0/49 | 0/25 | 0/14 |
| 700k | 568 | 2/211 0.95% | 0/20 | 0/10 | 0/13 |
| 800k | 92 | 2/31 6.45% | 0/2 | 0/1 | 0/0 |
| 900k | 142 | 0/54 0.00% | 0/6 | 0/0 | 0/2 |

Above 600k the whole corpus holds 2,071 main-thread turns, 805 tool calls,
142 file-touching calls, and 12 distinct files. Those four numbers reconcile
exactly with the brief's own power table, which is a useful cross-check that
the two extractors agree.

**R3 does not separate.** 10 / 805 above 600k is 1.24%, Wilson 95% CI 0.68%
to 2.27%. The below-600k rate is 1.13% and sits inside that interval.

**R4 rises below 500k and then has no sample.** 4.83% (CI 3.04 to 7.60) at
100k against 11.05% (CI 8.22 to 14.70) at 300k is a non-overlapping step, so
the rise is real within the low bands. Above 600k it is 0 of 36 calls, Wilson
CI 0% to 9.64%, which CONTAINS the below-600k rate of 8.03%. **The zeros in
the top three bands are not evidence of a lower rate.** They are what a rate
of 8% looks like when you sample it 36 times. Anyone reading that column as a
collapse is reading noise.

**Base-rate control passed.** Excluding the 20 globally most-repeated
(tool, input-digest) pairs moves the pooled R3 band series from
0.23/1.58/1.04/0.74/1.59/1.04/1.18/0.95/6.45/0.00 to
0.19/1.04/0.72/0.59/0.99/0.83/1.20/0.95/6.45/0.00. Benign polling is not
carrying the numerator. The series is flat either way, so the control
confirms the flatness rather than rescuing a signal.

The heaviest single repeat in the corpus is one Bash input-digest seen 43
times, and the top 20 are dominated by Bash, ToolSearch, and TaskUpdate. That
distribution is consistent with polling and task bookkeeping, which is what
the brief predicted, and it is why the control was pre-registered.

## Result 3: the compaction natural experiment (primary analysis)

**MEASURED.** 78 main-thread `compact_boundary` records (80 in the corpus,
2 in sidechains). All 78 carry `compactMetadata.preTokens`.

Where compaction actually fires:

| preTokens band | boundaries |
|---|---|
| 0 | 3 |
| 100k | 3 |
| 200k | 1 |
| 400k | 67 |
| 500k | 2 |
| 900k | 2 |

Min 28,814, median 467,446, max 969,333. The occupancy of the turn
immediately preceding each boundary agrees (median 465,350), which is an
independent cross-check on the attribution.

**This is the structural result.** The auto-compact threshold on this machine
sits at roughly 467k. The reported knee is at roughly 600k. The natural
experiment is an intervention at 467k, and 67 of its 78 instances are the
same threshold firing over and over. It holds session age fixed, exactly as
designed, and it does so at an occupancy below the one in question.

R3 paired results across the full sensitivity grid, post minus pre:

| scope | w | min | pairs | pre | post | diff | bootstrap CI95 | up/down/tie | sign p |
|---|---|---|---|---|---|---|---|---|---|
| window | 10 | 3 | 62 | 3.95% | 0.00% | -3.95% | -8.47 to -0.32 | 0/4/58 | 0.125 |
| window | 10 | 5 | 12 | 0.00% | 0.00% | 0.00% | 0.00 to 0.00 | 0/0/12 | 1.000 |
| window | 10 | 10 | 0 | | | | | | |
| window | 20 | 3 | 76 | 4.05% | 0.37% | -3.69% | -6.98 to -0.90 | 2/8/66 | 0.109 |
| **window** | **20** | **5** | **75** | **3.31%** | **0.37%** | **-2.94%** | **-5.86 to -0.48** | **2/7/66** | **0.180** |
| window | 20 | 10 | 11 | 0.76% | 0.00% | -0.76% | -2.27 to 0.00 | 0/1/10 | 1.000 |
| window | 40 | 3 | 77 | 4.88% | 0.86% | -4.01% | -7.59 to -1.00 | 4/8/65 | 0.388 |
| window | 40 | 5 | 76 | 4.94% | 0.88% | -4.07% | -7.72 to -1.03 | 4/8/64 | 0.388 |
| window | 40 | 10 | 74 | 4.40% | 0.90% | -3.50% | -7.05 to -0.60 | 4/7/63 | 0.549 |
| global | 10 | 3 | 62 | 4.35% | 1.53% | -2.82% | -8.06 to +1.45 | 4/4/54 | 1.000 |
| global | 10 | 5 | 12 | 0.00% | 1.67% | +1.67% | 0.00 to +5.00 | 1/0/11 | 1.000 |
| global | 20 | 3 | 76 | 5.62% | 1.29% | -4.33% | -8.60 to -0.76 | 7/10/59 | 0.629 |
| global | 20 | 5 | 75 | 4.63% | 1.31% | -3.32% | -6.96 to -0.26 | 7/9/59 | 0.804 |
| global | 20 | 10 | 11 | 0.76% | 0.00% | -0.76% | -2.27 to 0.00 | 0/1/10 | 1.000 |
| global | 40 | 3 | 77 | 6.72% | 1.67% | -5.04% | -9.82 to -1.20 | 13/11/53 | 0.839 |
| global | 40 | 5 | 76 | 6.81% | 1.60% | -5.21% | -10.02 to -1.25 | 12/11/53 | 1.000 |
| global | 40 | 10 | 74 | 5.91% | 1.64% | -4.27% | -8.74 to -0.61 | 12/10/52 | 0.832 |

R1, R1w, and R4 never reach the pre-registered 20 usable pairs in any cell.
Their best case is 9 pairs (R1w, w=40, min=3). R2 never has a usable pair,
which is moot since it was pre-registered as excluded. **The primary analysis
had power for exactly one of the four signals.**

**The R3 result does not survive its own sensitivity check, and this is a
call against the direction I would have preferred to report.** The bootstrap
CI on the mean difference excludes zero in 14 of 16 populated cells and the
mean is negative in 15 of 16, which read alone looks like a drop. It is not.
Between 52 and 66 of the 74 to 77 pairs in each cell are exact ties, 0% in
both windows, so the mean is carried by roughly a tenth of the boundaries.
The sign test never falls below 0.109. And the direction among the informative
pairs moves from 0 up against 4 down (window, w=10) to 13 up against 11 down
(global, w=40) purely as a function of the lookback scope. The pre-registered
failure criterion, "a result that flips sign or significance across the grid",
is met. So is the brief's own: "if the result is threshold-dependent, it is
not a result."

## Result 4: model stratification (not in the brief)

**MEASURED.** Model is a metadata field on the assistant record. I stratified
because the pooled band table assumes one population and the corpus does not
have one.

Turn counts by band and model, top contributors:

| band | turns | mix |
|---|---|---|
| 0 | 5,723 | fable-5 3,763, opus-4-8 917, opus-5 553, haiku-4-5 242 |
| 100k | 21,126 | fable-5 13,502, opus-5 4,463, opus-4-8 1,886, haiku-4-5 578 |
| 200k | 19,482 | fable-5 11,664, opus-5 5,497, opus-4-8 1,804, sonnet-5 361 |
| 300k | 16,147 | fable-5 9,642, opus-5 4,223, opus-4-8 1,955, sonnet-5 212 |
| 400k | 8,837 | fable-5 5,193, opus-5 1,797, opus-4-8 1,579, sonnet-5 198 |
| 500k | 2,320 | fable-5 1,188, opus-4-8 1,013, sonnet-5 119 |
| 600k | 1,269 | opus-4-8 580, fable-5 564, sonnet-5 125 |
| 700k | 568 | opus-4-8 340, fable-5 179, sonnet-5 49 |
| 800k | 92 | opus-4-8 72, fable-5 20 |
| 900k | 142 | fable-5 76, opus-4-8 66 |

`claude-opus-5` contributes 4,223 turns at 300k and zero at 500k and above.
Of the 78 compactions, 20 fire on opus-5 and 19 of those are in the 400k
band. **INFERRED:** opus-5 sessions on this machine compact at roughly 467k
and therefore never enter the region the operator observation is about, while
fable-5 and opus-4-8 sessions sometimes run past it. The mechanism is
consistent with a larger effective window on the configurations that survive,
which is what finding-016 established as the predictive denominator.

Within-model R3 and R4, with Wilson intervals:

`claude-fable-5`

| band | turns | R3 | R4 |
|---|---|---|---|
| 0 | 3,763 | 5/1,787 0.28% [0.1, 0.7] | 1/33 3.03% |
| 100k | 13,502 | 71/6,126 1.16% [0.9, 1.5] | 11/234 4.70% [2.6, 8.2] |
| 200k | 11,664 | 61/5,458 1.12% [0.9, 1.4] | 21/269 7.81% [5.2, 11.6] |
| 300k | 9,642 | 47/4,575 1.03% [0.8, 1.4] | 37/263 14.07% [10.4, 18.8] |
| 400k | 5,193 | 58/2,342 2.48% [1.9, 3.2] | 17/111 15.32% [9.8, 23.2] |
| 500k | 1,188 | 10/499 2.00% [1.1, 3.6] | 2/16 12.50% |
| 600k | 564 | 5/250 2.00% [0.9, 4.6] | 0/16 [0, 19.4] |
| 700k | 179 | 1/71 1.41% | 0/4 |
| 800k+ | 96 | 0/42 | 0/0 |

`claude-opus-5`

| band | turns | R3 | R4 |
|---|---|---|---|
| 0 | 553 | 0/310 0.00% | 0/3 |
| 100k | 4,463 | 9/2,281 0.39% [0.2, 0.7] | 6/71 8.45% |
| 200k | 5,497 | 12/2,679 0.45% [0.3, 0.8] | 3/64 4.69% |
| 300k | 4,223 | 1/2,130 0.05% [0.0, 0.3] | 2/64 3.12% |
| 400k | 1,797 | 1/825 0.12% [0.0, 0.7] | 1/29 3.45% |

`claude-opus-4-8`

| band | turns | R3 | R4 |
|---|---|---|---|
| 0 | 917 | 0/407 0.00% | 0/4 |
| 100k | 1,886 | 4/812 0.49% [0.2, 1.3] | 0/17 |
| 200k | 1,804 | 12/793 1.51% [0.9, 2.6] | 1/32 |
| 300k | 1,955 | 7/858 0.82% [0.4, 1.7] | 1/35 |
| 400k | 1,579 | 3/663 0.45% [0.2, 1.3] | 1/24 |
| 500k | 1,013 | 0/413 0.00% [0.0, 0.9] | 2/10 |
| 600k | 580 | 1/201 0.50% [0.1, 2.8] | 0/7 |
| 700k | 340 | 1/120 0.83% [0.1, 4.6] | 0/6 |
| 800k | 72 | 2/23 8.70% [2.4, 26.8] | 0/1 |
| 900k | 66 | 0/20 0.00% | 0/0 |

**MEASURED.** fable-5 R3 steps from 1.03% [0.8, 1.4] at 300k to 2.48%
[1.9, 3.2] at 400k, intervals that do not overlap, and holds near 2% through
600k. opus-5 R3 falls from 0.45% [0.3, 0.8] at 200k to 0.05% [0.0, 0.3] at
300k, intervals that do not overlap, in the opposite direction. opus-4-8 is
flat within intervals across its whole range.

**INFERRED.** Two models move in opposite directions across occupancy, and
the pooled table averages them. This is a confound neither SP1 nor the brief
named, and it applies retroactively to SP1's error-rate table, which was also
pooled across models. It does not make SP1 wrong; it makes SP1's flatness
less informative than it appeared, because a flat pooled series is what two
opposing within-model series produce.

**The fable-5 step is the single most knee-shaped thing in this data, and I
do not think it should be read as a knee.** It sits at 400k, the compaction
threshold, so the turns in that band are disproportionately the run-up to
compaction, which is the latest and hardest part of a session by
construction. That is the age confound the brief named, undiluted. The
natural experiment existed to break exactly this confound and could not,
because its intervention is at the same 467k.

## Result 5: censoring (supporting analysis)

**MEASURED.** The abandonment detector as specified is degenerate. Zero of
415 main-thread transcripts carry a terminal `result` record. The corpus
holds 413 `result` records and every one of them is in a sidechain-only file,
which is to say `result` marks a subagent completing, not an interactive
session ending cleanly. "Ended cleanly" and "was abandoned" are the same
observation on this field.

What can still be counted is where sessions last speak and where compaction
fires, by band:

| band | files reaching band | files ending here | end rate | compactions here |
|---|---|---|---|---|
| 0 | 175 | 76 | 43.4% | 3 |
| 100k | 338 | 238 | 70.4% | 6 |
| 200k | 106 | 35 | 33.0% | 2 |
| 300k | 87 | 31 | 35.6% | 1 |
| 400k | 62 | 14 | 22.6% | 63 |
| 500k | 21 | 8 | 38.1% | 2 |
| 600k | 12 | 3 | 25.0% | 0 |
| 700k | 9 | 5 | 55.6% | 0 |
| 800k | 3 | 1 | 33.3% | 0 |
| 900k | 2 | 0 | 0.0% | 1 |

**MEASURED.** 12 files reach 600k, 9 reach 700k, 3 reach 800k, 2 reach 900k.
The end rate has no usable trend at those counts; 5 of 9 at 700k and 3 of 12
at 600k are the same number within any interval you care to draw.

The compaction column is the informative one, and it says what Result 3 said:
63 of 78 compactions fire in a single band, and none fire above 500k except
one at 900k.

## Result 6: the file-history oracle (secondary success signal)

**MEASURED, and the answer is no.**

3,919 `file-history-snapshot` and 1,820 `file-history-delta` records exist,
none in sidechains. They appear in 169 and 111 of 1,570 files respectively,
so coverage is roughly 7% of transcripts against 5,209 Edit calls spread far
wider.

The delta record's fields are `type`, `messageId`, `snapshotMessageId`,
`trackingPath`, `backup`, `timestamp`, with `backup` carrying
`backupFileName`, `version`, `backupTime`, and sometimes `realParentDir`. It
records THAT a tracked file changed and names an out-of-band backup. It
carries no content digest. "Changed back to what it was" is therefore not
derivable from the transcript; it requires reading the referenced backup
files, which rule 4 forbids and which the brief's excluded scope also
forbids.

Durability is worse than coverage. `~/.claude/file-history/` holds 129
entries against 1,820 recorded deltas, so the referenced backups are heavily
reclaimed. Even a probe willing to break rule 4 would find most of them gone.

**Verdict:** not a usable revert oracle. The hash-comparison technique in
this probe is the better instrument, and it is the one worth keeping.

---

## Survivorship, for THIS proxy specifically

The brief predicted rework survives survivorship partially. **That prediction
was too generous, and the reason is the compaction threshold.**

Where rework genuinely is better than tool errors, as predicted: rework is a
leading indicator recorded before the censoring event, where a fatal error IS
the censoring event. Turns on the way to a bad end are in the corpus. That
part holds.

Where it is worse than the brief expected:

- **The high-occupancy population is selected on CONFIGURATION, not just on
  outcome.** A session passes 500k only if its configuration did not
  auto-compact at roughly 467k. Measured: opus-5 contributes zero turns above
  500k. So the 600k+ sample is not "sessions that went well enough to get
  there", which would be a bias of degree; it is a different set of
  model-and-window configurations, which is a change of population. Comparing
  it to the low bands is comparing two things that differ in more than
  occupancy.
- **Asymmetric censoring still points the wrong way for a negative result.**
  If degrading sessions are removed preferentially at high occupancy, the high
  bands hold a cleaner population and can show a DOWNWARD trend in rework from
  selection alone. **A flat or falling rework rate above 600k is therefore not
  evidence against the knee.** SP1 recorded the same caution; nothing here
  weakens it.
- **The within-session design does not repair it here.** The brief's argument
  was that survivorship applies equally to both windows and differences out.
  True, and irrelevant, because both windows sit at an occupancy the question
  is not about.

The one place survivorship is measurable rather than assumed is the
compaction column of Result 5, and what it measures is not agent degradation.
It measures where the harness intervenes.

---

## The alternative each result could and could not exclude

Stated because a measurement that cannot discriminate between its hypothesis
and the competing one is not evidence for either.

| result | consistent with | also consistent with | discriminated? |
|---|---|---|---|
| Flat R3 band table | no occupancy effect on rework | survivorship producing a cleaner high-band population; opposing within-model trends averaging out | **No.** Result 4 shows the second mechanism is live. |
| R4 rising to 500k then zero | rework rising with occupancy | session age rising with occupancy (the named confound); and the zeros are 0/36, whose interval contains the low-band rate | **No.** The natural experiment was the discriminator and it could not run at these occupancies. |
| Natural experiment R3 drop | occupancy-driven degradation relieved by compaction | a mean carried by ~10 of 76 boundaries with the sign test at p >= 0.109 and the direction flipping with lookback scope | **No.** Fails its own pre-registered sensitivity check. |
| fable-5 R3 step at 400k | a knee near 400k for that model | the 400k band being the pre-compaction run-up, which is the latest and hardest part of every session | **No.** Same confound, no available intervention. |
| Zero compactions above 500k | nothing degrades enough up there to trigger one | the auto-compact threshold simply sits at ~467k and the sessions above it have larger windows | **Yes, in favour of the second.** 67/78 at one threshold with a median of 467,446 is a harness setting, not a behavior. |

---

## Against the brief's stated success and failure signals

- **Primary (compaction natural experiment).** NOT MET. R3 cleared the
  20-boundary bar with 75 usable pairs, but the result is threshold-dependent
  in exactly the way the brief pre-registered as disqualifying. R1, R1w, and
  R4 never cleared 20 pairs in any cell.
- **Secondary (R3 band-wise).** NOT MET. No monotone trend whose interval
  excludes flat anywhere in the pooled range.
- **Supporting (R5).** NOT COMPUTABLE as specified. No main-thread transcript
  carries a terminal `result` record.
- **file-history as revert oracle.** ANSWERED, negative. No content digest in
  the record, 7% file coverage, 129 surviving backups against 1,820 deltas.
- **Failure signal "the base rate swamps the signal".** FIRED, in the form
  the brief named: the result moves with the qualification threshold.
- **Failure signal "rework rises after compaction across the board".** DID
  NOT fire. Post-window rework is lower or tied in most cells. R2 exclusion
  did its job.
- **Failure signal "fewer than 20 boundaries survive".** DID NOT fire for R3
  (75 of 78). FIRED for every other signal.

## Consequence for the dominated-region argument

SP1 recorded, against its own interest, that the dominated-region claim in
`context-pressure-is-an-operating-point-not-a-fill-level.md` rests entirely
on operator observation and should not be built into a default until a better
signal exists. **That standing recommendation is unchanged.** This probe adds
one negative measurement and three structural reasons the corpus cannot
produce a positive one, which strengthens the case that the next step is a
controlled trial rather than more mining.

## What would change the read

- **A controlled experiment**, as SP1 already named: the same task attempted
  from different starting occupancies, with rework scored by the hash
  comparison in this probe. It fixes the task, varies the condition, and is
  the only design that escapes both survivorship and the age confound. The
  detector now exists and costs nothing per run, so the only cost is quota.
- **Raising the auto-compact threshold** on a fable-5 or opus-4-8
  configuration for a handful of deliberate sessions would put boundaries
  above 600k and give the natural experiment an intervention where the
  question lives. Cheaper than a full trial and it reuses this analysis
  unchanged.
- **Stratifying by model in any future corpus measurement.** Result 4 shows
  the pooled view is misleading in both directions. This applies to SP1's
  table as much as to this one.
- **A different censoring field.** The `result` record does not mark
  interactive session end. Finding one that does, or accepting that it does
  not exist, is a prerequisite for the hazard view the brief wanted, and that
  view is the one measurement whose interpretation improves rather than
  degrades under survivorship.

## Excluded scope, honored

No detector shipped. No model calls, no sessions run, no quota spent. No
message content read. No file path published. Other harnesses (codex, gemini)
not touched.

## Handling note

The corpus is the operator's own working history across 110 project
directories including private repositories. This probe read tool names,
argument digests, file-path digests, timestamps, token counts, model names,
and record metadata. Every number above is a count or a rate derived from
those. Nothing in this document is transcript text.

