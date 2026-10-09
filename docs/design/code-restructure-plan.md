# Code restructure plan: two package moves and a sequencing rule

- **Status:** Proposal, 2026-10-09. This plan is for review. Nothing here
  changes code, and no ticket is filed from it until it has been reviewed.
- **Ruling it implements:** the operator chose (b) plus (d) from
  finding-marvel-4m2m section 6, on 2026-10-09: two package moves, and
  sequencing as a workflow rule only. (a), a same-package file split, is kept
  as a habit. An adversarial architecture review of this plan was also
  asked for.
- **Author seat:** architect, team arcaven.
- **Code read at:** marvel `2329fa1`. Every cite below was read there. The
  files the plan touches have not changed between `7561643` and `2329fa1`.
- **Builds on:** finding-marvel-4m2m (the seams and their cost),
  `services-list.md` sections 3.3 to 3.5 (the `workload.Process` design and
  its gate), `services-shape-requirements.md` MS-8 and MS-9.

## 1. Why

The services backplane needs one supervise primitive before a second managed
service is written. Today the restart lifecycle exists twice. A second driver
written now would copy `bus.Supervisor` instead of reusing it. The client
half of `daemon.go` is separate for another reason: it is the one block in
the three large files with no coupling to its host. It is cheap to move, and
it takes 405 lines out of the package whose test run every edit pays for.
Neither move is a broad restructure. Each one is a single seam the study
measured.

## 2. Premises re-checked for this plan

Every check below was run at `2329fa1`.

- **The client block has no `Daemon` receiver.** `daemon.go:2899-3303` runs
  from `SendRequest` to `loadKeyFile`. No function in it takes `*Daemon`. The
  next function, `attachServices` at `:3304`, is a method.
- **The block's callers outside the package.** Seven files call
  `daemon.SendRequest`, `SendRequestWith`, `WatchEventsWith` or
  `DialOptions`: five in `cmd/marvel` (`accountlimits.go`, `codexctx.go`,
  `ctxforward.go`, `header.go`, `main.go`), plus `cmd/simulator/main.go` and
  `internal/simulator/lua.go`. This matches the study.
- **Correction to the study.** 4m2m section 3 says the move "would let
  `cmd/marvel` stop importing the daemon package for client calls". It
  would stop those calls importing it. `cmd/marvel` still imports the daemon
  package, because `marvel daemon` runs the server from it:
  `daemon.NewWithOptions`, `daemon.Daemon`, `daemon.Options`, `daemon.Build`.
  The gain is a client package that a light caller (the simulator,
  `ctx-forward`) can import without linking the server. The `marvel` binary
  itself gains nothing.
- **The client needs the wire types.** `Request` (`daemon.go:91`),
  `Response` (`:97`), `EventsBatch` (`:1226`), `DialOptions` (`:2871`),
  `MethodEventsWatch` and `ErrWatchUnsupported` are used by both the server
  and the client. Callers outside the package use `daemon.Request` 75 times
  and `daemon.Response` 43 times.
- **Tests that reach the client (not measured in 4m2m).**
  - Six daemon test files call `SendRequest`, `SendRequestWith` or
    `WatchEventsWith`, 39 calls in all. They start a daemon and talk to it,
    so they stay where they are.
  - `daemon_test.go:608` tests the private `isSSH`.
  - `limitwiring_test.go:555` and `:664-674` is a source check. It names
    `dialDaemonWith`, `sshAuthMethodsFor`, `dialSSHDirect` and
    `dialSSHTunnel` as the dial sites it accepts. It fails whenever those
    functions move, which is what it is for.
- **`workload.Process` does not exist yet.** `internal/workload/process.go`
  has `ProcessSpec`, `Child` and `Start`. The `Process` type in
  `services-list.md` section 3.3 is a design, not code.
- **The supervisor duplication is still there.** `computeBackoff` is at
  `internal/team/controller.go:183-192` and at `internal/bus/supervisor.go:190-199`.
  The constants are the same in both: 30 s initial and 5 min maximum
  (`controller.go:149-150`, `supervisor.go:175-176`).
  `bus.Supervisor` still owns `spawnLocked` (`:306`), `watch` (`:400`),
  `onCrash` (`:450`), `Restart` (`:484`), `terminate` (`:517`) and `Stop`
  (`:568`).
- **The supervisor tests reach private state.** Across `internal/bus/*_test.go`:
  `s.observeLeaf` 23 times, `s.mgr` 7, `s.mu` 6, `s.leafPoll` 5,
  `s.dialTimeout` 5, `s.checkStructure` 5, `s.leafRepeatEvery` 4, `s.now` 3,
  `s.backoff` 3, `s.pidFile` 2, `s.authReloaded` 2, and `s.logPath`,
  `s.pollLeaf` and `s.checkStructureFn` once each.
  - Two kinds of reach are mixed here. The leaf-poll and structure reaches
    stay with `bus.Supervisor` under section 3.3, so they are unaffected.
  - `s.backoff`, `s.pidFile`, `s.logPath`, `s.mu`, and `s.dialTimeout` where
    it bounds the listener wait, touch the lifecycle that moves.
