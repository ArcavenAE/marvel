# finding-marvel-4m2m: the three largest files have clean seams, but the measured cost is in parallel waves and a duplicated supervisor, not in the file layout

Date: 2026-10-09
Status: frontier
Brief: `_kos/probes/brief-code-seams.md`
Supports: question-marvel-service-provider-shape
Cites: `docs/merge-conflict-reflection-2026-09.md`, `docs/design/services-list.md` section 3, `docs/design/service-provider/02-architecture.md` ("The backplane"), bd `aae-orc-oo62t`

## Why this matters

The operator asked where marvel's largest files split, which splits fit the services backplane and the supervisor extraction, and what the current layout costs. A restructure is expensive to review and easy to start for the wrong reason. I measured the cost first.

The answer in three lines:

- **The seams are real.** Each file breaks into four to eight contiguous concerns. One of them, the client half of `daemon.go`, has almost no coupling to the daemon and is a package-sized seam on its own.
- **The layout's conflict cost is small once PR size is held equal.** Most of the excess is large, multi-layer PRs, and one feature landed as five parallel PRs on one day carries four of the nine conflicts that name `daemon.go`.
- **The cost that matters for the backplane is a duplicated supervisor.** The restart backoff is implemented twice with the same logic, and moving the bus supervisor's lifecycle into `workload.Process` stalled because tests reach private fields.

Tags:
- **MEASURED:** a command run at `7f1c81e`, or against GitHub PR history, named beside the number.
- **CODE:** read in the tree at `7f1c81e`.
- **INFERRED:** reasoning, not observed.

All measurements were made at marvel `origin/main` `7f1c81e` (2026-10-08T22:30:53-05:00), read-only, in a worktree. The window is 2026-09-08T00:00Z to 2026-10-09T00:00Z (31 days) for commits, and `merged:2026-09-08..2026-10-08` for PRs.

## 1. The counts, re-measured

Director's figures were about 3938, 2600 and 1817 lines and 35 internal packages.

- **MEASURED** (`wc -l`): `daemon.go` 3883, `controller.go` 2600, `manager.go` 1817.
- **MEASURED** (`go list ./internal/... | wc -l`): 38 internal packages. There are 33 top-level directories under `internal/`, and `runtime/claudecode`, `runtime/codex`, `runtime/events`, `runtime/opencode` and `tmux/tmuxtest` are nested packages.
- **MEASURED**, growth (`git show $(git rev-list -1 --before=<date>T23:59:59-05:00 origin/main):<file> | wc -l`, the last commit on main before the end of each date in UTC-5):

| date | daemon.go | controller.go | manager.go |
|---|---|---|---|
| 2026-08-01 | 1562 | 1082 | 803 |
| 2026-09-01 | 2170 | 2073 | 1072 |
| 2026-09-15 | 2477 | 2152 | 1320 |
| 2026-10-01 | 2998 | 2330 | 1564 |
| 2026-10-08 | 3883 | 2600 | 1817 |

`daemon.go` grew 885 lines in the last week alone. The 2026-09 reflection measured it at 2477 and proposed a same-package split of its handler bodies; that split was not done.

## 2. The package graph

**MEASURED** (`go list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}' ./internal/... ./cmd/...`, internal edges only):

- **No cycles, and one wide importer.** `internal/daemon` imports 23 internal packages. The next widest are `cmd/marvel` (17), `session` (8), `usage` (7), `bus` (6), `runtime` (6), and `team` and `limitact` (4 each).
- **One hub.** `internal/api` is imported by 16 packages and imports only `asof`. `events` is imported by 10, `paths` by 7 and `runtime/events` by 6.
- **`daemon` is a leaf in the other direction.** Only `cmd/marvel`, `cmd/simulator` and `internal/simulator` import it.
- **`team` imports `session`; `session` does not import `team`.** The layering daemon, then team, then session, then runtime and tmux holds without exceptions.
- **`toolchain` and `gittest` have no non-test internal importers** (`gittest` is a test helper). `toolchain` holds no non-test file at the top level.

**INFERRED:** the graph is not the problem. The width is concentrated in one package whose job is composition.

## 3. The seams

**MEASURED**, a `go/ast` walk listing every function with its line span, grouped by contiguous concern:

**`internal/daemon/daemon.go`** (117 functions; `Daemon` has 36 fields):

