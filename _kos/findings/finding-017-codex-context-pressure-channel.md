# finding-017: the codex context-pressure channel, and the defect the exec stream was already shipping

- **Date:** 2026-08-08
- **Status:** captured. Channel verified; one shipped defect corrected in this change; two questions left open and named.
- **Probe:** codex-cli 0.146.0 on macOS arm64. Live hook capture under an isolated `CODEX_HOME`; read-only census of 209 rollout files (2098 `token_count` records); cross-reference of this repo's own codex fixtures against the operator's rollout for the same thread ids.
- **bd:** aae-orc-pt8k (this work), aae-orc-dc1j (the per-harness cooperative-channel ruling it serves)

## Summary

Four claims were checked. Three held with corrections to how they were
stated, and the fourth was refuted:

1. **The channel is conceded and it works.** Every codex hook payload
   carries `transcript_path`, an absolute path to the session's rollout
   JSONL. Verified live. The catalog named the field
   `agent_transcript_path`; that field exists on exactly one of eleven
   hooks and names a *subagent's* file.
2. **The denominator is on the wire and already effective.** Codex
   declares `model_context_window` in the rollout, and the declared
   number is below the catalog's raw window rather than equal to it, so
   marvel must not multiply. The catalog was right; the corpus cannot
   tell whether the number is keyed by model.
3. **`LayoutSubsumptive` is correct**, but the evidence the code cited
   for it could not have distinguished the alternative. A discriminating
   test exists and it confirms the declaration.
4. **`CumulationRequest` is wrong.** The `codex exec --json`
   `turn.completed` usage object is a running total, not a per-request
   level, and this repo's own fixture already showed it.

Result 4 is the one that mattered. It is the defect `internal/usage`
exists to prevent, shipped in the codex adapter, and it also settles the
architecture: the exec stream cannot produce a codex occupancy level at
all, so the rollout file that the hook points at is not one option among
several. It is the only source.

## 1. The channel

`transcript_path`, not `agent_transcript_path`.

The binary embeds a JSON Schema per hook event. All eleven input schemas
list `transcript_path` in `required`. `agent_transcript_path` appears in
exactly one, `subagent-stop.command.input`, alongside `transcript_path`,
`agent_id` and `agent_type`. It is a subagent's transcript, not the
session's. A design built on the catalog's field name would have found
the field absent on every hook that matters.

Both are typed `NullableString` while sitting in `required`. Present is
not the same as non-null; a consumer must handle null.

Live capture, isolated `CODEX_HOME`, `codex exec` with no credentials so
the run 401s after the hooks fire:

```json
{
  "session_id": "019fe41c-efc3-72f2-ad5c-45d8d411e7a0",
  "transcript_path": ".../.codex/sessions/2026/08/08/rollout-2026-08-08T20-22-09-019fe41c-efc3-72f2-ad5c-45d8d411e7a0.jsonl",
  "cwd": ".../probe-cwd",
  "hook_event_name": "SessionStart",
  "model": "gpt-5.6-sol",
  "permission_mode": "bypassPermissions",
  "source": "startup"
}
```

Four things this establishes at once, all before the first model call:

- The pointer is absolute, and it is handed over. Nothing is derived.
- `CODEX_HOME` really does relocate the state tree. The rollout landed
  in the isolated tree and the operator's 209 files were unchanged
  before and after.
- The **model name is on the hook payload**, `model` being required on
  ten of the eleven schemas (`session-end` is the exception). See §2 for
  why this does not close the denominator gap.
- SessionEnd fires too, carrying the same pointer.

The take-the-pointer rule holds for the same reason it holds on claude.
Codex's own path is `sessions/YYYY/MM/DD/rollout-<local-ISO-with-dashes>-<uuid>.jsonl`,
so a deriver needs the session's local date, its start timestamp to the
second in a dash-substituted ISO form, the daemon's timezone, and the
`CODEX_HOME` in force. Every one of those is a place to be wrong for
some sessions and right for most, which is the failure mode that makes
derivation attractive and dangerous.

Not established: whether hook trust can be pre-seeded so marvel need not
pass `--dangerously-bypass-hook-trust`. The probe used the flag. This is
on the critical path, because a design that requires the flag is not
shippable.

