# finding-016: the denominator that predicts compaction is the effective auto-compact window, not the context window

**Date:** 2026-08-08
**Probe:** `_kos/probes/probe-compaction-ground-truth-mining.md` (SP2, SP3,
and the three pre-registered falsifiers)
**Medium:** mining local artifacts. Zero model calls.
**Question:** `question-interactive-context-pressure`; bears on
`question-shift-triggers`.
**Status:** measured, with one retracted intermediate claim recorded below
because the way it was wrong is the finding's main lesson.

## Retracted intermediate claim, and why it is instructive

The first draft of this finding reported that Claude Code auto-compacts at "a
hard absolute constant near 467k, not a fraction of anything," on the strength
of 63 events clustering inside 3,509 tokens.

**That was wrong, and it was wrong in the most ordinary way available: I
measured a realized value without checking whether a SETTING explained it.**

```
~/.claude/settings.json        "autoCompactWindow": 500000
measured p50 fire point                            467,635
                               500,000 - 467,635 =  32,365
```

The binary also carries `CLAUDE_CODE_AUTO_COMPACT_WINDOW`,
`AUTOCOMPACT_PCT_OVERRIDE`, `autoCompactThreshold`, `autoCompactEnabled`, and
`DISABLE_AUTO_COMPACT`. The behavior is configured, not innate, and the
~32k gap is inside the 33-45k buffer `internal/usage/doc.go` already reasons
about. **The doc's model was right in shape. I mistook a configured value for
a law and briefly concluded the doc was wrong.**

The general rule this earns: **a measured threshold is uninterpretable
without the settings that govern it.** Raw range is not enough. Every harness
has its own settings surface, and a fleet controller must read the setting
and the realized value together or it will confidently describe one machine's
configuration as the harness's nature.

## The actual result

Against the EFFECTIVE window (the operator-set `autoCompactWindow`), not the
model's context window:

```
n = 63 auto-compactions, effective window 500,000

fire point p50            467,635   =  93.5% of the effective window
headroom at fire  p0 -12,487  p50 32,365  p100 33,932
IQR of fire point          1,609   =   0.3% spread
events exceeding the setting: 1 (512,487)
```

The same 63 events read as **46.8% of a 1M context window**. That is the
finding in one line: **CTX% computed against the context window is roughly
half the number that predicts compaction.**

And the effective-window figure converges with the only other harness
measured for it. codex was independently observed firing at 93.5% and 86% of
its window (which it publishes already effective, per the sweep). Two
harnesses, different vendors, both compacting at about 93.5% of whatever
window is actually in force.

## What this means for the shift trigger

A trigger armed at "90% of the resolved window" behaves completely differently
depending on which denominator marvel resolved, and marvel currently resolves
the model's context window:

- Against the effective window (500k): fires at 450k, about 17k before the
  harness would. Correct and useful.
- Against a 1M context window: fires at 900k, which the session never reaches
  because the harness compacts at 467k. **The trigger never fires**, and it
  looks correctly configured while being unreachable.

So the earlier conclusion survives with a corrected cause. It is not that a
percentage trigger is unreachable in principle. It is that **it is unreachable
whenever the denominator marvel divides by is not the denominator the harness
acts on**, which is the current state for any session whose context window
exceeds its auto-compact window.

This makes "what is the reported percentage a percentage OF" a required field
rather than a nicety. `LimitSource` grades where the denominator came from; it
does not record which QUANTITY it is. A window and an auto-compact threshold
are both plausible denominators and they differ by 2x here.

## Assign and verify are one loop, not two options

An earlier framing in this arc offered a choice: refuse a derived threshold
and LEARN the realized one, versus ASSIGN the threshold at spawn since marvel
constructs the process environment. Both halves were argued as alternatives.

**That framing is wrong. Assignment without measurement is open-loop.** marvel
cannot know that an assignment took effect, that it is still in effect, or
that the harness honored it, except by observing the realized fire point.
`AUTOCOMPACT_PCT_OVERRIDE` existing does not mean it is honored by the build
in the pane, and a setting written to a config file can be overridden by an
env var, a project-scope file, or a version that renamed the key.

The correct shape is one control loop:

1. **Assign** at spawn (`CLAUDE_CODE_AUTO_COMPACT_WINDOW`, or the harness's
   equivalent), because enforcement locus 1 is shipped and this is free.
2. **Record** what was assigned, as the expected value.
3. **Observe** the realized fire point from the compaction marker.
4. **Compare.** A realized point that does not match the assignment means the
   assignment did not take, and that is a loud, attributable failure of
   exactly the kind the sweep's "loud-absent versus silent-wrong" ruling wants.
5. **Correct or refuse.**

