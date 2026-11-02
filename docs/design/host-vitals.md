# Host vitals: a per-host sampler in marvel

Proposal, 2026-11-02. Code read at marvel `8587651a`. Nothing here is built.

- **Author:** the architect seat, team arcaven.
- **Asked by:** the operator, relayed by director, 2026-11-02.
- **Builds on:** `docs/design/host-health-guard.md` (#1166), whose section 4.1 placed a host ticker beside `RunMetrics` after v0.1. The reasoning and the code reads are in `_kos/findings/finding-marvel-vdni-host-vitals-sampler.md`.
- **Status:** for review. Section 9's decision was ruled by director on 2026-11-02; the operator may overrule.

## 0. Why

On one fleet host, the macOS file-event daemon (fseventsd) grew for weeks: about 14 GB resident on 2026-10-06, an 86 GB footprint when the graphical session crashed on 2026-10-30, and still 27 to 29 GB days later. Nothing noticed. A one-off 54 GB job then pushed that host into swap exhaustion, and the only warning was a low-memory panel on a screen nobody was watching. Separately, four stuck tmux clients spun at 100% CPU for three hours (the #805 class). The other two fleet hosts read flat over the same weeks (fseventsd 8 to 11 MB), so this is a per-host problem, and only a sampler on each host sees it.

marvel already runs on every fleet host. It already reads the whole process table every 5 seconds to price its own seats. A host reading from the same pass costs it no new process, and gives each host the early warning that host lacked.

## 1. The split

This is the direction the operator approved:

| part | owner | does |
|---|---|---|
| sample | marvel, on every host it runs on | reads the vitals once a minute, serves them, emits them |
| consume and decide | director | receives the events and decides what reaches the operator. director#470 is reworked from sampling to consuming. |
| trend | critic, from outside | growth per day and wear per week, across hosts and over weeks |

marvel never decides what is worth a person's attention, and director never reads a host's process table. Each part is useful without the others (section 6).

## 2. What to sample, once a minute

| vital | source | notes |
|---|---|---|
| swap used and total | `vm.swapusage` by in-process sysctl (linux: `/proc/meminfo`) | no exec |
| memory pressure | `kern.memorystatus_vm_pressure_level` and `kern.memorystatus_level` by in-process sysctl, the figures behind `memory_pressure` (linux: `/proc/pressure/memory` where present) | no exec |
| the top processes by RSS | the `ps` pass `RunMetrics` already runs (`internal/procstat`), widened | top 10, with pid, parent, elapsed time, CPU time and command name |
| fseventsd RSS | the same rows, by command name | darwin only; a GAP line on linux |
| orphaned CPU | the same rows, across passes | section 2.1 |
| SSD wear, weekly | `smartctl -a` on the boot disk, once a week: Percentage Used and Data Units Written | a GAP line when smartmontools is absent; it never installs it |

### 2.1 The orphan case

The target is a process whose parent is now launchd (parent pid 1) and whose ancestry was a marvel seat or a marvel test, such as the #805 tmux clients. A process loses its ancestry when it is reparented, so the sampler keeps it from earlier passes:

1. Each pass keeps the parent map of the pass before.
2. A process that was under a seat's pane tree or a test process last pass and has parent pid 1 now is marked as an orphan, with its pid, start time and resolved path (the identity of `host-health-guard.md` 4.3).
3. A process already orphaned before the daemon started is matched by its arguments or environment through `internal/procargs`: a `MARVEL_SESSION` variable, or a marvel test socket or home prefix. Which markers are reliable for each case is to be measured before the build.
4. The vital is CPU-hours since it was marked, from the `ps` CPU-time column, and the share of a core over the last 30 minutes.

### 2.2 Cost budget

The budget is one process-table read a minute. The design spends less than that.

- `RunMetrics` already runs `ps -A -o pid=,ppid=,pcpu=,rss=` once per pass, every 5 s (`internal/procstat/read_darwin.go`, `MetricsInterval` in `internal/daemon/daemon.go`): 12 execs a minute today.
- The vitals read takes every twelfth pass and adds three columns to the same call (elapsed time, CPU time, command name). That is no new exec, and a few tens of KB more output once a minute on a host with about 900 processes.
- Swap and pressure are sysctl calls in the daemon's own process. smartctl is one exec a week.
- For scale: on the busiest host, the reconcile loop's per-pane `tmux list-panes` was measured at about 827 execs a minute (#1214). A standalone sampler at one `ps` a minute would add about 0.12% to that. Riding the existing pass adds nothing.

## 3. Output

- **A bus event.** Once a minute, one small `host.vitals` envelope per host: the readings, and the thresholds over their line. It goes through marvel's existing bus publisher (`internal/bus`) where a bus is configured. director#470 names the subject it consumes.
- **marvel's own events ring.** Only crossings, not every sample: `host.vitals.crossed` when a threshold is first passed, and `host.vitals.cleared` when it is no longer passed. `marvel events` shows them without a bus.
- **OTel metrics, where enabled.** One gauge per vital (`marvel.host.swap_used_bytes`, `marvel.host.memory_pressure_level`, `marvel.host.process_rss_bytes` for the top processes, `marvel.host.orphan_cpu_seconds`, `marvel.host.ssd_percentage_used`), per SOUL section 6: always available, never mandatory. marvel's OTel package today has one gauge, used only by the simulator (`internal/otel/metrics.go`), so exporting from the daemon is new work.
- **`marvel get vitals`.** The latest sample, its time, each threshold with its state, and each GAP line. `-o json` for scripts. It reads daemon memory only; the samples are not written to bolt (section 7).

## 4. Thresholds (provisional)

Each threshold below is a proposal, re-checked on 2026-11-16 against the first two weeks of samples from all three hosts. Each is diagnostic, not a gate: a crossing is reported, and it blocks nothing, whether a spawn, a merge or a commit (SOUL section 8, ADR-007, and the fleet's diagnostic-not-gate rule). Constants are declared, never tuned by marvel.

| vital | crosses at | why this line |
|---|---|---|
| swap used | over 50% of total | the busiest host sat at 98% for days; 50% leaves time to act |
| fseventsd RSS | over 4 GB | the flat hosts read 8 to 11 MB, so 4 GB is far outside normal and far below the 14 GB the busiest host held four weeks before its crash |
| any process RSS | over 20 GB | the one-off job reached 54 GB; this host has 128 GB |
| an orphan's CPU | over 90% of a core for more than 30 minutes | the #805 clients spun for three hours |
| SSD wear | Percentage Used up 1 point or more in a week | a starting line, since no wear figure for these hosts is recorded yet; the first readings set the baseline |

## 5. No remediation

marvel never kills, restarts or signals a process because of a vital. A crossing proposes; the operator, or a seat holding a grant for that action, acts. This keeps `host-health-guard.md` HH-1 (a health reading is report-only) and HH-3 (marvel signals only what it created and recorded). The listing for an orphan carries the four identity values, so whoever acts does so by pid, never by pattern.

## 6. Independence

- **marvel without director:** it still samples, still serves `marvel get vitals`, and still writes crossings to its events ring. With no bus configured it skips the envelope and says so once in its log.
- **director without marvel:** a host with no marvel has no vitals. director shows a GAP line for that host, never a silent absence.
- **critic without either:** it trends whatever records it is given, and reports a GAP for a host or a day with none.

## 7. What it does not do

- It writes no sample to bolt. Readings are stale the moment they are taken, the precedent `UpdateSessionMetrics` already follows (`internal/api/store.go`). This also keeps it out of the write volume #1214 is reducing.
- It reads no file contents and does not walk a file tree.
- It needs no root. Only `smartctl` may need extra rights on some disks, and when it cannot read, that is a GAP line.
- It replaces the interim report-only host script that `host-health-guard.md` 4.1 put outside marvel for v0.1, once this lands. Until then both may run.

## 8. What it would have caught

Using the incident timeline, in UTC:

| problem | threshold | when it would have fired | lead time before harm |
|---|---|---|---|
| fseventsd growth | over 4 GB | by 2026-10-06 at the latest (14 GB that day; earlier readings do not exist) | at least 24 days before the 2026-10-31 04:53Z crash |
| the 54 GB job | any process over 20 GB | at the first minute it passed 20 GB, between its start at about 2026-10-30 21:56Z and the crash; its growth was not recorded, so the minute cannot be named | up to about 7 hours |
| swap exhaustion | swap over 50% | likely before the first low-memory panel at 2026-10-31 03:21Z, since the panel means swap was near full; no swap reading survives from that night, so the minute cannot be named | likely more than the 92 minutes from the panel to the crash |
| the stuck tmux clients | orphan over 90% for 30 minutes | 30 minutes into the spin | about 2.5 of the 3 hours |
| SSD wear | up 1 point a week | not dated: no wear reading exists | none known |

In each case the event reaches director, and director decides whether the operator is told. On 2026-10-30 that is the difference between a decision made in time and a crash found the next morning.

## 9. Operator decision

The initial-release party ruled 5-0 against any new always-on loop in the daemon before the stability promise, and `host-health-guard.md` kept that ruling. The operator's direction puts the sampler in marvel now. This design adds no loop of its own: it rides the `RunMetrics` pass that already runs. It is still new daemon behavior before the stability promise.

- **Recommended:** build it before the rc, off unless the cluster config turns it on, and turn it on for the three fleet hosts. It adds no exec and no bolt write, so the risk the party guarded against (a new loop's cost and failure modes in a daemon about to promise stability) is small. Being off by default keeps every other installation unchanged.
- **The alternative:** build it after v0.1, as `host-health-guard.md` 4.1 planned, and keep the interim host script until then.

**Ruled by director, 2026-11-02, on the operator's ask; the operator may overrule.** Director, word for word, with host names edited to roles: "build before the rc, OFF unless the cluster config enables it, and enabled on [the three fleet hosts]. This is the note's recommendation and it fits the release party's vote: it adds no new always-on loop. It rides the existing RunMetrics ps every 12th pass with zero new execs, and it is off by default for everyone else."

The recommendation above was valid until 2026-11-09; with the ruling recorded, it stands as ruled.

## 10. Candidate requirements (provisional)

- **HV-1** (provisional; source: incident, the operator's direction). marvel samples host vitals once a minute on every host where the cluster config turns it on, with no new process exec beyond the pass `RunMetrics` already runs, and one weekly `smartctl`.
- **HV-2** (provisional; source: HH-1, HH-3). A vital never causes marvel to signal, kill or restart a process.
- **HV-3** (provisional; source: SOUL section 8). Every threshold is a declared constant and a diagnostic; none gates anything.
- **HV-4** (provisional; source: SOUL section 2). With no director, the sampler and `marvel get vitals` work unchanged. With no marvel, director shows a GAP for that host.
- **HV-5** (provisional; source: #1214). A vitals sample is held in memory and never written to the state database.