| lines | size | functions | concern |
|---|---|---|---|
| 299-921 | 623 | 18 | lifecycle: `New`, `NewWithOptions`, `Start`, `shutdown`, reexec, pidfile |
| 922-1192 | 271 | 10 | RPC dispatch and caller scope (`dispatchAs`, the verb switch) |
| 1193-2256 | 1064 | 19 | resource handlers: logs, events, apply, get, describe, delete, scale, converge, heartbeat, run, shift, reset-health |
| 2257-2621 | 365 | 13 | inject, composer reading, capture |
| 2622-2898 | 277 | 9 | stop, reap, orphans and reexec handlers |
| 2899-3303 | 405 | 20 | **client**: `SendRequest`, `WatchEventsWith`, the `mrvl://` and SSH dialers, the SSH client config |
| 3304-3657 | 354 | 14 | services and bus attach, leaf seed |
| 3658-3883 | 226 | 11 | inject bookkeeping and refusals |

The client block is the cleanest seam in the three files.

- **MEASURED** (`awk 'NR>=2899 && NR<=3303' internal/daemon/daemon.go | grep -cE '\bd\.[a-zA-Z]'`): one match, and it is in a comment. No function in the block uses the `Daemon` receiver.
- **MEASURED** (`grep -rlw` per function): its callers are five files in `cmd/marvel`, `cmd/simulator/main.go` and `internal/simulator/lua.go`, seven files in all.
- **INFERRED:** it is client code that lives in the server package because the request and response types do. Moving it to its own package would let `cmd/marvel` stop importing the daemon package for client calls.

**`internal/team/controller.go`** (62 functions; `Controller` has 21 fields): role health store, 171-494 (324 lines); reconcile, plan and apply, 495-1423 (929); health evaluation and restart policy, 1424-1875 (452); shift lifecycle, 1876-2549 (674); run loop and schedule hold, 2550-2600 (51).

**`internal/session/manager.go`** (53 functions; `Manager` has 20 fields): paths and homes, 112-272 (161); adopt and reconcile tmux state, 273-561 (289); create and launch, 562-1038 (477); usage and instances, 1039-1337 (299); delete, reap and cleanup, 1364-1814 (451).

The field counts come from `awk '/^type <T> struct/,/^}/' <file> | grep -cE '^\s+[a-zA-Z_]+ '` and include embedded lines, so they are approximate.

## 4. Fit with the backplane and the supervisor gate

The backplane is four functions generalized from "a tmux session" to "any part": register, route, supervise and meter (`02-architecture.md`, "The backplane"). The seams map onto them:

| backplane function | where it lives today |
|---|---|
| register | `manager.go` create, launch and adopt; `daemon.go` apply |
| route | `daemon.go` dispatch, inject, capture |
| supervise | `controller.go` role health, restart policy, shift; `bus.Supervisor` |
| meter | `manager.go` usage and instances; `internal/admission` |

**The supervise function exists twice.**

- **MEASURED** (`sed -n '183,192p' internal/team/controller.go`, `sed -n '190,200p' internal/bus/supervisor.go`): `computeBackoff` has the same logic in both packages: the two print 10 and 11 lines and differ only by a comment and a blank line.
- **CODE:** `bus.Supervisor` keeps its own watch, crash, restart, terminate and stop (`supervisor.go:400-595`).
- **The supervisor extraction gate is half met.** marvel#351 (merged 2026-09-25, `4ef7222`) added `internal/workload` and the allowlisted child environment, and `bus.Supervisor` now spawns through `workload.Start` (`supervisor.go:316`). That closed the environment hazard in services-list.md section 3.2.
- The gate's structural claim is not met. Section 3.5 says that after the extraction "`spawnLocked` no longer exists". It does exist (`supervisor.go:306`), and the lifecycle (watch, backoff, adopt, `Restart`, `Stop`) did not move.
- The ticket says why. bd `aae-orc-oo62t` notes, 2026-09-25: the move "is not done, because the supervisor tests reach s.backoff/s.logPath/s.mgr/s.Env and acceptance says they pass unedited. The env/spawn half ships; the lifecycle move needs a ruling on whether test edits are allowed." The ticket is still in progress.
- **INFERRED:** a second managed service written today would copy `bus.Supervisor`'s lifecycle rather than its environment line. The gate's purpose holds for the lifecycle half, which is not done. Finishing that move, and having the controller's restart backoff use the same primitive, is the split that lines up with the backplane's "supervise".

