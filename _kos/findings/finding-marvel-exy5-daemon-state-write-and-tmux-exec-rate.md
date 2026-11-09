---
id: finding-marvel-exy5-daemon-state-write-and-tmux-exec-rate
type: finding
confidence: frontier
title: "The daemon writes every running session to its state file each pass, and starts one tmux call per seat"
created_at: "2026-11-02"
edges:
  - target: question-host-health-guard
    type: supports
    note: "the write and exec rates are marvel's own footprint by that node's subject test"
  - target: question-healthchecks
    type: supports
    note: "evaluateHealth is the dominant state-write source, so the health pass has a write cost"
---
# The daemon writes every running session to its state file each pass, and starts one tmux call per seat

## Summary

One host's disk-write volume of about 300 GB a day traced mostly to marvel's
own bolt state file, and the code read at main f9a43400 accounts for it: two
loops that run every 2 s each persist every running session whether or not
anything changed, and the dead-seat check starts one tmux call per seat per
pass. The git process rate in the same capture is not explained by marvel's
code and stays unknown.

## Findings

Source: marvel#1214 and its two comments of 2026-11-02. The
capture is a 60 s root `fs_usage` on one host, 2026-11-02 03:05Z, reported as
aggregates only. The code reading was done at main f9a43400. Nothing here was
re-measured for this harvest.

**Measured (the capture).**

- The marvel daemon made about 8,600 pwrites a minute of 16 KB each to its
  bolt state file, each one synced: about 140 MB a minute, or about 200 GB a
  day. That is most of the host's daily volume in the issue (observed: 8,634 a
  minute).
- 827 tmux client starts a minute (a `mkdir` under the tmux socket directory
  each time).
- 773 separate git processes in the 60 s, about 960k file operations, mostly
  `lstat` across the worktrees of several repositories.

**Read from code (not measured).**

- Two loops run every 2 s (`ReconcileInterval`, `internal/daemon/daemon.go:60`;
  the reconcile loop at `:708` and the limits loop at `:716`). Both persist
  every running session on every pass. `evaluateHealth`
  (`internal/team/controller.go:1546`) calls `UpdateSession` per session
  (`:1600`). `evaluateLimits` (`internal/daemon/limits.go:15`) does the same,
  and its update function returns nil when the limit is unchanged, which still
  writes.
- `UpdateSession` (`internal/api/store.go:494`) always ends in `persistPut`
  (`internal/api/bolt.go:364`), one synced bolt `Update` per call. With about
  28 running sessions that is about 1,680 commits a minute plus heartbeats; at
  about 5 pages a commit, about 8,400 pwrites a minute, close to the observed
  8,634.
- `ReapDead` (`internal/session/manager.go:2031`, called each reconcile pass
  from `internal/team/controller.go:644`) runs one `tmux list-panes -t <pane>`
  per session with a pane, through `PaneStatus`
  (`internal/tmux/driver.go:589`). That is 30 tmux execs a minute per seat, so
  the 827 observed fits about 28 seats with panes.
- Git: marvel's own git calls are `rev-parse` (`internal/api/trusted_folders.go`,
  `internal/runtime/crush/config.go`) and `fetch` and `archive` against a bare
  mirror (`internal/view/view.go`). None walks a work tree.

## Assessment

By the subject test of question-host-health-guard, the state-write and tmux
exec rates are marvel's own footprint. The code accounts for two of the three
rates; the third (git) is unexplained and is not attributed to marvel.

The fix proposed on marvel#1214 is one PR: skip the write when nothing
changed, coalesce each pass into one bolt `Update`, and read all panes with one
`list-panes -a` per pass, with before and after counts. Making derived fields
memory-only, `db.Batch` and `NoFreelistSync` are out of scope for it. No PR
references marvel#1214 yet (checked 2026-11-02).

Open: the write rate has not been re-measured after any change, and the file
event growth cause in question-host-health-guard stays a hypothesis until it
is. The attribution of the git rate is open.

See also `finding-marvel-2uv2-bolt-and-tmux-write-rate`, written after the fix in
marvel#1228 merged: the counts measured before and after it (bolt transactions
1800 to 12 a minute and tmux execs 900 to 30 a minute at 30 seats), the lines
the cause runs through at main 8587651, and what is left.