Step 4 is the only thing that makes step 1 trustworthy, and no amount of
assignment removes the need for it. One event in this corpus exceeded the
configured window (512,487 against a 500,000 setting), which is precisely the
kind of discrepancy step 4 exists to surface and which pure assignment would
never have shown.

## Model change invalidates the whole reading, and it is not rare

Every number here is per-model. A model change moves the context window, may
move the effective auto-compact window, moves the price ratio that the
early-versus-late cost calculation depends on, and moves the rate limits
below. Treating model identity as stable for the life of a session is
unsafe, and the ways it changes are ordinary rather than exotic:

- **Vendor-initiated downgrade.** A provider may move a session to a
  different model in response to a safety or abuse signal. The session
  continues; the denominator does not.
- **Quota-driven downgrade.** Providers prompt or auto-switch toward smaller
  models as an allowance depletes. codex's own OTEL surface carries
  `codex.compaction.model_fallback` with a `model_downshift` reason, so the
  harness itself treats this as a routine event worth instrumenting.
- **Router-initiated switch.** gemini routes between models mid-session and
  announces it only on a separate `model_routing` event.
- **Injection between agents.** One agent's output becomes another's input,
  and a supervisor or peer may act on content in that output as though it
  were instruction, including instructions that change a model. This is a
  defect rather than a feature, and it will happen anyway; a fleet controller
  should not assume the model it launched is the model running.

Consequence: **the model is part of the reading's identity, not context for
it.** An occupancy sample, a denominator, and a learned threshold are only
comparable to others carrying the same model. A learned threshold must be
keyed by (harness, model, effective-window setting) and invalidated on any
change, and a model change observed mid-session should invalidate the learned
value rather than averaging across the discontinuity.

## The denominator is a function of six independent inputs, none of them ours

Model change is one axis. It is not the only one, and the full set is what
decides the design. The effective window and the point that predicts
compaction both vary with:

1. **Harness release.** Constants, defaults, and setting names move between
   versions. This corpus spans one version.
