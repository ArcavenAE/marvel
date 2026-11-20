# The missing `get` reads: schedules and the kinds no verb shows

- **Status:** idea (pre-hypothesis, no commitment). A design for it exists as a recommendation; nothing is built and no verb is chosen.
- **Date:** 2026-11-19
- **Origin:** the operator's request, carried by commission. The operator asked, verbatim: "kos idea marvel we have marvel get budgets but where is the marvel get schedule(s) and so forth? what else are we missing? route to 4ptdd for analysis and to flesh out what we're missing and how to design it"
- **Subject:** marvel (the `get`, `describe` and `-w` read surface). Filed in the marvel graph per the subject test.
- **Related:** [[get-views-connection-and-service-context]] (decorating the `get` views; the same surface), [[multicluster-view]] (reading several clusters at once, which this idea leaves to a ruling), [[token-rate-column-and-configurable-columns]] (the configurable-columns substrate a new table rides). docs/design/cli-read-back-and-run-parity.md (an earlier ranked gap list, checked at e056128), docs/design/scheduled-runs.md (the schedule design and its describe team block), and the frontier node question-multicluster-view.

## The intent

The operator can list budgets but not schedules, and wants to know what else the read surface lacks and how to design it. The point is a fleet operator who runs `marvel get` all day and wants to see when a scheduled role fires next without reading a manifest or a JSON dump.

## What the code shows (at 554036cb)

- marvel stores each schedule's next due time and its jittered fire time (internal/api/schedule_status.go:125 to 126), the current firing, the run history, and the stale flag. No `get` reaches any of it. `describe team` prints a schedule block as JSON with the nominal due time only (internal/daemon/schedule_describe.go:144), which is off by the jitter from when the run starts (internal/team/schedule_clock.go:236).
- `get` prints tables for seven kinds (cmd/marvel/main.go:1512 to 1524); the daemon answers two more, `register` and `team-load`, which the CLI prints as raw JSON (main.go:1527).
- `-o` works for team, workspace and session only (cmd/marvel/getoutput.go:138), and `-w` watches sessions whatever kind is named (main.go:1415).
- `describe` answers five kinds, singular names only (internal/daemon/daemon.go:1839 to 1867).
- Cadence slots, role restart counts and the daemon's "still starting" flag have no table anywhere.

## The design, in short

A four-seat party (a fleet operator, an architect, a CLI designer and a distributed-systems seat) worked it over three rounds and a forced vote. The recommendation, for the operator to rule on:

1. One kind table that drives both `get` switches, the help and the unknown-kind error, so every kind the daemon answers has a printer and a place in help. Today `register` and `team-load` print as raw JSON and help names neither; `register` gets a table, and team load stays `get teams --load`.
2. `get schedules`, one row per declared scheduled role: WORKSPACE, TEAM, ROLE, CRON, TZ, STATE, NEXT-FIRE, LAST-RUN, OUTCOME, LAST-OK, STALE. NEXT-FIRE is the jittered fire time. STATE is a closed list: `starting`, `paused`, `suspended`, `frozen`, `retrying`, `firing`, `late`, `waiting`, `pending`. Firings are rows inside a schedule, not a kind of their own.
3. `-o json|yaml` for every kind, and plural names for `describe`.
4. `describe schedule <ws>/<team>/<role>` with the run history and the retry fields no read shows today.
5. `get cadences` on the same join.
6. Smaller: restart and backoff columns on `get teams`, a notice when a single-cluster read comes from a daemon still starting, and help that says `-w` polls.

No read is proposed for kinds with no code (packs, leases, casts), in line with ADR-003's caution against building every kind at once.

The design record (inventory with file:line per cell, the party record, a ticket plan with red tests, and two decision cards) is kept with the operator's decision records, dated 2026-11-19.

## Open questions

- Should every `get` read across clusters, with an outcome row per cluster, or only sessions for now? The party voted 3 to 1 for sessions only; the multi-cluster question is the operator's.
- Which of the reads get built, and in what order? The party recommends the kind table first.
- Does a `late` state earn its place next to STALE? One seat holds that STALE already alarms and that a one-tick threshold may flicker.
