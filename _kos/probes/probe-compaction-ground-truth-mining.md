# Probe brief: compaction ground truth is already on disk, dozens of times

**Status:** RUN and CLOSED 2026-08-09. SP2 and SP3 in finding-016
(2026-08-08); SP1, SP4 and SP5 in finding-023 (2026-08-09).

Results, one line each:

- **SP1, answered.** Marvel's occupancy formula tracks `preTokens` to a
  median 0.4% low (47 of 56 contiguous events within 1%), never exactly,
  and the residual is a one-request lag rather than a formula error.
  Across a resume gap the relationship disappears entirely (0.14x to
  18.6x).
- **SP2 and SP3, answered by finding-016**, and not recomputed.
- **SP4, answered in the detector's favor.** 63 of 63 labelled events
  detected, zero false negatives, minimum margin 4.03x over the guard.
  The reasoned constants are now calibrated. Five false positives across
  30,583 samples, of which two are real unlabelled context drops.
  Regression fixture at
  `internal/usage/testdata/compaction_series_claudecode.json`.
- **SP5, answered for gemini and opencode.** Neither has a labelled
  compaction corpus on this machine: gemini's chat files carry a token
  series and no compaction field at all, and opencode's
  `session_context_epoch` is empty and is a per-session pointer that
  could not hold a history anyway. codex is finding-017; Crush is the
  k2mi arm.

Two claims in the brief below are refuted by the run and are corrected in
place where they appear: subagent context IS minable (1,186 subagent
transcript files, two of the 82 boundaries inside them), and the file
counts have moved again because the corpus is live.

The result the probe did not go looking for: three separate mechanisms
report LOW pressure at HIGH pressure, the direction that silently
disables shift rotation. See finding-023.

Scripts: `scripts/mine_claude_compactions.py`,
`scripts/mine_other_harness_compactions.py`.

---

**Original brief follows, unedited except where a claim is struck.**

**Status when written:** OPEN (brief only; not started).
**Question:** `question-interactive-context-pressure`, and the compaction
half of `question-shift-triggers`.
**Probe medium:** mining existing local artifacts. No model calls, no
harness sessions, no daemon.
**Timebox:** one sitting.
**Prior work it extends:** finding-007 named "capture one long session
crossing auto-compaction with the occupancy series and compaction
metadata" as its top follow-up. It has never been run, and it is cited as
unrun by finding-008, `aae-orc-dc1j`, and `aae-orc-hpeu`.

## Why this probe exists

The measurement has been treated as expensive because crossing
auto-compaction on purpose costs real quota and a long session. That
premise is false: the crossings already happened, and Claude Code wrote
them down.

Measured on kinu, 2026-08-08, over `~/.claude/projects/**/*.jsonl`
(1535 files, 54 project directories), by PARSING each line and testing
`subtype == "compact_boundary"`:

```
 77  compact_boundary records
 77  of 77 carry compactMetadata          (100 percent)
 71  with 8 keys (incl. preCompactDiscoveredTools)
  6  with 7 keys (without it)
```

One additive optional field between two generations. Benign versioning by
any standard we would apply to our own JSON. The corpus is clean.

## Retraction, and the reason it is the most interesting thing here

An earlier draft of this brief reported "112 matching lines, of which 34 carry
no top-level compactMetadata," and told the probe that resolving those 34 was
its first job because they were 30 percent of the corpus. **That was a grep
artifact and it is retracted.** Matching the raw string `compact_boundary`
returns lines that are not compaction records at all. Parsed:

```
134  lines loosely matching the string
 77  actual compact_boundary records
 57  something else: 32 assistant, 21 user, 2 queue-operation, 2 attachment
```

The 57 are **message content**: this review discussing compaction, written
into the same transcript directory it is mining. The loose count was 112 when
first taken and 134 hours later, and the delta is the sessions that produced
this document.

So the corpus is contaminated by the act of studying it, and the
contamination is measurable and growing. Three consequences, and the last one
is not about this probe:

1. **Parse, never grep.** The probe filters on `subtype`, not on a substring.
   Any count in this brief is reproducible only with the parsing method
   stated beside it.
2. **Snapshot before mining.** The corpus is live, and this probe's own
   write-up is one of the things appending to it.
3. **Observation perturbs the appropriated side too.** The sweep found that
   attaching to Crush's contracted event stream registers marvel as a real
   client. This is the same property on the channel class that was assumed
   inert: reading a transcript directory cannot alter a harness, but WRITING
   about what you read lands in the same directory. Any agent-run analysis of
   agent transcripts has this problem, and a marvel feature that mines its
   own fleet's transcripts would have it permanently.

The corrected specimen, one record:

```
trigger: auto
preTokens 467021  ->  postTokens 13774
cumulativeDroppedTokens 453247
durationMs 154351
```

So the ground truth finding-007 wanted is a local corpus of 77 labelled
compaction events, each paired with the full per-assistant-message
`message.usage` series that precedes and follows it in the same file. Mining
it costs zero model calls.

**This is a probe, not a finding.** Nothing below has been computed.

## Hypothesis

The existing transcript corpus is sufficient to (a) measure the real
relationship between marvel's computed occupancy and the harness's actual
compaction trigger point, (b) retire the accountant's inferred
compaction-detection heuristic in favor of an explicit marker, and (c)
price the cost of a late shift, which is the number
`bound-context-instead-of-measuring-it.md` needs and does not have.

## Sub-probes and success signals