- **Open work on the same lines.** marvel#799, "signal the broker only
  through a proven child", is an approved draft at `742eb07`. It edits
  `internal/bus/supervisor.go`, `supervisor_test.go` and
  `internal/workload/process.go`, and adds `internal/childproof`. It is the
  MS-9 proof. The operator ruled V11 (b) for its legacy case on 2026-10-09.

## 3. The moves

### Move 1: the client block into `internal/daemonrpc`

**What moves.** Into a new package `internal/daemonrpc`:

- the wire types: `Request`, `Response`, `EventsBatch`, `DialOptions`;
- the method-name constants the client sends (`MethodEventsWatch`, and
  any other `Method*` a caller outside the package uses);
- `ErrWatchUnsupported`;
- every function in `daemon.go:2899-3303`;
- `isSSH`.

The package holds both halves of the wire: the types, and the client that
speaks them. The server imports it for the types. This follows `net/rpc`,
where one package carries the wire format and its client, and the server is
one consumer of the types.

**What stays.** Everything with a `Daemon` receiver, the dispatch switch, the
SSH server (`sshserver.go`), and the server-side handling of
`MethodEventsWatch`.

**How, in three commits on one PR:**

1. Create `internal/daemonrpc` with the moved code, unchanged except for its
   package clause. In `internal/daemon`, replace each moved type with an
   alias (`type Request = daemonrpc.Request`) and each moved function with a
   one-line forwarder. No caller changes. `go build ./...` and the full test
   suite pass. The `limitwiring` source check is updated to name the new
   file paths. This is a test edit, inside Move 1's own ruling.
2. Point the seven caller files at `daemonrpc` directly. Move `isSSH`'s
   test with it.
3. Delete the forwarders. Keep the type aliases for one release, so an
   out-of-tree caller has a release to migrate. Remove them in a later,
   separate PR.

**Tests that prove it.**

- The full suite passes at each commit, with no assertion changed. The diff
  of `internal/daemon/*_test.go` across the PR is limited to import lines,
  the `isSSH` test leaving, and the `limitwiring` path table.
- `go list -deps ./internal/daemonrpc` names no `internal/daemon`,
  `internal/team` or `internal/session`. A test in `daemonrpc` asserts this,
  so the package cannot grow a server dependency back.
- A wire round trip: a `daemonrpc` client against the existing test daemon
  (`startTestDaemon`) for `get sessions` and an `events.watch` stream. The
  daemon package already has these in `events_watch_test.go`; they now cross
  the package line.

**Rollback.** Each commit reverts on its own. Commit 1 is additive behind
aliases. Reverting commit 2 or 3 restores the forwarders. No stored format,
wire format or flag changes, so a revert needs no data step.

### Move 2: finish `aae-orc-oo62t`, the supervise primitive

**Precondition.** marvel#799 merges first. It rewrites the signal path and
the pidfile proof inside the code this move relocates. Moving the code
before it lands means two in-flight PRs on the same functions, which is the
pattern section 4 rules out. The V11 (b) legacy-pidfile handling ships with
the MS-9 proof, as MS-9 already requires, so it comes in with #799 or
directly after it, and before this move.

**What moves.** Into `internal/workload`:

- a `Process` type that owns spawn (`spawnLocked`, through the existing
  `workload.Start`), watch, crash handling and backoff, adopt by the proven
  pidfile, `Restart`, `terminate` and `Stop(keep)`;
- `ProcessSpec` gains the driver hooks from `services-list.md` section 3.3:
  `Ready`, `AfterReady` and `Reload`;
- one exported backoff, `workload.Backoff(n int) time.Duration`, with the
  30 s and 5 min constants.

**What stays.** `bus.Supervisor` becomes the bus driver:

- it builds the `ProcessSpec` (binary lookup, listener dial as `Ready`,
  provisioning as `AfterReady`, SIGHUP as `Reload`);
- it keeps the `/leafz` poll, the structural check, the `bus.leaf.*` events,
  the `Leaf` and `Domain` status fields, and `mgr`;
- the `bus.*` event kinds (twelve in `internal/events/events.go` today) keep
  their names. The lifecycle ones (`bus.started`, `bus.crashed`,
  `bus.stopped`, `bus.reloaded`, `bus.unavailable`) are emitted from
  `Process` through a hook the driver supplies.

The team controller keeps its restart policy, crash-loop state and
`RoleHealth`. Only its `computeBackoff` is replaced, by a call to
`workload.Backoff`. A session restart is not a `Process`. The two share the
schedule, not the lifecycle.

**How, in four commits on one PR:**

1. Add `workload.Backoff` with a table test that pins the existing values.
   Point both `computeBackoff` sites at it. No behavior change, and the
   existing controller and supervisor tests pass unedited.
2. Add `workload.Process`, built from the lifecycle code as #799 leaves it,
   with its own tests (below). Nothing calls it yet.
3. Make `bus.Supervisor` a driver over `workload.Process`. This is the
   commit that edits supervisor tests, if decision D1 allows it.