One catalog claim I could not reproduce and could not properly test: it
records that `CODEX_*` variables are not propagated to hook subprocesses.
My hook saw `CODEX_HOME`. But I had exported `CODEX_HOME` into codex's
own environment, so ordinary inheritance explains it and my measurement
cannot separate that from codex exporting it. No codex-*generated*
variable appeared, which is consistent with the catalog. Agent identity
should still ride the hook command line.

## 2. The denominator

Two corrections were already recorded. Both hold, and a third is needed.

**Do not multiply.** `~/.codex/models_cache.json` lists
`context_window` 272000 and `effective_context_window_percent` 95 for all
eight catalog models. The rollout declares 258400, which is
272000 x 0.95. The declared number is already effective. A marvel that
multiplies again lands on 245480, runs 5% pessimistic, and fires shifts
early. The alternative hypothesis, that the rollout emits the raw window,
predicts 258400 == 272000 and is excluded.

**A third source, not in the catalog.** The catalog names
`token_count.info.model_context_window`. `event_msg`/`task_started`
carries `model_context_window` too, at the START of every turn. Corpus:
369 task_started declarations and 2097 token_count declarations, all
258400, disagreeing in zero files. It also fired in my unauthenticated
probe session, which never received a model response at all. So the
denominator is available before the first sample, which is exactly when a
denominator is worth having.

**What the corpus cannot decide, and this is the load-bearing caveat.**
Every declaration in the corpus is 258400, for all three models that
appear (gpt-5.6-sol 1706 records, gpt-5.6-luna 369, gpt-5.6-terra 183).
The catalog gives all eight models identical `context_window` and
`effective_context_window_percent`. So "the window is keyed by model" and
"the window is one number for this account and plan" predict identical
data here. The corpus is evidence for neither.

This is why the old `limits.go` comment had the right conclusion for the
wrong reason. It said the 258400 measurement lacked a key because the
model name was never captured. The model name is captured, in
`turn_context.model` in the rollout and on ten of eleven hook payloads;
only the exec stream withholds it. Naming the model would not have keyed
anything, because nothing shows the window varies by model. A table entry
would be a per-account plan limit wearing a model name. gpt-5.4 already
refuses the key from the other direction: `max_context_window` 1000000
against `context_window` 272000.

The rung this belongs on is the feed, not the table. The window rides
the same record as the level. On the ruled ladder (`stream`, `learned`,
`manifest`, `feed`, `table`, `table-alias`) a codex reader supplies it as
`stream`, since it is the harness's own declaration in the artifact being
read. I did not add the `feed` constant: nothing produces one yet, and an
unused rung is a claim about a design that does not exist.

## 3. Occupancy semantics

`LayoutSubsumptive` is right. The evidence the code cited for it was not
evidence.

The parser cited `input_tokens` 13992 with `cached_input_tokens` 11008.
That row is equally what an additive harness with a large new prompt
produces. A subsumptive `In` is always the larger number, which is
precisely why `AdditiveConfirmed` is documented as one-sided. The row
could not have come out differently under the alternative, so it decided
nothing.

The discriminator is the window bound. A harness cannot hold more prompt
than its context window, and codex declares that window beside every
sample. Over 2081 scored records (post-compaction all-zero sentinels
excluded):

| reading | max observed | as % of the 258400 window | records over the window |
|---|---|---|---|
| `In` alone (subsumptive) | 242504 | 93.8% | 0 of 2081 |
| `In + cached + cache_write` (additive) | 482049 | 186.6% | 801 of 2081 |

The sixteen pre-compaction peaks say the same thing in the place it
matters most: under the subsumptive reading they cluster at 82.6% to
93.8% of window, which is the shape auto-compaction produces. Under the
additive reading twelve of the sixteen sit between 163% and 187%. The
remaining four are turns with small cache values where the two readings
nearly coincide, and those four decide nothing.

Under the additive hypothesis the two rows of that table would have
swapped: the sum plausible, `In` alone a small remainder. They did not.

**A second subsumption, not previously recorded.** Codex's
`reasoning_output_tokens` is a subset of `output_tokens`, not a term
beside it. Only rows with nonzero reasoning can tell: over those 1665
records, `total == input + output` holds on all 1665 and
`total == input + output + reasoning` on none, with reasoning never
exceeding output. The 432 zero-reasoning rows satisfy both and are
excluded. This matters because `RequestUsage.TotalMismatch` sums
`In + Out + ReasoningOut`; wiring a codex `Total` without a
reasoning-subset flag would report a phantom mismatch on every thinking
turn. It is inert today only because the exec stream publishes no total.

