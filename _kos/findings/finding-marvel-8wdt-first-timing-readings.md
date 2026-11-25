# finding-marvel-8wdt: the first I-451 timing readings put the reconcile pass at 27 ms with its tail in tmux, and the heartbeat at 60 times a bus beat on host B on the same synced write

**Date:** 2026-11-25
**Confidence:** frontier. Two cumulative readings from two daemons on two
hosts, taken once each, with no load test and no profile. The static reading
behind the interpretation is at marvel `a44309bd`; the daemons ran a newer
build (`f080235`), so the record points named here are the pin's.
**Placement:** marvel. The subject is marvel's own hot paths and the
instrumentation that measures them; the hosts are objects.
**Source:** the November 2026 status review of the daemon's code quality work
(held as a private record), which re-verified the structural review's 46 rows
at the pin and took the readings on 2026-11-25. The id placeholder 8wdt is
minted by the filing seat with `kos id finding`.
**Depends on:** marvel#1434 and #1438 (I-451 slice 1, the histograms and
`daemon.overrun`), which this finding is the first reading of.

## Why this matters

The structural review's two largest findings, the reconcile pass under one
controller mutex (row 1) and a synced store write per heartbeat under the
store's one RWMutex (row 2), were held on the word that the I-451 histograms
would decide them. Slice 1 landed on 2026-11-15. These are the first readings
of it, and they say something different about each row.

## Result

1. The surface. `daemon.status` returns `DaemonStatus.Timings`
   (`internal/daemon/status.go:61`, `:126-128`), one histogram per site with
   count, sum, max, overruns and 1-2-5 buckets from 1 ms to 30 s, cumulative
   since daemon start (no reset, no percentile field); `marvel describe
   daemon` prints it as JSON. Four record points exist: `reconcile.pass`
   (`internal/team/controller.go:580`, wrapping the controller mutex hold),
   `reconcile.missed-ticks`, `rpc.<method>` (`internal/daemon/daemon.go:1400-
   1411`, handler time including the get handler's wait on the controller
   mutex) and `tmux.<subcommand>` (`internal/tmux/driver.go:302-304`). No
   site measures the store or a lock wait.

2. The readings (count, mean, max, overruns; buckets as upper bound in ms:
   count).

   | daemon | site | count | mean ms | max ms | overruns |
   |---|---|---|---|---|---|
   | host A, 13 fed seats, up 5 h 26 m | reconcile.pass (budget 2000) | 9808 | 28.0 | 366 | 0 |
   | | tmux.list-panes | 9884 | 20.9 | 188 | 0 |
   | | rpc.get (budget 200) | 985 | 3.1 | 11.4 | 0 |
   | | rpc.heartbeat | 18194 | 8.9 | 104 | 0 |
   | | rpc.bus.beat | 7253 | 0.07 | 9.3 | 0 |
   | host B, 26 sessions | reconcile.pass | 8181 | 26.4 | 776 | 0 |
   | | tmux.list-panes | 8236 | 17.1 | 273 | 0 |
   | | rpc.get | 929 | 3.8 | 389 | 2 |
   | | rpc.heartbeat | 30856 | 12.0 | 380 | 0 |
   | | rpc.bus.beat | 12543 | 0.2 | | 0 |
   | | rpc.capture | | 85 | 769 | |
   | | rpc.shift | | 130 | 787 | |

   Host A buckets: reconcile.pass 10:560, 20:1364, 50:7814, 100:50, 200:10,
   500:10; rpc.get 1:121, 2:167, 5:634, 10:60, 20:3; rpc.heartbeat 5:21,
   10:15882, 20:2185, 50:104, 100:1, 200:1; rpc.bus.beat 1:7202, 2:1, 5:36,
   10:14. Missed ticks 0 on both; no `daemon.overrun` event on host A. Host B
   was read by a second reader on that host; host A by the review's area agent.
   Both reads were `marvel describe daemon`, read-only.

