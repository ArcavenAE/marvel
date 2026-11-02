# finding-marvel-2uv2: the daemon wrote one synced bolt transaction per seat per tick and ran one tmux client per seat per pass; after the fix a quiet fleet writes almost nothing

**Date:** 2026-11-02
**Confidence:** bedrock for the counts below, which were measured on this
code with a counter and a tmux shim. The disk volume (GB a day) is the
operator's measurement on one fleet host and was not reproduced here.
**Placement:** marvel. The subject is how marvel's own daemon writes its state
and polls tmux; the host, the disk and the harnesses are objects.
**Source:** marvel#1214, read at main 8587651. Measured with
`Store.BoltUpdates` and a shim named `tmux` on a scratch daemon and in
`internal/team` tests; nothing ran against a live daemon.

## Why this matters

One busy fleet host wrote about 300 GB a day to disk and started about a
thousand tmux clients a minute from the daemon. The state file is about 1 MB.
An exec trace on a fleet host, 60 s, counted 1,078 tmux execs, 1,050 of them
with the marvel daemon as parent, and about 620 GB a day written for the whole
host. The daemon's share is a polling pattern, not the state it holds, and it
grows with the number of seats.

## The mechanism, with the lines it runs through (main 8587651)

- `ReapDead` (`internal/session/manager.go:2031`) called `PaneStatus` once per
  paned session each pass (`:2045`), which runs `tmux list-panes -t <pane>`
  (`internal/tmux/driver.go:589`). A pass runs every `ReconcileInterval`, 2 s
  (`internal/daemon/daemon.go:60`, used at `:708`).
- `evaluateHealth` (`internal/team/controller.go:1546`, its update at `:1600`)
  called `UpdateSession` for every running session each pass, and
  `evaluateLimits` (`internal/daemon/limits.go:15`, run by `RunLimits` from
  `daemon.go:716` on its own 2 s ticker) did the same. `UpdateSession`
  (`internal/api/store.go:494`) ended in an unconditional `persistPut`
  (`internal/api/bolt.go:364`), one synced bbolt transaction with a version
  bump, even when the callback changed nothing. `UpdateTeam` (`store.go:563`)
  had the same shape.

Three details the first reading of the mechanism did not have:

1. The limits tick is not part of the reconcile pass. Health and limits each
   pay on their own ticker, so a fleet pays the write twice.
2. `UpdateSessionContext`, `UpdateSessionSpend` and `UpdateSessionMetrics`
   change memory only and rely on the next persist to carry them. A
   skip-if-unchanged compare against the record before the update would have
   stopped them reaching the file (an existing test caught it). The fix
   compares against the bytes last written to the file for that key.
3. `evaluateHealth` stamps `LastHealthCheck` on every tick of a heartbeat role.
   Nothing reads it back, so the change test ignores that one field (operator
   ruling, 2026-11-02). The file still gets the full record whenever anything
   else changes.

## Measured

Thirty seats, `process-alive`, 60 s on a scratch daemon with a scratch home, a
scratch tmux socket and no inherited `MARVEL_*` variable. Bolt transactions are
the delta of the file's resource version, which one transaction advances once;
tmux execs are the shim's log, by subcommand.

| | before (main behavior) | after |
|---|---|---|
| bolt transactions a minute | 1800 | 12 |
| tmux execs a minute | 900 (`list-panes` x 900) | 30 (`list-panes -a` x 30) |

By caller, before, at 30 passes a minute: health 900, limits 900, nothing else
in a steady fleet. The same split in `TestWriteRateByCaller` (10 passes, 30
seats): 30 transactions and 30 `list-panes` a pass before, 0 and one
`list-panes -a` after.

A pass that does change many records now writes them once. The first health
tick after a restart over 20 seats, and a tick carrying a memory-only context
reading for all 20, each start one transaction where they started 20
(`TestHealthTickWritesChangedSeatsInOneTransaction`). A limits tick that limits
or clears 15 sessions starts one
(`TestEvaluateLimitsWritesChangedSessionsInOneTransaction`).

## What is left, and why

- **12 transactions a minute, one per 5 s.** The metrics sampler
  (`MetricsInterval`, `daemon.go:65`) updates each seat's CPU and memory in
  memory at `store.go:1148`; the next tick writes them. They are real changes,
  so the compare does not skip them, and they now go out together. Excluding
  them from the compare would be the same move as `LastHealthCheck` and was
  ruled out of this change (derived fields memory-only).
- **Heartbeats.** One transaction per heartbeat per seat (20 heartbeats, 20
  transactions, measured). Each stamps `LastHeartbeat`, so it is a real change
  and arrives off the tick. This fleet's seats do not heartbeat, so the figure
  above holds none; a heartbeating fleet pays one per beat. Ruled out of scope.
- **git.** The same exec trace counted 378 git execs in the minute, nearly all
  children of harness processes. That is harness behavior, not marvel's.

## Behavior that changed

- A no-op update no longer advances the resource version. Its only reader is
  the startup log line (`internal/daemon/daemon.go:334`).
- A persist error from a tick comes back once, from `WriteSet.Flush`, not per
  session. Memory is updated either way, as it always was on a failed persist.
  A limits tick now emits its events before the write.
- `ReapDead` confirms a pane the listing lacks with the one-pane read before
  reaping it, so a pane created after the listing is never reaped on an absence.
  A healthy pass makes no second client.

Relevant paths: `internal/api/bolt.go`, `internal/api/store.go`,
`internal/api/write_set.go`, `internal/team/controller.go`,
`internal/daemon/limits.go`, `internal/session/manager.go`,
`internal/tmux/driver.go`, `internal/team/write_rate_test.go`.
