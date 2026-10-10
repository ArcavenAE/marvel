# finding-marvel-so6h: an idle claude seat's statusline cost and context figure stayed flat for 30 minutes

**Date:** 2026-10-10
**Confidence:** frontier. One seat, one host, one claude build, one 30-minute window.
**Probe:** `_kos/probes/probe-idle-claude-statusline-flat.md`
**Placement:** marvel. The subject is a turn signal for marvel's watcher (`docs/design/marvel-watcher.md`, section 8, decision 2).

## Why this matters

The watcher needs to know when a claude seat took a turn, and an interactive claude emits no event stream that says so. The statusline's cost and context figures are a candidate signal, but only if they stay flat while a seat idles. This probe checked that half by observation only.

## Result: FLAT

- **Seat:** a marvel-managed interactive claude reviewer seat with the statusline context feed on, chosen by the team supervisor. The supervisor reported it idle at the start and stated it would dispatch nothing to it during the window. That statement is the only source for "nothing dispatched": the probe did not read the seat's inbox, its roster state or a dispatch ledger (see limit 4).
- **Window:** 2026-10-10T05:44:10Z to 06:14:12Z, one sample every 60 s, 31 samples.
- **Valid samples:** 31 of 31. The session's context `observed_at` advanced on every sample, so the statusline command ran between each pair.
- **Turns:** none. The capture showed no working indicator on any sample.
- **Figures:** the cost read $30.87 and the context read 34% on all 31 samples.

Each sample recorded these fields: UTC time, cost, context %, the context `observed_at`, LAST-ACTIVE, ACTIVE%, a turn flag, and the pattern's match count (1 on every sample). Nothing else from the pane was kept.

## Limits

1. One seat, one host, and one claude build (Opus 5.5, 1M context), in one window. The harness (Claude Code) version, which the brief asked for, was not recorded: no sample kept it, so the finding cannot name it.
2. This statusline's token figure is a context percentage, not a token count. The probe compared the cost and the context %.
3. Stale paint is not ruled out. `observed_at` shows the statusline command ran, not that the frame was repainted after it. The only way to force a repaint, `marvel capture --repaint`, resizes the seat's pane, so the probe did not use it.
4. The cross-check against events is weak. An interactive claude emits no `agent.*` stream, and the seat's only event in the ring was its adoption, before the window. So "no turn" rests on the capture's turn flag, which limit 3 says may be stale paint, and on the supervisor's statement that nothing was dispatched. Neither is independent proof that the seat was idle. The figures not moving across 31 fresh `observed_at` stamps is consistent with idle, but it is the thing under test, so it cannot also confirm it.
5. No turn happened, so whether the figures move on every turn was not observed. The C2 scratch kit (watcher section 6) still answers that, and it still gates any ring authority.

## Side observation, for #801

Through the whole idle window, `get sessions` showed LAST-ACTIVE between 6 and 8 s and ACTIVE% at 100% for this idle seat, both marked as statusline-fed. That is #801's symptom (the statusline heartbeat re-stamps `ContextAt`, so an idle claude seat never reads as quiet), seen on a live seat.

## Ruling that followed

The operator ruled watcher decision 2 on 2026-10-10: "statusline". A statusline cost or context change counts as a turn, for reports only. It never restarts, rings or routes. If a later test shows misses, it falls back to `unknown`. It is recorded in `docs/design/marvel-watcher.md` section 8. Decision 1, the design, is still open, so this ruling files no ticket by itself.
