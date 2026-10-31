# Round 2 input: a second host's draft on host memory exhaustion

Summary by the chair of a draft by a research-supervisor seat on another fleet host (2026-10-31, read-only; sender's sha256 3099846b...8f30, 4747 bytes, relayed by the arcaven supervisor and not re-hashed). Readings are from about 16:30 to 17:20Z on that host. The draft's own recommendation is valid until 2026-11-07.

**Answer it gives:** a report-only host watcher plus two narrow guards. Nothing kills on its own.

1. **Watch five signals every 5 minutes** (no root): free memory percent (`memory_pressure`), swap used and total (`sysctl vm.swapusage`), the largest processes (`ps -axo pid,ppid,lstart,rss,comm`), the file-event daemon's RSS slope over 24 h, and orphaned test brokers (`nats-server` with parent pid 1 and a config under a temp directory).
2. **Detection rules, declared constants, report only:** one process over 25% of physical memory or growing over 1 GB in 10 minutes; free memory under 15% for three readings; swap over half of total where swap exists; the file-event daemon growing over 1 GB a day; any orphaned test broker older than 1 hour. Each emits one `host.memory.warning` event (rule, pid, start time, resolved path, reading) to the events ring and to the host's supervisor. Thresholds stay diagnostic; nothing blocks a merge or a spawn.
3. **Cleanup by identity, never by pattern:** one command shaped like `marvel reap --homes` (#1157). Dry run by default lists pid, ppid, start time, resolved path, RSS and the rule. `--confirm` re-reads each pid and sends one SIGTERM only when start time and path still match. A pattern kill missed on the other host because the running argv was the interpreter's resolved path, not the command typed.
4. **Fix the sources:** each test that starts a `nats-server` registers its pid with `t.Cleanup` and a package `TestMain` sweeps its recorded pids (the live orphan there names `TestAnUnawareSuccessorFiresTheDroppedSignalAndStatusSaysSo` in its config path). An unbounded pagination loop belongs to another tool, not marvel.
5. **Temp churn does not explain it alone:** that host holds 339 test scratch dirs and 1039 temp entries with a 7.5 MB file-event daemon after 48 days, so the busiest host's growth (about 2.5 GB a day) has another driver, unproven.

**Questions it leaves for a ruling:**
- A spawn cap on low memory: recommended no gate now; report the reading in the spawn event and decide after a week of readings.
- Where the watcher lives: recommended in the marvel daemon, as a ticker beside the watchdog; a host without marvel runs the same checks as a script.

**Busiest-host context (supervisor, 21:55Z):** its file-event daemon was flat for the last hour at 27.4 GiB after the earlier growth. A 30 s root trace put git (harness-internal status polling) first, then the harness and host-installed agents outside marvel.
