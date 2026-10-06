# Watchdog positive control and the uncovered state

- **Status:** design for review, 2026-10-06, for #626. No code lands until this is
  reviewed. Checked against `origin/main` 6a509e0.
- **Ruling:** the watchdog design panel's V6 (control cadence), split with
  no majority on 2026-10-06, ruled by the operator the same day, relayed by
  director: per covered version, plus at start and at reload, with
  uncovered as its own state. **The letter is pending confirmation:** the
  relay named option (b), whose text in the panel record is "start and
  reload" only, while the content relayed is option (c). This design follows
  the content. If the confirmed words differ, section 3 changes and nothing
  else does.
- **Builds on:** `docs/design/harness-state-watchdog-p1.md` (#554) and the
  first shipped pattern set, `claude/2.1.290/logged-out` (#598).

## 1. Why

The watchdog can be wrong in two quiet ways, and today neither is visible.

- **It can run with a pattern set that no longer matches its own sample.**
  A bad edit to a pattern's rows, a mask that eats a fixed row, or a loader
  change would leave the loop running and every seat reading `unknown`,
  which is also what a healthy fleet reads.
- **It can face a harness version it has no pattern for.** Matching is exact
  (V2), so on every claude point release the fleet is unwatched, and
  `Classify` returns `unknown` for that too
  (`internal/panestate/panestate.go:158-188`). "Nothing matched" and "nothing
  could match" look the same.

A positive control answers the first; an `uncovered` state answers the
second. Neither changes `State`, `HealthState` or any action: HEALTH stays
liveness.

## 2. What the control is

For each covered harness version, the pattern set's own stored sample
(`<id>.sample.txt`, `panestate.Sample`) is normalized and classified through
the same `Classify` call a pane goes through, with that version as the
session version. It passes when the result is `logged-out` at `high` with
that pattern's id. Anything else is a failed control for that version.

The control proves the set loads and matches. It cannot detect a harness
upgrade, because the sample never changes; that is what `uncovered` is for.

## 3. When it runs (the ruled cadence)

- **At start:** once per covered version, before the first pass. A version
  whose control fails is not watched: its sessions read
  `unknown (control failed)`, never `logged-out`.
- **At reload:** whenever the pattern sets are loaded again. Today the
  embedded set loads only at start; when the validated overlay (V5,
  `Load(fs.FS, root)`) can be reloaded, every reload runs the control for
  every version in the new set before the watchdog uses it. A reload whose
  control fails for a version keeps that version off, as at start.
- **Not every pass.** An unchanged set over an unchanged sample proves
  nothing new per pass.

## 4. The uncovered state

A session whose harness has pattern sets, but none for its harness version,
reads `uncovered` instead of `unknown`, with the versions that are covered:

- `HarnessState.State = "uncovered"`, confidence empty, `PatternFor` names
  the nearest covered version for the reader. A low-confidence match from
  another version's set is still kept as evidence, as today.
- A harness with no pattern sets at all reads nothing, as today: marvel does
  not claim to watch harnesses it has never had a sample for.
- `get sessions` HEALTH gains no suffix for `uncovered`. `describe session`
  shows `Harness state: uncovered (claude 2.1.291; covered: 2.1.290)`.
- One `watchdog.uncovered` event per harness version per change, at info,
  naming the version and how many sessions run it, so a point release reads
  as one fact. It clears when a set for that version loads.

## 5. Surfaces for the control

- `watchdog.control` event per version per run, info on pass, warning on
  fail, with the pattern id and version and, on fail, the result state and
  confidence. Never sample text.
- The daemon log line at start: `watchdog: controls passed for claude
  2.1.290` or the failing versions.
- When the self-report (V3) is ruled, its status record carries the last
  control result per version and its time. Until then the event and the log
  are the surface.

## 6. Tests (red first)

- The shipped set's control passes for every version in it (this is the
  release guard: a bad pattern edit fails CI).
- A pattern whose fixed row no longer matches its sample fails its control,
  and a session on that version, fed that sample, reads `unknown (control
  failed)`, never `logged-out`.
- A control runs once per covered version at start and none per pass (count
  calls across three passes).
- With an overlay reload hook (fake), a reload runs the control for the new
  set before it is used.
- A session reporting claude 2.1.291 with a 2.1.290 set reads `uncovered`,
  with `covered: 2.1.290`; the same session with no claude sets at all reads
  nothing.
- `watchdog.uncovered` fires once for three sessions on 2.1.291 and clears
  when a 2.1.291 set loads.
- `State` and `HealthState` are unchanged in every case above.

## 7. Not in scope

The other panel votes that are not ruled (V3 self-report shape, V4
staleness, V5 overlay ownership, V8, V9, V10); a cross-version match (V2 is
exact only); any action on a state.
