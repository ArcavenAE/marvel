# Probe brief: compaction ground truth is already on disk, dozens of times

**Status:** OPEN (brief only; not started).
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

Observed on kinu, 2026-08-08, over `~/.claude/projects/**/*.jsonl`
(1535 files across 54 project directories):

```
  44 files contain a system/compact_boundary line
 112 such lines, of which:
      71  carry compactMetadata with 8 keys (incl. preCompactDiscoveredTools)
       7  carry compactMetadata with 7 keys (no preCompactDiscoveredTools)
      34  carry no top-level compactMetadata at all
```

So the usable corpus is **78 labelled events across two schema
generations**, not the flat count.

**A correction, and a method note that matters more than the number.** An
earlier draft of this brief said 46 files and 126 lines. A second count by
another reader said 44 and 105. The recount above says 44 and 112. All three
were taken on the same host within an hour. The counts diverge because the
counting methods diverge (match on `compact_boundary`, match on
`compactMetadata`, parse and inspect the object) and because the corpus is
LIVE: sessions are being appended to while it is being counted, including
the session doing the counting.

The rule that follows is the useful part: **state the method, not the
number, and re-count at probe time.** Do not inherit any figure in this
brief, including this one. Snapshot the corpus before mining so the
denominator of the analysis does not move underneath it.

The two key sets present, with the more recent one first:

```
trigger, preTokens, postTokens, cumulativeDroppedTokens,
durationMs, preservedSegment, preservedMessages, preCompactDiscoveredTools
```

and the same list without `preCompactDiscoveredTools`. One sampled record:

```
trigger: auto
preTokens 467021  ->  postTokens 13774
cumulativeDroppedTokens 453247
durationMs 154351
```

So the ground truth finding-007 wanted is a local corpus of labelled
compaction events, each paired with the full per-assistant-message
`message.usage` series that precedes and follows it in the same file.
Mining it costs zero model calls.

The 34 lines with no top-level `compactMetadata` are the first thing to
resolve, since they are 30 percent of the matches. Either they are an older
generation that predates the field, or `compact_boundary` appears on them in
some other position (a nested value, a tool result). Whichever it is, it
decides whether the usable corpus is 78 or larger.

**This is a probe, not a finding.** Nothing below has been computed. The
numbers above are a count and one sampled record.

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
- **The subagent question.** No `isSidechain: true` assistant line exists in
  the local corpus, so subagent context is not minable this way and stays
  with `subagentStatusLine`.

## Handling note

The corpus is the operator's own working history across 52 project
directories, including private repositories. This probe reads token counts,
timestamps, and compaction metadata. It has no reason to read message
content, and should not. Any artifact it produces (fixtures, tables) should
carry counts and derived numbers, never transcript text.

## What would change the read

Two schema generations are already visible in the counts above, and 34 of
112 matching lines carry no top-level `compactMetadata` at all. If the older
generation turns out to dominate, the usable corpus shrinks and the probe
should say so rather than mixing generations. Field presence is checked per
instance before anything is aggregated, not assumed from the match count.