## 4. The refutation: turn.completed is a running total

`internal/usage/profiles.go` declared codex `CumulationRequest` with an
honest note that it was unverified, and framed the open question as
whether `input_tokens` accumulates *across turns*. Framed that way, the
single-turn `exec` fixtures looked unable to answer it.

They could. The `tool_call.jsonl` fixture in this repo carries thread id
`019fba87-d036-7ae1-a20e-7187ef8e3329`, and the operator's rollout for
that same thread is on disk. One turn, two model requests, a tool call
and an answer:

| source | request 1 | request 2 |
|---|---|---|
| rollout `last_token_usage.input_tokens` | 14005 | **14105** |
| rollout `total_token_usage.input_tokens` | 14005 | **28110** |
| rollout `total_token_usage.cached_input_tokens` | 11008 | **24064** |

The fixture's `turn.completed` reports `input_tokens` 28110, `cached`
24064, `output` 76. It matches `total_token_usage` field for field. The
prompt at turn end was 14105.

So marvel was reading 28110 as the occupancy level where the truth was
14105: 1.99x, on the smallest multi-request turn there is, growing with
request count. That is the defect `internal/usage/doc.go` was written to
prevent, arriving through a door the guards do not cover. Claude's
version is caught because the cumulative figure rides a terminal line and
`Sample.Terminal` excludes it structurally. Codex has no terminal line at
all (no `session.ended`), and every sample is a total rather than only
the last one, so there was nothing to mark. `CumulationViolations`, the
runtime guard, needs a terminal total to compare against and therefore
never fires for codex.

Differencing does not rescue it. Differencing the cumulative series would
recover a per-request level if samples arrived per request, but they
arrive per turn, so a difference yields the sum of that turn's requests.
On this fixture that is 28110 again.

**The consequence is architectural, and it is the answer to pt8k.** The
codex exec stream cannot produce an occupancy level. Not with a better
parser, not with a manifest window, not with differencing. Codex context
pressure requires `last_token_usage` from the rollout file, which is
reached through the hook's `transcript_path`. The channel is not the
preferred option; it is the only one.

**Not settled:** whether the accumulator resets at a turn boundary or
runs for the session. A single-turn capture is consistent with both, and
this session had no codex credentials to run a multi-turn
`codex exec resume`. It does not change the treatment, since neither is a
level, but per-session is the worse of the two. Naming it as open rather
than assuming per-session is the point.

## What changed in the code

- `internal/usage/profiles.go`: codex is `CumulationSession`, with the
  measurement.
- `internal/usage/accountant.go`: the fold honors `Cumulation`. A
  non-terminal sample declaring `CumulationSession` records spend by
  REPLACEMENT (accumulating running totals squares the count) and
  produces no occupancy level. Previously the field was set and never
  read, so the declaration bound nothing.
- `internal/usage/stats.go`: `CumulativeSamples` counts them.
- `internal/usage/limits.go`: the codex table section stays empty for
  the corrected reason.
- `internal/runtime/codex/parser.go`, `mapping.md`: the layout evidence
  is replaced with evidence that discriminates; the reasoning-subset trap
  is recorded; `handleThreadStarted`'s never-populated `model`/`cwd` are
  named as such.

**Operator-visible change:** a codex session's CTX% renders `-` instead
of a number, and `runtime.context_window` no longer lights it, because
the denominator was never the missing piece. This follows the package's
stated discipline (a wrong number is worse than absence) and the exposure
is small, since codex CTX% was already absent unless an operator had set
a window by hand. Reverting is one branch in `fold`.

Since codex was the fleet's only demonstrated "measured tokens, no
window" case, the two end-to-end tests for that state moved to opencode,
which reports levels and names no model.

## What was not established

- Whether the codex accumulator resets per turn or per session (needs an
  authenticated multi-turn `codex exec resume`).
- Whether hook trust can be pre-seeded without
  `--dangerously-bypass-hook-trust`. Critical path.
- Whether codex's window varies by model at all. Every model available
  here reports 258400.
- Whether `codex exec` propagates codex-generated `CODEX_*` variables to
  hook subprocesses. My rig inherited one from the parent shell and
  cannot separate the two explanations.
- Everything the sweep listed as untested for a tailing reader
  (`.zst` rollout compression under a live consumer, whether
  `SessionStart source:"compact"` opens a new file). Untouched here.
