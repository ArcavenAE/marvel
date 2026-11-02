# finding-marvel-vdni: a host-vitals sampler can ride marvel's existing process-table pass at no new exec

**Date:** 2026-11-02 (revised the same day after review)
**Confidence:** frontier. These are code reads at one commit and one set of host readings. Nothing is built or measured under load.
**Design:** `docs/design/host-vitals.md`
**Placement:** marvel. The subject is a sampler inside marvel's daemon; director and critic are objects that consume its output.

## Why this matters

One fleet host degraded for weeks while nothing watched it, and the operator has asked for marvel to sample host vitals on every host it runs on. A sampler inside a daemon that is about to promise stability has to be cheap and side-effect free. Whether it can be decides when it should land.

## What was read

At marvel `8587651a`:

1. **The daemon already reads the whole process table.** `RunMetrics` runs every `MetricsInterval` (5 s, `internal/daemon/daemon.go`). On darwin, each pass runs `ps -A -o pid=,ppid=,pcpu=,rss=` once (`internal/procstat/read_darwin.go`, `readProcesses`). That is 12 execs a minute today, for marvel's own seat metrics.
2. **Metrics are memory-only by design.** `Store.UpdateSessionMetrics` (`internal/api/store.go`) does not persist, because a reading is stale once the daemon stops. A host sample has the same property.
3. **Identity by arguments is already possible.** `internal/procargs` reads a pid's arguments on darwin, and the bus broker uses it to prove a process before signalling it (#799). The orphan case can use it to match a reparented process to a seat or a test.
4. **OTel export from the daemon does not exist yet.** `internal/otel/metrics.go` has a stdout meter provider and one gauge, and only `cmd/simulator` uses them.
5. **marvel can publish to the bus.** `internal/bus` holds a NATS publisher for role-inbox envelopes, which is the shape a per-minute vitals envelope would take.
6. **The memory figures need no exec.** On the busiest host, `kern.memorystatus_level`, `kern.memorystatus_vm_pressure_level` and `vm.swapusage` all answer as plain sysctls, and those can be read in-process.

For scale, a root capture on the busiest host measured about 827 tmux client starts a minute (#1214); attributing them to the reconcile loop's per-pane `tmux list-panes` comes from reading the code on that issue, not from a measurement. A vitals read on every twelfth metrics pass, with three more `ps` columns, adds no exec while a seat is live.

7. **The metrics pass skips `ps` when no seat has a pid.** `SampleMetricsOnce` (`internal/daemon/metrics.go`) returns before sampling when no live session has a pid, and `Sampler.Sample` (`internal/procstat/procstat.go`) returns early for no roots. So the vitals read runs its own `ps` on a host with no seats: one a minute (design section 2.2).

## What it means

- The sampler is a read on a pass that already runs, not a new loop with its own cost. That bears on the initial-release party's 5-0 ruling against new always-on daemon loops before the stability promise. Director ruled the timing on 2026-11-02, and the operator may overrule: build before the rc, off unless the cluster config enables it (`docs/design/host-vitals.md` section 9).
- Keeping samples out of bolt costs nothing in durability and keeps the sampler out of #1214's write volume.
- The orphan case is the only part that needs state across passes (the previous parent map). Its markers (`MARVEL_SESSION`, test socket and home prefixes) are named but not yet measured as reliable.

## Limits

1. Code reads at one commit; nothing was run against a daemon.
2. The sysctl names were checked on one darwin host. The linux sources (`/proc/meminfo`, `/proc/pressure/memory`) were not read on a fleet host.
3. smartctl output on these hosts was not read. Whether it can read the boot disk without extra rights is open.
4. The incident timeline in the design comes from that host's own diagnostics as relayed, not from this probe.
