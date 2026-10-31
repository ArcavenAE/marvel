# Host operator (SRE), round 3

I accept C8 as should (4 to 1 in round 2; the tag does not wait on it), and argue only that it lands in the rc window because it is small. Read-only on fa228925.

## C8. Test homes leak on normal runs

- **Fix.** In `internal/homeguard/homeguard.go` `RunWith`, before `m.Run()`: set `GOMODCACHE` to the module cache resolved from the real HOME (read once with `go env GOMODCACHE` before HOME is replaced), and `GOFLAGS=-modcacherw` as a second line of defense. Replace `_ = os.RemoveAll(home)` with a removal whose error is printed and turns exit 0 into 1. No test body changes; the guard's other checks stay as they are.
- **Failing test first.** `TestRunWithRemovesTheScratchHomeAfterAGoSubprocess` in `homeguard_test.go`: it runs a child test binary (the package's own, re-exec'd with a flag) whose one test writes a read-only directory tree under `$HOME/go/pkg/mod`, the way `go` does. The parent asserts that the child exits 0 and that the scratch HOME the child printed no longer exists. On fa228925 the directory survives and the child still exits 0, because the error is dropped.
- **Second test.** `TestRunWithFailsWhenTheScratchHomeCannotBeRemoved`: same child, with `-modcacherw` forced off. It asserts that a failed removal makes the child exit nonzero and print `homeguard: remove scratch home`. It fails today because the exit code ignores the removal.
- **Ticket.** "homeguard: point Go's module cache outside the scratch HOME and fail the binary when the scratch HOME cannot be removed". No edges; it lands any time before the rc.

## C9. Test layouts fall back to `/tmp/marvel-h-*`

- **Fix.** Make the scratch HOME short enough that the durable base fits, so no test layout needs the `/tmp` fallback: homeguard makes HOME with `os.MkdirTemp("/tmp", "mth-")` instead of under TMPDIR. That is about 18 bytes in place of 76, well inside `MaxUnixSocketPath` (`internal/paths/paths.go:218`). `scratchRoots` gains that directory, so the guard's path checks still pass. A test that builds a Manager on a `t.TempDir()` layout uses `shorttmp.Dir(t)` (`internal/shorttmp/shorttmp.go:26`) for the same reason. The production fallback chain (`internal/session/manager.go:280-291`) does not change.
- **Failing test first.** `TestScratchLayoutUsesTheDurableHarnessBase` in `internal/session`: under homeguard's HOME it calls `harnessHomeDirs()` and asserts the returned dir is under `$HOME/.marvel/h`, not `/tmp/marvel-h-`. It fails on fa228925 because the 142-byte path the other seats computed is over 104.
- **Package check.** A `TestMain` helper, `homeguard.CountTempBases`, counts `/tmp/marvel-h-*` before and after `m.Run()` and fails the binary if the count grew. It is a structural check on the test run (is the run well-formed), not a health metric, so it may fail a test. It fails today in every package that builds a Manager. It reads names only and removes nothing.
- **Ticket.** "tests: give test layouts a home short enough for the durable harness base, and fail a package that leaves a /tmp/marvel-h base". Edge: it shares `homeguard.go` with C8, so it is one chain, C8 then C9 (rule d), not siblings.

## C10. Orphaned codex app-servers are invisible

- **Fix.** `marvel reap --homes` already reads codex's pid file inside a home (`internal/session/home_sweep.go:45-48`) but only for this layout's bases, and it counts other layouts read-only (`:88-92`). It gains one listed section: for other layouts' bases, each `app-server-daemon/daemon.pid` whose pid is live and whose process start time is after the pid file's mtime is listed with pid, ppid, start time, resolved executable path and home. It stays listing only: `--confirm` does not signal another layout's daemon. `marvel orphans` gets one help line saying it lists stale-token holders only, and points to `reap --homes` for helper processes.
- **Failing test first.** `TestSweepHomesListsALiveDaemonUnderAnotherLayoutsBase` with `tempRoot` set: it makes another layout's base and a home with a pid file naming a live child process the test started (a `sleep`). It asserts that the listing names that pid with its start time and path, that `--confirm` leaves it running, and that the test kills its own child in `t.Cleanup`. It fails today because other layouts are counted, not listed.
- **Identity rule (from the draft, item 3).** Any later verb that signals a listed process re-reads pid, start time and resolved path at the moment of the signal, and sends one TERM only when all three still match. Pattern matching on names is never a selector.
- **Ticket.** "reap --homes: list live codex daemons under other layouts' bases, and say what marvel orphans covers". Edge: after C9, since C9 removes most new other-layout bases and the listing then shows the old ones only.

## C11. Test brokers and tmux servers outlive a run

- **Fix.** One package helper, used by every test that starts a `nats-server` (the `internal/bus` tests and `internal/daemon` reexec e2e): it records the broker pid the moment it starts, registers `t.Cleanup` to TERM then KILL that pid (by pid, never by name), and before the step that leaves a broker for adoption, not after (#1162's mechanism). `TestMain` in those packages kills any recorded pid still alive after `m.Run()`. For tmux, the same shape: `tmuxtest` records each server name it starts and kills it in `TestMain` (#463), and `SweepDead` gets a per-server time limit (#805).
- **Failing test first.** `TestAdoptionHandoffStopsTheBrokerWhenTheSecondDaemonFailsToStart` in `internal/daemon`: it injects a failure into the second daemon's `attachServices` and asserts, after the subtest's cleanup, that the broker pid is gone (`kill -0` fails). It fails today because the cleanup is registered only after `attachServices` succeeds.
- **Ticket.** "tests: every test that starts a nats-server or tmux server stops it by recorded pid on every exit path". Closes #1162 and #463 once landed; #884 (a killed binary) is narrowed by the `TestMain` sweep but not closed, since a SIGKILL'd binary runs nothing. No edges.

## C12b. A read-only report of marvel's own footprint

- **Fix.** No new verb: `marvel reap --homes` already reports homes, bases and codex daemons. Its listing gains a short summary block: this layout's homes and bases with counts, other layouts' bases and homes, live helper daemons (C10), brokers marvel started and their pids (from the bus supervisor's pid file), and the oldest running seat with its age and harness build. It is a read, it never fails on a count, and it carries no threshold.
- **Failing test first.** `TestSweepHomesDryRunReportsTheFootprintSummary`: a scratch layout with two homes, one other-layout base and a recorded broker pid. It asserts the summary's counts and that the dry run changed nothing on disk (a metadata hash before and after). It fails today because the summary does not exist.
- **Ticket.** "reap --homes: add a footprint summary of what marvel holds on this host". Edge: after C10, which adds the daemon rows the summary counts. Not in the rc unless C10 lands first; should.

## Order for the rc (my items only, none gates the tag)

- C8 then C9 (one chain on `homeguard.go`), then C10, then C12b.
- C11 stands alone and can land in parallel with the C8 chain, since it touches different files.
- None of these blocks C1; all of them should be merged before `v0.1.0-rc.1` is cut, so the rc's own CI run is the first clean one.