2. **User settings.** `autoCompactWindow` here, plus
   `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `AUTOCOMPACT_PCT_OVERRIDE`,
   `autoCompactEnabled`, `DISABLE_AUTO_COMPACT`. Settable per user, per
   project, and per environment, with a precedence order marvel does not own.
3. **Model.** Different windows, prices, and rate limits, changing by the
   routine mechanisms listed above.
4. **Backend service.** Measured in the 2.1.226 binary:
   `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`,
   `CLAUDE_CODE_USE_FOUNDRY`, `CLAUDE_CODE_USE_MANTLE`,
   `CLAUDE_CODE_USE_GATEWAY`, `CLAUDE_CODE_USE_ANTHROPIC_AWS`,
   `CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD`, plus `ANTHROPIC_BASE_URL` for an
   arbitrary proxy and `ANTHROPIC_BEDROCK_SERVICE_TIER`. The same model id
   through a different provider can carry a different window, different rate
   limits, and different quota behavior.
5. **Entitlement.** `context-1m-2025-08-07` is a BETA HEADER, so the 1M window
   is gated rather than intrinsic. The same model id is a 200k model or a 1M
   model depending on whether the beta is enabled for that account on that
   backend.
6. **Which model slot.** One session runs up to three: `ANTHROPIC_MODEL`,
   `ANTHROPIC_SMALL_FAST_MODEL`, and `CLAUDE_CODE_SUBAGENT_MODEL`. A session
   is not one model, so "the session's window" is already an approximation
   before any of the above moves.

**This is the argument that settles the design, and it settles it against
tables.** A model-to-window table is a snapshot of a six-dimensional space in
which every axis is mutable by a party other than marvel: the vendor ships a
release, the operator edits a setting, the provider downgrades a model, the
account gains or loses a beta. A table cannot be wrong loudly. It returns a
plausible number for a cell whose value changed, which is the silent-wrong
failure the sweep ruled is the one to avoid.

So the ladder inverts in emphasis. An in-band declaration from the harness
outranks any table because it is the only source that reflects all six inputs
at once. A learned value outranks a table for the same reason. **The table is
the rung of last resort, and its correct behavior when it misses is `?`, not
a guess.** That is roughly what `limits.go` already grades for; what this
finding adds is that the table's miss rate is structural rather than a matter
of keeping it current, so effort spent extending the table buys less than
effort spent on in-band capture and learning.

It also means the learned key from the previous section is bigger than stated
there. A learned threshold is keyed by (harness, harness version, backend,
entitlement, model slot, model id, effective-window setting), and any change
to any component invalidates rather than averages.

## Context is one constraint among several

This whole arc has treated context pressure as THE resource. It is one row.
The same session is simultaneously bounded by limits this work has not
touched at all: input tokens per minute, output tokens per minute, tokens per
day, request rate, and account or plan quota.

Those bind differently. Context pressure is per-session, resets at
compaction, and is remediated by a shift. Rate limits are per-account,
recover on a clock, are SHARED ACROSS THE FLEET, and are not remediated by a
shift at all: rotating an agent does not restore TPM, and shifting several
agents at once consumes a burst of it. A shift trigger that knows only
context can therefore make a rate-limit situation worse at exactly the wrong
moment.

That is a gap in the framing rather than in the measurements, and it belongs
in the resource matrix work rather than here. Recorded because this finding
would otherwise read as though solving context pressure solves scheduling.

## The pre-registered falsifiers

Three numbers were registered in advance as tests of the "fire early, because
late fails invisibly" argument. Two did not fire; one did.

**(a) Trigger mix. Does not falsify.** 68 auto (88.3%), 9 manual (11.7%).
The priced failure mode is the common case.

**(b) Cost ratio. FIRES, against the bar set in advance.** The bar was that
late must be worse by an order, and that inside 1.5x the claim degrades.

```
median cumulativeDroppedTokens   487,126
median occupancy at compaction   467,446
cache read at 10% of input price -> re-warm premium ~420,701 -> 1.16x
cache read at 25% of input price -> re-warm premium ~350,584 -> 1.39x
```

Both inside 1.5x. **On cost, firing late is only slightly worse.** This is a
calculation with stated assumptions, not a measurement: the price ratio is
not in the corpus, and it is itself model-dependent per the section above.

**(c) What survives verbatim. Does not falsify, and it explains (b).**

```
preserved messages per event: median 4, mode 4, min 1
distribution: 1:1 2:8 3:18 4:28 5:7 6:5 7:3 9:2 16:1 17:1 278:1 631:1 799:1
token retention (post/pre): median 4.3%
```

59 of 77 events preserve five messages or fewer verbatim. The harness keeps a
handful of recent messages and compresses roughly 96% of context into a
summary. It does not preserve the working set.

So the argument's framing survives while its metric fails: the cost of firing
late is not tokens, it is that 96% of the session's context passes through an
unauthored summarization. **The case for shifting early is knowledge
fidelity, and it should stop being argued on cost, where the numbers do not
support it.**

## Secondary observations

- **Compaction is slow.** Median 154s, max 275s. A session is unavailable for
  two to five minutes at every boundary. A shift and a compaction racing each
  other is a real interleaving.
- **Cluster A is unexplained by the setting.** Five auto events at or below
  210k (28,814 / 41,597 / 54,596 / 168,174 / 198,537). The last two fit a
  200k context window compacting near its top. The first three fit nothing
  yet, and two of them are the events where `postTokens` EXCEEDS `preTokens`
  (28,814 to 187,208 and 41,597 to 124,308). A compaction that increased
  occupancy is unexplained; plausibly session resumption, recorded as a guess.
- **One event exceeded the configured window** (512,487 against 500,000),
  which is the discrepancy the verify step exists to catch.
- **Manual compactions do not cluster**: 153k to 969k, operator chosen.

## What this does NOT establish

- **Only Claude Code, and only at one setting value.** The whole corpus ran
  with `autoCompactWindow: 500000`. Whether the ~32k buffer is a constant, a
  percentage of the setting, or a function of something else cannot be
  separated from a single setting value. Varying the setting is the obvious
  next probe and it is cheap.
- **The 93.5% convergence with codex is suggestive, not established.** Two
  harnesses at one setting each.
- **Whether the assignment mechanisms work at all.**
  `CLAUDE_CODE_AUTO_COMPACT_WINDOW` and `AUTOCOMPACT_PCT_OVERRIDE` are
  present as strings. Neither has been exercised.
- **SP4 is unrun** and is now more urgent, not less: the shipped hysteresis
  was tuned against a 200k-window geometry and is being asked to fire on a
  ~450k drop.

## Method note

Records selected by parsing each line and testing
`subtype == "compact_boundary"`, never by matching the raw string: a loose
match returns 134 lines of which 77 are records, the other 57 being this
review's own prose about compaction landing in the directory it mines. Only
counts, token fields, timestamps, and metadata were read.

## Consequences to carry

1. **Denominator identity is a required field.** `LimitSource` grades where a
   denominator came from but not which quantity it is. Context window and
   auto-compact window differ by 2x here and only one predicts compaction.
2. **Assign and verify ship together or not at all**, and the learned value
   is keyed by (harness, model, effective-window setting).
3. **Model change invalidates the reading**, and marvel should treat an
   observed model change as invalidating rather than averaging across it.
4. **Rate limits are a parallel constraint class** that a context-only
   trigger can make worse. Belongs in the resource matrix, not here.
5. `internal/usage/doc.go` is vindicated in shape and should gain the
   measured buffer and the effective-window distinction.