### SP1. Does marvel's occupancy formula predict the compaction point?

For each labelled boundary, compute occupancy from the preceding
assistant message using marvel's own additive rule (`input +
cache_read + cache_creation`) and compare against `preTokens`.

**Success signal:** a stated relationship between the two, with its spread.
If `preTokens` equals marvel's occupancy, the formula is validated against
the harness's own accounting on real data. If it differs by a constant or
a proportion, that constant is the correction marvel currently lacks. If it
differs unpredictably, the formula is wrong and that is the most valuable
possible outcome.

### SP2. Where does auto-compaction actually fire?

Group by `trigger`. For the `auto` cases, compute `preTokens` against the
window for that session's model.

**Success signal:** the effective auto-compaction threshold as a measured
distribution rather than the reasoned "threshold plus a 33-45k buffer"
currently carried in `internal/usage/doc.go`. Note the known complication:
`message.model` reads `claude-opus-5` with no variant marker even on a 1M
build, so sessions must be separated by observed window rather than by
model id alone, and some may not be separable at all.

### SP3. What does a late shift actually cost?

`cumulativeDroppedTokens` is the size of the uncontrolled summarization
that happens when nobody shifted in time. The specimen above dropped
453,247 tokens.

**Success signal:** a distribution of dropped-token counts across every real
event in the snapshot. This is the number that decides whether the early-versus-late
asymmetry argument in `bound-context-instead-of-measuring-it.md` holds. If
drops are consistently large, firing early is cheap by comparison and the
precision argument collapses. If drops are small, the asymmetry is weaker
than claimed.

### SP4. Would the accountant's inferred detector have caught these?

The accountant infers compaction from a downward step with a hysteresis
that its own source marks as reasoned rather than measured. The comment at
`internal/usage/accountant.go:14-31` says so in as many words: the bound was
"chosen to be un-fireable by ordinary variation rather than calibrated: no
live compaction was crossed on any harness". The constants are
`defaultHysteresisTokens = 2048` and `defaultHysteresisFraction = 0.10`, and
the single compaction geometry the comment cites is one instance recovered
from orc finding-066 (roughly 167k down to 96k). This probe would replace an
n-of-1 with the whole snapshot.

Replay the occupancy series across each boundary and check whether the
existing detector fires, at the right record, without false positives
elsewhere in the file.

**Success signal:** a false-positive and false-negative count for the
shipped heuristic against every labelled event. That is a regression fixture
as well as a measurement, and it is the cheapest validation of shipped
behavior available anywhere in this arc.

### SP5. Does the same marker exist in the other harnesses?

Phase 1 of the remainder sweep recorded compaction signals per runtime that
have not been checked against real data: codex drops its occupancy level to
zero (observed three times in one session) and writes `compacted` and
`context_compacted` records; Crush writes `prompt_tokens = 0` on sessions
carrying a summary message; opencode has a `session_context_epoch` table,
empty on this host; gemini emits a `chat_compression` metric with before
and after counts.

**Success signal:** for each runtime with local artifacts, either a labelled
compaction corpus of its own or a statement that none exists on this
machine.

Two further corpora are confirmed present on kinu as of 2026-08-08, which
widens this sub-probe from a long shot into the second half of the job:

- **codex**: roughly 209 session files under `~/.codex/sessions/`. The
  likeliest second compaction corpus.
- **gemini**: 26 files under `~/.gemini/tmp/*/chats/`, carrying **477
  messages with a per-turn token series** shaped `{input, output, cached,
  thoughts, tool, total}` with the model name alongside. Measured directly,
  not inferred from docs. Notably richer than gemini's documented
  `AfterModel` hook payload, which promises only `totalTokenCount`. Whether
  any compaction marker rides these files is unknown; gemini's compaction
  numbers are documented on its OTEL path
  (`gemini_cli.chat_compression{tokens_before, tokens_after}`), which is a
  different channel and may mean the local files carry the series without
  the labels.

Three independent local corpora is the argument for doing this probe before
any probe that spends model quota.

## Excluded scope

- **Building anything.** This probe produces measurements and fixtures. Any
  change to the detector, the threshold handling, or the shift trigger is
  downstream work.
- **Forcing a compaction.** The premise is that this is unnecessary. If a
  sub-probe genuinely needs a controlled crossing, record that as a result
  rather than running it here.
- ~~**The subagent question.** No `isSidechain: true` assistant line exists in
  the local corpus, so subagent context is not minable this way and stays
  with `subagentStatusLine`.~~ **REFUTED 2026-08-09 (finding-023).**
  Subagent transcripts are 1,186 separate files under
  `<project>/<session>/subagents/`, carrying thousands of
  `isSidechain: true` usage lines, and two of the 82 compaction
  boundaries are inside them. The claim was true of the flat glob it was
  measured with and false of the corpus. Subagents compact, in windows
  marvel is not watching.

## Handling note

The corpus is the operator's own working history across 52 project
directories, including private repositories. This probe reads token counts,
timestamps, and compaction metadata. It has no reason to read message
content, and should not. Any artifact it produces (fixtures, tables) should
carry counts and derived numbers, never transcript text.

## What would change the read

Field presence is checked per instance before anything is aggregated, not
assumed from a match count. The corrected census says every record carries
`compactMetadata` and the two generations differ by one optional field, so
generation-mixing is not the hazard it appeared to be. The hazard that
remains is the counting method itself: parse and filter on `subtype`, never
match the raw string, or 57 lines of this review's own prose enter the
sample.