The other seams fit less directly.

- **INFERRED:** the shift lifecycle (674 lines) is the backplane's "Shift primitive applied to a part", but it is written against sessions, and no second part kind exists to generalize it for.
- **INFERRED:** the client block is not a backplane function at all. It is the control plane's client.

## 5. What the layout costs

### 5.1 Touch rate

**MEASURED** (`git log <window> --format=%h origin/main -- <file> | wc -l`): 390 commits on main in the window.

- `daemon.go` 62, `controller.go` 22, `manager.go` 25;
- for comparison, `cmd/marvel/main.go` 57, `api/types.go` 34, `events/events.go` 33;
- 85 commits touched at least one of the three.

### 5.2 Merge conflicts (a proxy)

**MEASURED** (`gh pr list --state merged --search "merged:2026-09-08..2026-10-08" --json number,files,additions,deletions`; then for each PR, `gh api repos/ArcavenAE/marvel/pulls/<n>/commits`, counting commits with two parents, and among those, messages containing "onflict"):

- 382 merged PRs; 86 touched at least one of the three files, and 23 touched two or more.
- **Hot PRs** (touch one of the three, 86): 29 have a base merge (34%), and 13 name a conflict (15%). Percentages here are rounded to the nearest whole number.
- **Other PRs** (296): 21 have a base merge (7%), and 4 name a conflict (1%).

Most of that gap is PR size. Bucketed by diff lines, Go PRs only:

| diff lines | hot: n, base merge, conflict | other Go: n, base merge, conflict |
|---|---|---|
| 0-300 | 21, 19%, 5% | 81, 9%, 1% |
| 300-1000 | 42, 29%, 10% | 26, 35%, 8% |
| 1000 and up | 23, 57%, 35% | 3, 33%, 0% |

- In the middle band, where both groups have samples, PRs touching the three files fold in main no more often than other Go PRs.
- Small hot PRs do fold in main about twice as often (19% against 9%).
- The large band has three PRs on the other side, too few to compare.

Where the conflicts land is a separate question from how often they happen.

- **MEASURED** (file paths parsed from the 13 conflict-named merge messages; 12 list files): `daemon.go` appears in 9 of the 12.
- **MEASURED:** the nine are #285, #286, #478, #482, #531, #548, #549, #552 and #556. Four of them, #548, #549, #552 and #556, are four of the five PRs of one feature (usage limits; the fifth, #551, also conflicted, in `internal/api/account_test.go`). All five are based on main and merged on 2026-10-04.
- **MEASURED:** two more are the #285/#286 stack that the 2026-09 reflection already diagnosed as a workflow artifact.

Limits:
- A base merge is also made when a branch is only behind.
- A conflict resolved by rebase leaves no trace.
- So these are a proxy and a lower bound on conflicts, not a count.

### 5.3 Review size

**MEASURED** (same PR list, `files[].additions + deletions` for the file against the PR total):

| file | PRs | median lines changed in the file | median PR diff | median files per PR |
|---|---|---|---|---|
| `daemon.go` | 63 | 17 | 688 | 10 |
| `controller.go` | 22 | 12 | 565.5 | 9 |
| `manager.go` | 26 | 14.5 | 551 | 11 |
| none of the three | 296 | | 115 | |

A PR that touches `daemon.go` changes a median of 17 lines in it. The review cost is the rest of the PR: features that cross the CLI, the API types, the daemon handler and the controller in one diff. The "none" group includes docs-only PRs, so its median is not a like-for-like comparison.

### 5.4 Test isolation

**MEASURED** (a `go/ast` walk over `internal/daemon/*_test.go`, following same-package helpers): 334 functions named `Test*`, which includes `TestMain`, so 333 tests.

- 21 start a daemon (`Start`, `startTestDaemon`, `StartMRVL`).
- 192 construct one (`New`, `NewWithOptions`, or a `Daemon{}` literal) without starting it.
- 121 use neither (including `TestMain`).

**MEASURED** (`grep -c 'skipIfNoTmux(t)'`): tests that need a real tmux: daemon 30, team 84, session 43, tmux 34.

