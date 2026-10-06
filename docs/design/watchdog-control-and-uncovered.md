# Watchdog positive control and the uncovered state

- **Status:** design for review, 2026-10-06, for #626, revised the same day
  for review 5432714252. No code lands until this is reviewed. Checked against `origin/main` 6a509e0.
- **Ruling:** the watchdog design panel's V6 (control cadence), split with
  no majority on 2026-10-06, ruled by the operator the same day as the
  panel's option (c): one control per covered harness version, at start and
  at reload, with uncovered as its own state. The operator answered "(b)"
  against a list director had lettered differently; director confirmed the
  mapping.
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

A new function in `internal/panestate`, `Control(sets []Pattern, sample
func(Pattern) (string, error)) []ControlResult`, runs once **per pattern**:
it reads that pattern's stored sample (`<id>.sample.txt`, through
`EmbeddedSample` for the shipped set), normalizes it, and classifies it
through the same `Classify` call a pane goes through, with only that one
pattern in the set and its own harness version as the session version. A
pattern passes when the result is `logged-out` at `high` with that
pattern's id; anything else, including an unreadable sample, fails it. Each
result names the pattern id, version, harness and harness version, pass or
fail, and on fail the result state and confidence or the read error.

The watchdog keeps only the patterns that passed. A failed pattern can
never match a pane.

The control proves each pattern loads and matches its own sample. It cannot
detect a harness upgrade, because the sample never changes; that is what
`uncovered` is for.

## 3. When it runs (the ruled cadence)

- **At start:** once per pattern of every covered version, before the first
  pass.
- **At reload:** whenever the pattern sets are loaded again. Today the
  embedded set loads only at start; when the validated overlay (V5,
  `Load(fs.FS, root)`) can be reloaded, every reload runs the control for
  every pattern in the new set before the watchdog uses it.
- **Not every pass.** An unchanged set over an unchanged sample proves
  nothing new per pass.

## 4. What a session reads: two coverage states

Today `HarnessState` holds only a match result, and a pass with no match
clears it (`internal/daemon/watchdog.go:151-159`). Coverage is a different
fact, so it gets its own state values and its own field, and a no-match does
not clear it.

- **`api.HarnessStateUncovered = "uncovered"`**: the session's harness has
  patterns, but none for its harness version.
- **`api.HarnessStateControlFailed = "control-failed"`**: the session's
  harness version has patterns, and every one of them failed its control.
  A version with at least one passing pattern is watched with those; the
  failed ones show only in the control results (section 5).
- **A new field, `HarnessState.Covered []string`** (`json:"covered,omitempty"`),
  the harness versions with at least one passing pattern, set on both
  coverage states. `PatternFor` keeps its meaning, the matched pattern's
  version (`watchdog.go:167`), and stays empty on a coverage state.
- **Order in `visit`:** after the quiet gate and `rule.Foreground` (which is
  where the version is known), coverage is checked **before** `Classify`. An
  uncovered or control-failed version sets that state, with `Confidence`
  empty and no evidence, and returns: there is nothing it could match. Only
  a covered version goes on to `Classify`, and only there does the existing
  no-match clear apply. A coverage state clears when the version becomes
  covered (a set loads) or the session ends, through the existing `clear`.
- **Evaluated at the quiet gate only,** as every watchdog read is today: a
  seat busy within the window is not examined, so its coverage shows once it
  next goes quiet. This keeps the watchdog's pane-read cost unchanged.
- A harness with no patterns at all reads nothing, as today: marvel does not
  claim to watch a harness it has never had a sample for.
- `get sessions` HEALTH gains no suffix for either state. `describe session`
  shows `Harness state: uncovered (claude 2.1.291; covered: 2.1.290)` or
  `Harness state: control-failed (claude 2.1.290; covered: none)`.

## 5. Surfaces and events

Two new kinds in a new `watchdog.` family, for facts about the watchdog
itself rather than about a session or an account. Both are registered in the
kinds list beside `KindAccountLoggedOut` (`internal/events/events.go:340-365`),
so the ring filter and the bus tap carry them.

- **`watchdog.control`**, one per pattern per run, info on pass, warning on
  fail, with the pattern id and version, the harness version, and on fail
  the result state and confidence or the read error. Never sample text.
- **`watchdog.uncovered`**, per harness version, at info. "Per change" means
  on a transition only: it is emitted when the set of examined sessions on an
  uncovered version goes from empty to non-empty, naming the version and the
  count at that moment, and a cleared form when that set becomes empty or a
  set for the version loads. A count change while it stays non-empty emits
  nothing, so a point release reads as one fact.
- **The daemon log line at start:** `watchdog: controls passed for claude
  2.1.290 (1 pattern)`, or the failing patterns.
- When the self-report (V3) is ruled, its status record carries the last
  control result per pattern and its time. Until then the events and the log
  are the surface.

## 6. Tests (red first)

- **The release guard, new:** `TestEveryShippedPatternPassesItsControl`
  calls `panestate.Control` over `LoadEmbedded` with `EmbeddedSample`,
  requires at least one result, and fails on any failed result. It is red
  until `Control` exists. It relates to the existing
  `TestWatchdogReadsTheCapturedScreenAsLoggedOut`
  (`internal/daemon/watchdog_shipped_test.go:32`) this way: that test stays,
  as the end-to-end check that a 2.1.290 seat shown the captured screen
  reads logged-out, but it names one version and one sample path, so a
  second shipped version would be untested by it. The new guard iterates
  every shipped pattern, so a new or edited pattern is covered with no test
  edit.
- **The guard is not green at red:** a fixture pattern whose fixed row was
  edited so it no longer matches its own sample makes `Control` report fail
  for that pattern and pass for an intact one beside it.
- A version whose only pattern fails reads `control-failed` with `Covered`
  naming the other versions; fed that sample, it never reads `logged-out`,
  and a no-match pass does not clear it.
- A version with one failing and one passing pattern is watched with the
  passing one only.
- The control runs once per pattern at start and none per pass (count calls
  across three passes).
- With an overlay reload hook (fake), a reload runs the control for the new
  set before it is used.
- A session reporting claude 2.1.291 with a 2.1.290 set reads `uncovered`,
  `Covered` = `[2.1.290]`, `PatternFor` empty; the same session with no
  claude patterns at all reads nothing.
- `watchdog.uncovered` fires once as three sessions on 2.1.291 are examined,
  not again when a fourth is, and its cleared form fires when a 2.1.291 set
  loads.
- A busy session (inside the window) on an uncovered version is not marked
  until it goes quiet.
- `State` and `HealthState` are unchanged in every case above.

## 7. Not in scope

The other panel votes that are not ruled (V3 self-report shape, V4
staleness, V5 overlay ownership, V8, V9, V10); a cross-version match (V2 is
exact only); any action on a state.
