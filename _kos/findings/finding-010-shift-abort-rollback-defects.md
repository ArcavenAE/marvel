---
id: finding-010-shift-abort-rollback-defects
title: "Stuck-shift abort: four pre-existing defects behind one demo beat, and what the runbook never checked"
question: question-shifts
confidence: frontier
tags: [marvel, shifts, rollback, restart-policy, reconciler, demo, isolation]
bd: [aae-orc-d0pt, aae-orc-pyre, aae-orc-t6da]
provenance:
  created_by: agent
  session: marvel-shift-demo-investigation-2026-08-01
  host: kinu
  created_at: "2026-08-02"
---

# Stuck-shift abort: four pre-existing defects behind one demo beat

Running docs/demo.md beat 1d one step PAST its last command (the runbook
verifies only the `team.shift-timed-out` event; nobody had inspected the
post-abort state) surfaced what decomposed into four distinct defects.
A five-agent investigation (code analysis plus three isolated repros at
`2be837a`, `31f1ea5`, and `fe8f874`) reproduced all three observed
symptoms at all three commits: 9/9 cells. Everything below is
pre-existing; PRs #99 (usage accountant) and #101 (admission) are
exonerated by bisection, by diff scope, and because the demo manifest
declares no budget, so every #101 gate was dead in the scenario.

The healthy-path rotation is NOT in question: the same session drove
`examples/shift-demo.toml` end to end (gen 1 to gen 2, workers first,
supervisor last, `team.shift-started` / `team.shift-completed`, and a
CTX% contrast between old and fresh generations in the session table:
a g1 worker at 27% beside fresh g2 workers at 4% and 2%, from the
simulator's heartbeat producer). The defects live in the abort path
and in one restart policy.

## Defect 1: the abort claims a rollback it does not perform

`abortStuckShift` (internal/team/controller.go:916-944 at fe8f874)
emits "rolled back to gen N" from `Shift.OldGeneration`, deletes (in
the launching phase) the shifting role's new-generation sessions, and
zeroes `Shift`. It never
restores `Team.Generation`, which is written exactly once in the tree
(controller.go:801, in `InitiateShift`). Generation is monotonic per
team: one stuck shift leaks a generation permanently, and zeroing
`Shift` erases the only record of the rollback target, which is why
`describe team` shows `Generation: 2` beside `OldGeneration: 0`.

Introduced by PR #83 (5f26f86, 2026-07-31), which added the function
and its message. Fix shape and its pairing constraint (restore only
when `Phase == ShiftLaunching && RoleIndex == 0`; ship together with
Defect 2's tagging fix or the restore strands undrainable orphans):
bd aae-orc-d0pt carries the full fixer note.

## Defect 2: mid-shift repairs of non-shifting roles mint the new generation

During a shift, `reconcileShift` repairs non-shifting roles via
`reconcileRole` tagged with `t.Generation`, already advanced. Roles
that never shifted get new-generation replacements, minted before the
abort and surviving it (the abort deletes only the shifting role's
sessions). With Defect 1 leaving the counter bumped, every later
replacement is also new-generation. Dates to the original shift probe
(a762e35). Fix: tag non-shifting-role repairs with
`Shift.OldGeneration`; the team generation is aspirational until the
shift completes.

## Defect 3: restart_policy=never bypasses crash accounting (resource leak)

The `RestartNever` branch (controller.go:607-623) marks the session
failed and returns without `noteCrashAndBackoff`. Therefore: no
RoleHealth entry, so the backoff gate never fires and respawn happens
every 2s tick; `MaxRestarts` is unreachable (consulted only inside
`noteCrashAndBackoff`); `SessionFailed` is excluded from
`CountsAsAlive` so the reconciler recreates unconditionally; and
nothing reaps the failed row or its pane. Measured at all three
commits on Act 1c alone, no shift required: about one leaked live
process every 8s for the life of the daemon (17 failed rows, panes %2
through %22, at t+120s for a replicas=1 role). Dates to PR #21
(81a00b2); PR #83 only made it visible. The severest item in the set;
split to bd aae-orc-pyre (P1). Contract question to settle first:
does `never` mean "do not restart this session" (current) or "this
role stops" (what the manifest comment "terminal" implies)?

## Defect 4: a role in restart backoff is invisible

Between `restartSession`'s delete and the backoff gate's release
(about 60s), a role has zero rows anywhere: `get sessions` cannot
distinguish "cooling down" from "not declared". The only evidence is
`health.crashloop-backoff` in the event ring. Fix shape: a placeholder
row or RoleHealth surfaced in `get sessions` / `describe team`.
Latent sibling hazards recorded in d0pt: `shiftDrain` reads an empty
old generation as a successful drain, and
`ListSessionsByTeamRoleGeneration` has no state filter, so failed
sessions count as launched.

## Why CI and the demo verification both passed

`TestShiftTimeoutAbortsStuckLaunch` (controller_test.go:1181) asserts
phase, per-generation counts, and the event, but never
`team.Generation`, and its fixture is single-role with a one-hour
heartbeat timeout, so the health path never fires. The runbook's
"verified run" for beat 1d used a one-role team, matching the test
fixture rather than the three-role manifest the beat instructs the
operator to apply. The general lesson: a runbook beat verified only to
its event is verified only to its event. Post-state inspection is what
converted a passing demo into four tickets.

## Method knowledge (isolation)

Two repro arms' first attempts were silently contaminated and
discarded; the contamination mechanics are themselves durable:

- The daemon's local socket default is the hardcoded machine-global
  `config.DefaultSocket = /tmp/marvel.sock`; HOME does not move it and
  `paths.RuntimeSocket()` (the XDG-aware version) has no callers.
  A starting daemon unlinks the path with no liveness probe. Tracked
  as bd aae-orc-t6da; current behavior documented in
  docs/architecture.md (PRs #103, #104).
- Explicit sockets are not enough; DISTINCT ones are. Two arms handed
  the same explicit scratch path collided exactly as on the default.
- A daemon survives its socket being unlinked, so "socket file absent"
  cannot distinguish never-bound from displaced. The only ownership
  check that carried weight was lsof against a recorded pid.
- Event timestamps are stamped at emission (`time.Now().UTC()`), so a
  uniform clock offset between a shell and the stream it reads is
  proof the stream belongs to another daemon.

## Doc drift to fix with the code (recorded in d0pt)

docs/demo.md quotes the false "rolled back to gen 1" as beat 1d's
expected output; the capped-role note misses the one post-backoff
respawn before saturation; the crashloop-backoff note needs one clause
saying 1d is where the event becomes observable; the daemon-start
lines should carry the distinct-socket guidance.
