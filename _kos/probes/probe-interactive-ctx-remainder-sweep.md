# Probe brief: interactive CTX% remainder sweep (codex, gemini, opencode, Crush)

**Status:** OPEN (brief only; not started).
**Question:** `question-interactive-context-pressure` (the non-claude remainder)
**Probe medium:** desk research first, then code (live rig per surviving candidate)
**Timebox:** phase 1 one sitting; phase 2 one session per surviving runtime, run only as fleet priority demands
**Prior work it extends:** finding-011 (statusline side channel, solved
interactive claude), finding-008 (native OTEL, capable-but-unverified for
codex and gemini), finding-007 (headless accountant), aae-orc-dc1j (the
decision ticket), aae-orc-7hzb (whose "other harnesses statusline
equivalents" remainder this brief absorbs).

## Why this probe exists

Interactive claude CTX% shipped 2026-08-05 via the statusline side channel:
one day, one brief, because the work ran secondary-first (catalog candidate
channels from docs/source, then one empirical rig on the best candidate).
This brief applies the same shape to the four remaining runtimes instead of
pre-committing to per-runtime probe pairs. The remainder matrix at time of
writing:

| Runtime | Known channel | Actually unknown |
|---|---|---|
| codex | OTEL (capable per finding-008, not live-verified) | occupancy derivation; whether a statusline-like hook exists |
| gemini | OTEL (capable, not live-verified; no marvel adapter yet) | same, plus whether the fleet runs it at all |
| opencode | none verified | its client/server HTTP API and plugin surface are unchecked candidates |
| Crush | none | config and event hooks unchecked |

## Hypothesis

For each runtime, at least one cooperative channel (OTEL, statusline-like
hook, HTTP API, plugin) exports enough to compute raw occupancy with a
denominator, without owning the PTY and without capture-pane scraping. Where
no such channel exists, the honest answer is documented headless-only, not a
scraper.

## Phase 1 — secondary sweep (one pass, all four runtimes)

Desk research only: docs, source, changelogs, issue trackers. No daemons.
Catalog every candidate side channel per runtime:

- codex: OTEL metric/log semantics (does anything carry cumulative context
  tokens plus the window size, or only per-turn usage?); any statusline or
  notify-hook analog; `conversation_starts` as denominator source
  (finding-008 note).
- gemini: OTEL semantics, same questions; also record whether adding a
  marvel adapter is even scheduled, because a channel for a runtime marvel
  cannot launch is shelf inventory.
- opencode: the client/server HTTP API (what does the server expose about a
  session's token state?); the plugin API (can a plugin observe usage and
  call out?); any TUI statusline configurability.
- Crush: config surface, event hooks, anything resembling a status command
  or emit-on-tick facility.

Output: a channel-candidate table with one verdict per runtime, one of:
`candidate: <channel>` (carries occupancy + denominator, or enough to derive
them) or `no channel` (nothing exports the pair).

## Phase 2 — primary verification (surviving candidates only)

The finding-011 rig shape, per surviving runtime: launch the runtime
interactively under marvel, attach the candidate channel, drive turns, and
verify the exported figure against ground truth (the harness's own rendered
indicator, or a token-counted transcript). Run in fleet-priority order:
codex and opencode before gemini and Crush unless operations say otherwise.

Success signal per runtime: a live `marvel get sessions` row showing CTX%
for an interactive session of that runtime, fed by the candidate channel
through the existing heartbeat RPC (or a documented reason the channel
needs a new ingest path).

## Kill rule

A runtime whose phase-1 verdict is `no channel` gets recorded in the
question node as "interactive CTX% not meterable for <runtime>; documented
headless-only" (dc1j option (b)) and phase 2 is not run for it. The
capture-pane scraper path (dc1j option (a)) stays ruled out unless an
operator deliberately reopens it; fragility plus a normalized figure lost to
the statusline precedent.

## Non-goals

- Per-subagent context surfaces (that is the aae-orc-7hzb daemon-surface
  remainder, separate data plane).
- OTEL collector architecture (held per question-marvel-otel-architecture;
  if phase 2 verifies a codex/gemini OTEL channel, ingest design goes to
  that question, not this probe).
- Settings-precedence documentation for projected statuslines (7hzb
  remainder, docs work, not research).