4. Remove the dead lifecycle code from `internal/bus`. `spawnLocked` is gone,
   which is the structural form of the gate in `services-list.md` 3.5.

**Tests that prove it.**

- In `internal/workload`: the allowlist tests in `services-list.md` 3.4,
  items 1 and 2.
- Also in `internal/workload`:
  - a crash then restart on the `Backoff` schedule, with an injected clock;
  - `Stop(keep=true)` leaves the child running and its pidfile in place;
  - adopt accepts only a proven child, reusing #799's `childproof` tests
    against `Process`;
  - `Reload` sends the declared signal, and restarts when the signal is 0.
- In `internal/bus`: every supervisor test passes, edited only as D1 allows.
  The seed test also asserts that `BEADS_DOLT_PASSWORD` and
  `MARVEL_HEARTBEAT_TOKEN` are absent from the broker's environment (3.4
  item 3).
- Event parity:
  - The same scripted crash, restart and stop sequence is run before and
    after the move.
  - It must produce the same `bus.*` event kinds in the same order.
  - It is recorded at commit 1 as a golden list and asserted at commit 3.
- The gate check: a source test asserts that no non-test file in
  `internal/bus` starts a child (`exec.Command` followed by `Start`, or
  `workload.Start`). The one allowed `exec.Command` is `binaryVersion`'s
  one-shot `--version` run (`supervisor.go:356`), and the test names it.
  This way a second spawn path cannot come back.

**Rollback.**

- Commits 1 and 2 are additive.
- Commit 3 is the one behavior-bearing change. Reverting it restores the bus
  supervisor's own lifecycle, because commit 4 is a separate commit and is
  reverted first.
- The pidfile format is #799's, and this move does not change it. So a
  daemon at either side of the revert reads the other's pidfile.
- A running broker survives a daemon reexec across the revert, as it does
  today (adopt by proven pidfile).

## 4. Order, and the workflow rule

1. Move 1. It is independent of everything open, and it can start now.
2. marvel#799, and the V11 (b) legacy handling.
3. Move 2.

Move 1 and Move 2 touch different packages, so they may be open at the same
time. Move 2 may not be open while #799 is.

**The sequencing rule (d), as workflow and not as a check:**

- Land a feature as one PR, or as a chain where each PR is based on the one
  before and merges in order (`stacked-pr-merge-order.md`).
- Never land it as parallel sibling PRs off main against the same files.
  The 2026-10-04 usage-limits wave (#548, #549, #551, #552, #556) is the
  specimen: it carries four of the nine `daemon.go` conflicts that 4m2m
  measured.
- Supervisors apply this when they dispatch a builder. Nothing in CI
  enforces it. A CI rule would be a gate created by accretion, which needs
  its own ratified decision.

**The habit (a):** when a handler in `daemon.go`, `controller.go` or
`manager.go` is next edited for a feature, it may move to its own file in the
same package, in a separate commit inside that PR. Never as a sweep.

## 5. Decision needed

**D1. May the supervisor tests be edited for Move 2?** This was not ruled on
2026-10-09, and Move 2's commit 3 needs an answer.

- (a) Yes, scoped:
  - an edit may re-point a reach (`s.backoff`, `s.pidFile`, `s.logPath`,
    `s.mu`, `s.dialTimeout`) to the `Process` it now lives on, or to an
    exported test hook on it;
  - it may not change what any test asserts;
  - the PR lists each edited test with its before and after line, and the
    reviewer checks that the assertions are unchanged.
- (b) No edits. `bus.Supervisor` keeps those fields as configuration and
  hands them to `Process` at `Start`. Tests that set `s.backoff` or
  `s.dialTimeout` before `Start` keep working. Tests that read `s.mu`,
  `s.pidFile` or crash state after the fact would then read a copy, not the
  live lifecycle state. Those tests would pass while checking stale fields.
- (c) Defer Move 2 and ship Move 1 alone.

Recommended: (a). Option (b) keeps the tests green by giving the lifecycle
two sources of truth. That is worse than a reviewed, behavior-preserving
edit. The sentence in `services-list.md` 3.4 item 3, "the ten existing
supervisor tests pass unedited", would be amended in the same PR to say
"pass, edited only to re-point private reaches".

This recommendation is valid until 2026-10-23, or until #799 merges,
whichever comes first. The architect re-checks it then. Nothing executes on
silence or on that date.

## 6. Work items, proposed

None is filed until this plan has been reviewed and D1 is ruled. Each would
land as its own ticket, with the edges shown.

| id | work | depends on |
|---|---|---|
| R1 | Move 1, commits 1 to 3 | none |
| R2 | remove the `internal/daemon` type aliases, one release after R1 | R1 |
| R3 | Move 2 commits 1 and 2 (`workload.Backoff`, `workload.Process` with its tests) | marvel#799 |
| R4 | Move 2 commits 3 and 4, and the `services-list.md` 3.4 amendment | R3, D1 |

`aae-orc-oo62t` is the existing ticket for R3 and R4. Its REMAINING note
would point here, rather than a new ticket being filed beside it.
