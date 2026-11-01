# Host operator (SRE), round 1

Lens: what marvel leaves on a host, and whether marvel can see it. Read-only throughout; nothing was killed, removed or reaped. Tree: fa228925 (`git rev-parse --short=8 HEAD`).

My headline: most of the temp-tree churn in inputs.md section 1 is not leaked seats. It is the test suite, on every normal run, and the cause is one discarded error.

## Items

1. **blocker. homeguard cannot delete its own scratch HOME.** `internal/homeguard/homeguard.go:46-51` makes `marvel-test-home-*` and defers `_ = os.RemoveAll(home)`. Tests that shell out to `go build` or `go list` (8 call sites, `grep -rn 'exec.Command("go"' --include='*_test.go'`, e.g. `cmd/rolescheck/binary_test.go:20`, `internal/daemon/reexec_target_test.go:51`) fill `$HOME/go/pkg/mod`, which Go writes read-only. RemoveAll then fails and the error is dropped. Check: of 695 leaked dirs on this host, 681 contain `go/` (loop with `[[ -d $d/go ]]`); the largest hold about 1,900 entries, 1801 of them under `go/pkg`, and all but ten not user-writable (`find ! -perm -u+w`). So the leak is the normal path, not killed runs. It also means a test run may fetch modules into a throwaway cache each time.
   - Fix shape: in `RunWith` setup, point `GOMODCACHE` at the real cache (or set `GOFLAGS=-modcacherw`), and treat a failed RemoveAll as a test failure, not `_`.
2. **blocker. Test layouts leak `/tmp/marvel-h-<tag>` bases.** 1531 bases, 1337 of them empty (`ls -A` loop). The base is `filepath.Join("/tmp", "marvel-h-"+tag)` (`internal/session/manager.go:298-303`), and each scratch HOME is a new tag. Inferred, not checked: the durable base under a long scratch HOME fails the socket-length test, so tests fall to the `/tmp` base. `marvel reap --empty-bases` (#1160) is an operator sweep for a leak marvel keeps making.
   - Fix shape: tests set the Manager's `tempRoot` inside `t.TempDir()`; a test helper fails if `/tmp/marvel-h-*` grew during the package run.
3. **should. Test brokers and tmux servers outlive a run.** #884 (killed run), #1162 (two reexec e2e tests), #463 and #805 (tmux). All open in the tsv. No live leaked broker on this host now (`ps` shows one nats-server, parent 1, 15 days, which belongs to another component), but another host had one today.
4. **blocker. Orphans of marvel's own seats are invisible.** #186 (agent from a previous daemon heartbeats forever) is open. 11 codex app-server processes with parent 1 are live here now (`ps -axo ppid,command`). #1159 reaps a home and daemon when a seat is deleted from now on; it does nothing for what is already orphaned, and `SweepHomes` runs only on an operator RPC (`internal/daemon/daemon.go:2928`). #871 (stale heartbeat token files at start) is the same class.
   - Fix shape: at daemon start, list (never kill) orphans and stale homes as one event and in `describe`; removal stays `reap --confirm`.
5. **should. A footprint read, not a host watch.** One read verb (or a section of an existing one) that reports what marvel itself holds: homes, bases, codex daemons, brokers it started, file counts under its bases, oldest seat and its harness version. A declared bound may raise an event. It never gates and never acts (`diagnostic-not-gate`). This would have shown items 1, 2 and 4 weeks ago.
6. **should. Seat worktrees accumulate (#394).** marvel launches the builder roles that make worktrees, and nothing ends them. The orc root here has about 280 of them, and harness git polling over that tree led the root trace in inputs.md. marvel owns the role shape, so it should own "a seat's worktree is removed when the seat's work merges", or say in the role model that it does not.
7. **after-release. A host-health watcher in the daemon.** Swap, memory pressure and file-event load are host facts. Inside the daemon they add a sampler to the one process that must not hang, and they invite remediation by pattern. Revisit after item 5 shows what marvel's own share is.
8. **after-release. Host sleep and wake (#866).** Real, but no part of today's incident.

## Must NOT be in scope

Any marvel action on a process or path it did not create and record: no kill by name, no sweep by glob outside its own bases, no "free memory" remediation. Cleanup by pattern caused the fear that made this party; a release that automates it turns a manual hazard into a standing one.

## What a release promises (host view)

A stable marvel leaves behind only what it recorded, can list all of it, and removes it on request. Its own test suite holds to the same rule, because the people who run the tests are the first adopters.

## Marvel's to fix, and not

- marvel's: items 1, 2 and 3 (its test suite), item 4 (its seats' orphans), item 5 (seeing its own footprint), and the worktree contract in item 6.
- not marvel's: the file-event daemon's memory and CPU (OS); git polling by the harness over a large tree (harness and repo layout); the graphical session loss and the keychain logouts that followed (OS and host); host-installed monitoring agents; how old a seat may run and which harness build it runs (operator policy, already expressible as max-age).