**MEASURED**, one run each, on 2026-10-09 between about 06:30Z and 06:40Z, on a macOS arm64 workstation with go1.26.5 and tmux 3.7b on `PATH`, every `MARVEL_*` unset (`go test -count=1`). These are single runs on one host, not a benchmark:

| package | time |
|---|---|
| `internal/daemon` | 128.6 s |
| `internal/team` | 36.4 s |
| `internal/session` | 22.0 s |
| `internal/bus` | 20.1 s |
| `internal/api` | 6.5 s |

**MEASURED** (`go test -count=1 -json ./internal/daemon`): the summed elapsed time is 125.5 s over 333 top-level tests, and the 12 slowest take 66% of it.

- The slowest is `TestStartServesReadsWhileAdoptionWaitsOnTmux` at 20.1 s.
- Next come two module-wide source checks: `TestOnlyRespondSetsResponseResult` at 9.1 s and `TestResponseSourceCheckCatchesPlantedWrites` at 5.3 s. They type-check the whole module and do not depend on the file layout.
- The rest are lifecycle and adoption tests.

**MEASURED** (`grep -oE '\bd\.[a-z][A-Za-z]*\b'` over the daemon tests): tests reach 56 distinct lowercase names after `d.`, in 550 places; 54 of them are `Daemon` fields or methods (`d.sock` is a filename string and `d.limitAct` appears only in a comment and a string). They build `Daemon{}` literals in 14 places across 5 files.

**INFERRED:** two consequences.

- A file split inside `internal/daemon` changes no test and no test time.
- A package split breaks every test that reaches those names, which is the same block `aae-orc-oo62t` hit in `internal/bus`.
- Because `go test` caches per package, any edit to any of daemon's 24 files, or to any of its 23 imports, re-runs the whole 128 s package. That is the one test cost that package boundaries, and not file boundaries, would change.

## 6. Options

**Question for the operator.** Should marvel restructure its three largest files now, and if so, along which seams?

- **(a) Same-package file split.** Move each concern block into its own file in the same package (the 2026-09 reflection's move 3, extended to `controller.go` and `manager.go`). No API or test change.
  - Gives: smaller files to read, and fewer same-region edits.
  - Costs: one large mechanical PR per file, which conflicts with every open PR that touches them.
  - Measured benefit on conflicts: small, because 5.2 found little excess once size is held equal.
- **(b) Two targeted package moves.**
  - (1) The `daemon.go` client block into its own package. One receiver reference (a comment) and seven caller files. I did not measure how many tests reach it.
  - (2) Finish `aae-orc-oo62t`: move the bus supervisor's lifecycle into `workload.Process`, and have the team controller's restart backoff use the same primitive. This needs the ruling the ticket already asks for: may the supervisor tests be edited?
  - Gives: the supervisor gate fully met, one supervise primitive for the backplane, and `cmd/marvel` off the daemon package for client calls.
  - Costs: two PRs, the second with test edits.
- **(c) Backplane-first restructure.** Packages for register, route, supervise and meter now.
  - Costs: the largest change, against a design that is speculative (`question-marvel-service-provider-shape`), with 54 private members reached by tests, and no second part kind to prove the generalization. SOUL section 7 (gradual elaboration) argues against it.
- **(d) Workflow only.** Land a feature as one PR or as a sequenced chain rather than as parallel siblings; the 2026-10-04 usage-limits wave is the specimen. No code change.

## 7. Recommendation

**(b) and (d), and not (a) or (c) now.**

- (b)(2) is the only split the measurements tie to a cost that will grow: the next managed service either waits for it or copies the lifecycle.
- (b)(1) is cheap and has no test coupling.
- (d) addresses the largest single source of the conflicts measured.
- (a) is reasonable as an incremental habit: a handler moved to its own file when it is next edited. But the conflict data does not justify a dedicated sweep, and a sweep would itself conflict.

The decision the operator holds is the `oo62t` question: may the supervisor tests be edited to move the lifecycle?

Source: this finding, marvel `7f1c81e`.

This recommendation is valid until 2026-10-23, or until the marvel services party rules on the backplane shape, whichever comes first; the architect re-checks it then.

## Appendix: commands

The `go/ast` function lister and the test classifier were scratch programs that read files only. The PR data is `gh pr list` and `gh api .../pulls/<n>/commits` output, saved locally and not committed.
