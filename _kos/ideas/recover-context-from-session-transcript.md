# Recover context by mining the previous session's transcript

Status: idea (pre-hypothesis). Generative, carries no commitment.

## The trap this comes from

When a marvel-managed seat freezes (auth expiry, crash, context exhaustion,
a wedged interactive prompt) the only recovery today is a relaunch, and a
relaunch starts the harness fresh. Every seat's in-flight reasoning is lost.
On 2026-09-15 an account change expired the whole twin fleet's tokens at once;
the fix was a rolling shift, which re-authed all eleven seats and lost their
in-flight work. The seat that was mid deep-research lost that pass; its
committed PR survived, its live reasoning did not.

The trap underneath: a seat frozen on a 401 cannot act to checkpoint itself
before the restart. So "have the departing agent write a handoff" (marvel's
current model, probe-shift-handoff-artifact) fails exactly when recovery
matters most, because the departing agent is the thing that is stuck.

## The idea

Give marvel a built-in recovery method that does not depend on the frozen
seat being able to act:

1. Identify the previous session's transcript file (the harness's own session
   log: the Claude Code JSONL, or the equivalent for codex, opencode, crush).
2. Spin up a utility harness (an ephemeral, cheap, fast model, or a
   purpose-built backend) whose only job is to scan that transcript.
3. Assemble a targeted recovery context: cherry-pick the load-bearing spans
   (the current task and its acceptance criteria, decisions made and their
   rationale, artifacts produced with their ids, open asks, the last good
   checkpoint) and drop the bloat (verbose tool-output dumps, retracted
   reasoning, redundant re-reads, superseded plans).
4. Bootstrap the fresh generation with that distilled context, so it resumes
   with continuity rather than from zero.

The departing seat never has to act. The recovery agent reads the corpse.

## Why the recovered context can be better than the original

A live session's context accretes noise: full tool outputs, dead ends,
reasoning it later retracted, files re-read three times. A curated recovery
context keeps the props and discards the scaffolding. That is the F25
stage-rig discipline (backdrops authored, props verbatim, level-of-detail by
cue) applied to session recovery, and the CogCanvas result is the warning
label: recursive summarization measured 19 percent recall against 97.5
percent for verbatim artifacts. So the utility does not summarize the
transcript into prose; it SELECTS which verbatim spans to carry (the props)
and writes a short authored orient over them (the backdrop). Recovery becomes
a re-focusing opportunity, and the recovered seat may run cheaper and sharper
than the one it replaced.

## Conditions it serves

- Forced relaunch after an auth or account change (the 2026-09-15 instance).
- Context exhaustion: instead of a hand-written shift handoff, mine the
  outgoing transcript for a distilled continuation. A better shift primitive
  than the current departing-agent handoff.
- Crash or crash-loop, where the seat produced no handoff at all.
- A wedged interactive prompt cleared by relaunch rather than by answering it.
- Operator-initiated re-focus: deliberately distill a bloated long-running
  session into a tighter one, no freeze required.

## The utility harness

- Ephemeral, marvel-spawned, one job then gone. A natural fit for the
  completion semantics marvel already has for headless roles (ADR-010).
- Model and backend are a cost-or-quality dial: a small fast local model (see
  local-llm-for-marvels-own-purposes) may be enough for span selection; a
  stronger model may pick better. Measure before deciding.
- Reads the transcript as untrusted data, never as instructions (the same
  discipline the collection tooling applies to third-party material).

## Open questions and tensions

- Span selection without fidelity loss: what heuristic or model picks the
  load-bearing spans, and how is it evaluated against "did the recovered seat
  need something that was dropped"?
- Relation to the harness's own resume: Claude Code --resume restores the FULL
  context, bloat included; this is selective and cross-harness. Replacement,
  complement, or a layer on top?
- Where the distilled context is injected: an appended system prompt, a first
  user turn, or a file the seat is told to read first. Each harness differs.
- The durable-intent path (marvel#276 R5, director finding-169 refuse-and-park)
  preserves the ASK; this preserves the WORKING STATE. They compose: the ask
  says what to do, the recovery context says how far you got.
- Cost: the utility run plus the recovered context's tokens against the
  wall-clock and re-derivation of a cold restart (probe-cold-shift-wall-clock
  holds the cold number to beat).

## Falsifiable claims to probe

- A distilled recovery context lets a fresh seat resume a task with fewer
  tokens and less re-derivation than a cold restart, on a measured task.
  (Compare cold-restart against full-resume against distilled-recovery on
  token count and time-to-first-useful-action.)
- The recovered seat completes the task at equal or better quality than the
  frozen one would have. (Requires a task with a gradeable outcome.)
- Span selection loses nothing load-bearing: the recovered seat never has to
  re-fetch a fact that was in the dropped bloat. (Falsifier: a recovered run
  that re-derives a dropped decision.)

## Relations

- marvel#276 (frozen-session detection, classification, remediation; this is
  the richer form of R5, durable intent across a relaunch).
- probe-shift-handoff-artifact, probe-cold-shift-wall-clock (the current
  handoff model and the cold-restart cost this must beat).
- probe-compaction-ground-truth-mining (mining a transcript for ground truth
  is the adjacent technique).
- context-pressure-is-an-operating-point-not-a-fill-level,
  bound-context-instead-of-measuring-it (context as a managed resource).
- local-llm-for-marvels-own-purposes (the utility harness's backend).
- aae-orc F25 stage-rig (backdrops authored, props verbatim) and the CogCanvas
  verbatim-recall finding (the fidelity discipline this must honor).