3. Row 1, the pass under one mutex. INFERRED from the figures: the pass runs
   every 2.0 s and holds the controller mutex for 1.3 to 1.4 percent of wall
   time on both hosts; one `tmux.list-panes` per pass (ReapDead's listing) is
   65 to 75 percent of the pass sum, so the hold is mostly tmux time. Host A
   could not show the read path waiting (about 0.2 expected overlaps of a get
   with a long pass); host B did: a `rpc.get` maximum of 389 ms and two
   overruns of its 200 ms budget beside a pass maximum of 776 ms is a get
   waiting behind the pass. The review's first step (a separate mutex for the
   role-health projection the read path takes) is supported; its second step
   (plan under the lock, act off it, record under it) is not demanded, since
   no pass crossed its 2 s budget, and the tmux share is the condition the
   review attached to it.

4. Row 2, the synced write per heartbeat. The bus beat and the heartbeat take
   the same lock and the same unconditional synced write
   (`UpdateSessionBusBeat`, `internal/api/store.go:1116-1130`;
   `UpdateSessionHeartbeat`, `store.go:961-1108`; both end in `persistPut`).
   The bus beat costs 0.07 and 0.2 ms; the heartbeat costs 8.9 and 12.0 ms,
   about 60 times more on host B. So the synced write is about 0.2 of the
   heartbeat's 12 ms, and the remaining cost is in the heartbeat's own path,
   which a static read does not place (no I/O beyond `persistPut`, the
   context resolver has no production caller, the same record is marshalled).
   The load is still material: INFERRED, heartbeat handler time, an upper
   bound on the store lock, was about 2.3 percent of wall time on host B,
   growing with fed seats. The review's remedy as written (note a dirty key, flush on the tick) would remove about
   0.2 ms per beat. A breakdown of the heartbeat path (timing sites around
   lock acquire, the field arithmetic, the encodes and the bolt transaction,
   or a profile) comes before that change.

5. Status of the structural review's 46 rows at the pin: 2 done, 4 part done,
   7 open with a ticket, 33 never ticketed, 0 obsolete; 10 rows grew without
   the finding changing. The October restructure plan is implemented in full
   (its ten work items and the macOS job), with two side items still open
   (reading the process table without exec'ing ps; the seed-env test through
   `kern.procargs2`). Its habit (a), a same-package file split when a handler
   is next edited, produced zero moves in 45 days while the three largest
   files grew by a net 282 lines.

## Limits

Static reading only for the code; the readings are cumulative since each
daemon's start, taken once, on hosts with 13 and 26 seats; neither is a loaded
fleet. The daemons ran a build newer than the pin. The host B uptime was not
recorded, so its wall-time shares assume one pass per 2 s. Every cost figure
marked INFERRED is arithmetic on the histogram, not a measurement of a cause.

## Bears on

- `question-marvel-service-provider-shape` (frontier). Back-reference text:
  "finding-marvel-8wdt (2026-11-25): the first I-451 readings put the
  reconciler's pass at about 27 ms mean on two daemons with 65 to 75 percent
  of the hold in tmux list-panes, and the heartbeat at about 60 times a bus
  beat on the same synced write. The backplane's meter function now has a
  first reading of its first citizen; the measurement fabric's dual (parts
  emit observations) is the surface these came through."
- `elem-agentic-resource-matrix` (bedrock). Back-reference text:
  "finding-marvel-8wdt (2026-11-25): the time row has its first measured
  per-site histograms (I-451 slice 1) and two readings; the controller mutex
  hold is 1.3 to 1.4 percent of wall time and heartbeat handler time, an upper
  bound on the store lock, about 0.8 to 2.3 percent (inferred), before any enforcement locus beyond spawn-time construction."
- `docs/design/code-restructure-plan.md`, status header, successor record:
  "The November 2026 status review found the plan implemented in full, two
  side items open, and habit (a) unexercised; see finding-marvel-8wdt for the
  readings that decide the held moves."
- `finding-marvel-4m2m` (the seams and their cost): the three large files
  grew by a net 282 lines after the planned moves, which this finding records
  as the habit's measured result.
