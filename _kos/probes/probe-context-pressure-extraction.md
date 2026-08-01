# Probe brief: context-pressure extraction from real harness streams

**Status:** OPEN (brief only; not started). Scheduled as bd `aae-orc-<probe>`,
blocks `aae-orc-w5su`.
**Question:** `question-stream-attachment` (per-harness binding, producer half)
**Probe medium:** code (run each harness on kinu, read real streams)
**Timebox:** 1 session
**Prior work it extends:** `aae-orc-3cp` (closed, byte-path fidelity,
finding-005), the shim spike `aae-orc-e35c` (finding-004), finding-006 (the gap
that opened this).

## Why this probe exists

Lens 1 (the 2026-07-31 harness-first directive) requires a reliable, injectable
way to extract context pressure and selected values from the harness processes
marvel manages. Most of that is done: per-process CPU/RSS ships (procstat), and
token/cost events are parsed and live-verified for claude, codex, and opencode.
The one piece that is neither built nor understood is **context pressure**
(CTX%), and finding-006 showed why it is not a quick build.

The Claude Code parser already extracts token usage (`input_tokens`,
`output_tokens`, `cache_creation`, per-model `modelUsage`, cost). It does not
extract context-window occupancy. These are different quantities:

- **Token usage** is per-turn cost. We have it.
- **Context pressure** is how full the window is right now:
  `context_tokens / model_context_limit`. It accumulates across a session and
  **resets on compaction**.

Reaching for `w5su` without this probe invites the assumption-stacking failure
the graph has hit before: hardcode a context limit, treat a per-turn token
delta as occupancy, ignore compaction, and ship a CTX% that is wrong across
models and lies after the first compaction. The probe is cheap (a few harness
runs plus stream reads) and de-risks the whole capability.

## Hypothesis

For each cooperative harness, context pressure is reconstructable from the
harness's own structured stream without owning the PTY, using a per-harness
producer that computes occupancy from stream fields plus a model-context-limit
lookup, and detects compaction as a downward step in occupancy. The strongest
candidate signal for Claude Code is the per-turn `input_tokens` on the result
message, which is approximately the whole context fed to the model that turn.

## Sub-probes and success signals

### SP1. Per-harness context-occupancy inventory

For claude, codex, opencode (installed on kinu at the finding-005 versions),
and Crush (opaque text + sqlite), and Gemini CLI from the graph only:

- Does the stream expose context-window occupancy or remaining budget directly?
- If not, which field is the best occupancy proxy, and how accurate is it?
  For Claude Code, measure whether `input_tokens` per result tracks the true
  context size (account for cache-read tokens, which are read from context but
  billed differently).
- How is the model context limit obtained (declared by the harness, inferable
  from the model id, or must marvel carry a model→limit table)?
- How is compaction observable (a downward step in occupancy, an explicit
  event, a stream marker)?

**Success signal:** a filled table, one row per harness, stating for each
whether CTX% is (a) directly reported, (b) reconstructable with a named proxy
and its error bound, or (c) not reconstructable from the stream. Name the exact
fields.

### SP2. De-vague "selected other values"

The Lens-1 directive said "context pressure and *selected other values*." That
list was never written down.

**Success signal:** an inventory of the values marvel wants to surface per agent
beyond CPU/RSS/tokens/CTX% (candidates: turns elapsed, tool-call rate,
permission-block state, auth-required state, time-since-last-output, rate-limit
or throttle signals, error rate), each tagged with which harness exposes it and
in what field. This becomes the scope boundary for `w5su` and any follow-on.

### SP3. Where the derivation lives, and does it need the shim

CTX% is a computation on top of the parsed stream. Decide the seam:

- In each adapter's parser, or in one shared usage-accountant fed by the events
  ring (the accountant owns the model→limit table and compaction detection)?
- Does the stream alone suffice, or is this the concrete need that pulls the
  shim (finding-004, `aae-orc-gtpz`) into the daemon? kxce deliberately left the
  shim trigger open; this probe is where it is tested against a real need.

**Success signal:** a recommended seam with the reasoning, and an explicit
yes/no on whether the shim is required for context pressure (with the evidence,
not a preference).

### SP4. Reference ground-truth

Claude Code's own UI shows a context percentage. Matching how it computes that
is the cheapest correctness check available.

**Success signal:** a stated method for how Claude Code derives its displayed
context %, and whether marvel's chosen proxy (SP1) agrees with it within a
measured tolerance across at least one multi-turn session that crosses a
compaction.

## Method

Host kinu. Start a marvel daemon, run a mixed team through
`examples/mixed-adapters.toml`, drive multi-turn sessions long enough to
approach and cross a compaction on at least the Claude Code agent, and capture
each harness's raw stream via the existing FIFO path. Read the streams
directly; do not infer from documentation. For Crush, inspect `.crush/crush.db`.
Tear the daemon and sessions down afterward.

## What would change the read

A harness version bump that changes its usage/stream schema (pin the versions
tested), and a Linux re-run (B13: process and stream behavior can differ).

## On completion, the harvest

- Write a finding with the SP1 table and the SP3 seam decision.
- Update `question-stream-attachment` (producer half resolved or scoped).
- Unblock `aae-orc-w5su` with the derivation approach fixed, or, if SP3 finds
  the shim is required, add that dependency and sequence the shim work first.
