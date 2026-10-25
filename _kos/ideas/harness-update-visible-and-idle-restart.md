# Harness updates made visible, and an idle restart a manifest can allow

**Status: idea. Pre-hypothesis. Nothing here is designed or measured.**

Captured: 2026-10-11
Source: operator, relayed by director and the arcaven supervisor seat.

## The operator's words

> "kos idea marvel imagine a feature where marvel could automatically detect certain features like claude "Update installed / Restart to update" and manifests could be configured to allow marvel to automatically exit and resume those sessions to allow the update to take place during idle periods when that agent session is not fully engaged, and imagine where this state, of an update pending could be known, optionally displayed, possibly with a harness version column and indictators for certain conditions like a pending update and "Auto-update failed" and other conditions related to the harness version"

## Why this is worth reading

A seat that has an update installed but not yet applied keeps running the old
harness until something restarts it. Nothing in `marvel get sessions` shows
that today, so an operator learns about it only by looking at panes one at a
time. Director saw the "Update installed · Restart to update" banner in
`marvel capture` of several seats on 2026-10-11. If marvel can show the
condition, and a manifest can allow a restart while the seat is quiet, the
update lands without a human visiting each pane and without a shift change.

## How this differs from the sibling idea

`harness-update-pending-exit-and-resume.md` (2026-10-04) already proposes the
pending update as its own health condition, recovered by exit and resume when
the context is healthy. This idea adds three things it does not hold:

1. **Visibility.** The state is known and optionally displayed: a harness
   version column, and indicators for conditions tied to the version, such as
   a pending update and "Auto-update failed".
2. **A manifest opt-in.** The restart happens only where a manifest allows it.
3. **An idle gate.** The restart waits for a period when the seat is not fully
   engaged, not just for a healthy context.

The two ideas likely become one design. They are kept apart here because they
were captured a week apart from two separate asks.

## What marvel already has, read at origin/main 9bb2cf0

- **The running version of a claude seat.** claude runs under its version
  number, so tmux reports it as the pane's command, and the claude adapter
  reads it there (`Foreground`, internal/runtime/claude.go:305). The
  harness-state watchdog already passes that version to the pane classifier
  (internal/daemon/watchdog.go:285, internal/panestate/panestate.go:263). A
  version column could start from this value. No column shows it today.
- **A column registry.** Each `marvel get sessions` column is one entry in
  `sessionColumnRegistry` (cmd/marvel/columns.go:25), with an opt-in set for
  columns kept out of the default table (:53).
- **Quiet signals.** The ACTIVE% and LAST-ACTIVE columns exist. ACTIVE% claims
  "not quiet", not "busy" (cmd/marvel/activepct.go:33), so it is a candidate
  input to the idle gate, not yet a proof of one.
- **Pattern-matched pane states.** `internal/panestate` classifies a screen
  against versioned patterns per harness (today one claude set, 2.1.290).
  A banner pattern would be one more pattern set.
- **An update screen marvel already refuses to type into.** codex shows an
  "Update available" menu, and the composer check treats its lines as hazards
  (internal/composer/composer.go:92).

## Open questions

- Is "pending" detectable without reading the banner? One candidate: compare
  the running version from the pane command with the version installed on
  disk. That assumes the installed binary can be read without touching the
  seat. Unmeasured.
- Which conditions get an indicator? The operator names a pending update and
  "Auto-update failed". Others may follow from the version: a seat on a
  version no pane pattern covers (the watchdog already counts these as
  uncovered), or seats in one team on different versions.
- What makes a period idle enough? ACTIVE% and LAST-ACTIVE read quiet, not
  idle; a seat waiting on a long tool call can read quiet. The context-pressure
  idea's warning, that pressure is an operating point and not a fill level,
  applies here too.
- What does the manifest key look like, and at which level does it sit (team,
  role, seat)? The auto-shift trigger model is the closest existing layering.
- Does each harness resume after an exit, and does the resumed session run the
  new version? This is the sibling idea's first open question, and it still
  holds.
- How do the restarts of a whole team get ordered, so that not every seat
  restarts in the same quiet minute? The hibernate idea's ordered resume is
  the nearest pattern.
- Is the version column a session column, or a field in `marvel describe`
  with only an indicator in the table?

## Related

- `_kos/ideas/harness-update-pending-exit-and-resume.md`: the sibling; the
  condition and its exit-and-resume recovery.
- `_kos/ideas/fleet-hibernate.md`: ordered sign-off and ordered resume.
- `_kos/ideas/fleet-pause-resume.md` and
  `docs/design/fleet-suspend-resume.md`: freezing and resuming seats.
- `_kos/ideas/tmux-harness-state-watchdog.md` and
  `docs/design/harness-state-watchdog-p1.md`: classifying pane state by
  harness version.
- `_kos/ideas/health-signal-taxonomy.md`: what marvel knows about a seat's
  health, by harness.
- `_kos/ideas/auto-shift-trigger-model.md`: the manifest layering a restart
  rule would follow.
- `_kos/ideas/context-pressure-is-an-operating-point-not-a-fill-level.md`.
- `_kos/nodes/frontier/question-seat-harness-state.yaml`,
  `_kos/nodes/frontier/question-shift-triggers.yaml`,
  `_kos/nodes/frontier/question-fleet-pause-resume.yaml`.
- marvel#477 (detect the codex update menu and never type into it) and
  marvel#483 (seat codex config turns the startup update check off).
- `marvel daemon reexec` re-executes the daemon's own process. It does not
  restart seats, so it is not this feature.

## Became

2026-10-24: merged with `harness-update-pending-exit-and-resume.md` into one
exploration brief, `_kos/probes/probe-harness-update-exit-and-resume.md`. The
brief tests the shared premise (resume after exit runs the new version) and
both detection candidates named here (the banner, and running against
installed version). The version column, the indicators, the manifest opt-in
and the idle gate stay open questions for the design that follows the
finding.
