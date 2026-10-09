# Code restructure plan: two package moves and a sequencing rule

- **Status:** Proposal, 2026-10-09, revision 2. It is for review. It changes
  no code, and no ticket is filed from it until it has been reviewed.
- **Ruling it implements:** the operator chose (b) plus (d) from
  finding-marvel-4m2m section 6, on 2026-10-09:
  - two package moves;
  - sequencing as a workflow rule only;
  - (a), a same-package file split, kept as a habit.

  The operator also asked for an adversarial architecture review of this
  plan.
- **Revision 2:** it answers that review (round 1, at `6abc0af`). Section 7
  lists each finding and what changed for it.
- **Author seat:** architect, team arcaven.
- **Code read at:** marvel `2329fa1`, and every cite was read there.
  marvel#799 was read at `92f4376`.
- **Builds on:**
  - finding-marvel-4m2m (the seams and their cost);
  - `services-list.md` sections 3.3 to 3.5 (the `workload.Process` design and
    its gate);
  - `services-shape-requirements.md` MS-8 and MS-9.

## 1. Why

The services backplane needs one supervise primitive before a second managed
service is written. Today the restart lifecycle exists twice, and a driver
written now would copy `bus.Supervisor` rather than reuse it. That is Move 2.

Move 1 has a smaller purpose. The client half of `daemon.go` is the one block
in the three large files that does not use the server's state. Moving it out
gives two things:
- light callers (the simulator, `ctx-forward`) can link the client without
  the server;
- the client gets its own source checks.

Move 1 does not shorten the daemon package's test run. The client package
becomes a daemon import, and finding 4m2m section 5.4 measured that any edit
to a daemon import re-runs the whole package.

## 2. Premises re-checked for this plan

At `2329fa1` unless marked.

- **The client block's span and coupling.**
  - The block runs from `SendRequest` at `daemon.go:2899` to `loadKeyFile`,
    which ends at `:3288`. None of its functions takes `*Daemon`.
  - It shares four names with the rest of the file:
    - `DefaultMRVLPort` (`:58-59`) is used by the server (`:704`, `:707`)
      and by the client (`:3034`);
    - `listenNetwork` (`:73`) is used by the server and by the client
      (`:3010`);
    - `isMRVL` (`:81`) is used by the client only (`:2993`);
    - `busLeafCredential` (`:3293`) is a server constant that sits just
      after the block (used at `:3375`).
- **The block's callers outside the package.** Seven files:
  - five in `cmd/marvel`: `accountlimits.go`, `codexctx.go`,
    `ctxforward.go`, `header.go` and `main.go`;
  - `cmd/simulator/main.go`;
  - `internal/simulator/lua.go`.

  `cmd/marvel` keeps importing the daemon package either way, because
  `marvel daemon` runs the server from it (`NewWithOptions`, `Daemon`,
  `Options`, `Build`).
- **The wire types.** Both the server and the client use these:
  - `Request` (`:91`), `Response` (`:97`), `EventsBatch` (`:1226`) and
    `DialOptions` (`:2871`);
  - the `Method*` constants and `ErrWatchUnsupported`.

  Outside the package, callers use `daemon.Request` 75 times and
  `daemon.Response` 43 times.
- **The user-facing text that names the package.** `:1172` returns
  "use daemon.WatchEventsWith".
- **Tests that reach the client.**
  - Five daemon test files make 34 calls to `SendRequest`, `SendRequestWith`
    or `WatchEventsWith`. They start a daemon and talk to it, so they stay
    in the daemon package.
  - `daemon_test.go:608` tests `isSSH`.
- **The `limitwiring` checks.** `limitwiring_test.go` holds two AST
  inventories, keyed by function name:
  - the callers of the client entry points (`:543-557`);
  - the dial sites (`:662-674`), whose purpose is that the only dials in
    the package are the client's.

  It parses every non-test file in the package (`:28-49`). Both inventories
  fail as soon as the dials leave, which the round-1 review confirmed with a
  prototype.
- **No caller outside the module.** The package is under `internal/`, and
  the module has one `go.mod` and no `go.work`. Go refuses an `internal`
  import from another module, so no external caller exists to keep aliases
  for.
- **`workload.Process` does not exist yet.** `internal/workload` has only
  `ProcessSpec`, `Child` and `Start`.
- **The duplicated backoff.**
  - `computeBackoff` is at `team/controller.go:183-192` and at
    `bus/supervisor.go:190-199`, with the same 30 s and 5 min constants.
  - The bus injects its schedule as a `backoff` field (`supervisor.go:58`).
  - `TestComputeBackoffMatchesRoleSchedule` (`supervisor_test.go:23-34`)
    pins the two schedules against each other.
- **The bus lifecycle under one lock.** `becomeReady` (`supervisor.go:273-300`)
  holds `s.mu` and does all of this in order:
  1. waits on the listener;
  2. runs `AfterReady`;
  3. sets `ready`;
  4. starts `confirmAsync`;
  5. takes the first structural reading (`checkStructureLocked`, so that
     "ready means listener, pid, provisioned, and authorized from the first
     moment anything asks");
  6. emits `bus.started` and, with a hub and no leaf credential,
     `bus.leaf.unenrolled`;
  7. starts `watch`.

  Two more facts:
  - `watch` runs one ticker for both `pollLeaf` and `checkStructure`
    (`:424-433`).
  - Adopt sends SIGHUP before `becomeReady` (`:251`), because this daemon
    re-rendered the passwords. That is a bus decision.
- **The events.** At `2329fa1`, `internal/events/events.go` has twelve
  `bus.*` kinds. `bus.unavailable` is emitted by `team/bushold.go:32`, not by
  the supervisor. marvel#799 adds three more: `bus.pidfile-stale`,
  `bus.pidfile-unproven` and `bus.identity-unrecorded`.
- **marvel#799** ("signal the broker only through a proven child") is open at
  `92f4376` and carries the V11 (b) handling.
  - It edits `internal/bus/supervisor.go`, `internal/workload/process.go`,
    `internal/events/events.go` and `cmd/marvel/main.go`.
  - It adds `internal/childproof` and `internal/procargs`.
  - Its tests add reaches of live lifecycle state (`s.child`, `s.pid`,
    `s.prober`).
- **MS-8's order.** `services-shape-requirements.md` MS-8 says that the
  generic loop lives in `workload.Process`, that the lifecycle interface
  lives in `internal/service`, and "Order: the interface lands first". The
  daemon calls `*bus.Supervisor` directly today.
- **Merges are squashes.** marvel#806 (three commits) landed as `2a6bad2`,
  which has one parent. A commit inside a PR is not a revert unit on main.

## 3. The moves

The unit of review, merge and rollback is a PR. Each move is a short stack of
PRs, merged in order under `stacked-pr-merge-order.md`, and each PR leaves
main building and green. A rollback reverts PRs in reverse order, never one
from the middle of a stack.

### Move 1: the client into `internal/daemonrpc`

**What moves:**
- the wire types (`Request`, `Response`, `EventsBatch`, `DialOptions`), in
  `daemonrpc/wire.go`;
- the `Method*` constants and `ErrWatchUnsupported`, also in `wire.go`;
- the client functions from `daemon.go:2899-3288`, plus `isSSH`, `isMRVL`,
  `listenNetwork` and `DefaultMRVLPort`, in `daemonrpc/client.go`.

`listenNetwork` and `DefaultMRVLPort` are exported from `daemonrpc` because
the server uses them.

**What stays:**
- everything with a `Daemon` receiver;
- the dispatch switch;
- the SSH server (`sshserver.go`);
- `busLeafCredential`.

The name `daemonrpc` follows `net/rpc`, where one package carries the wire
format and its client, and the server consumes the types.

**PR 1a: the package, behind aliases and forwarders.**
- Create `internal/daemonrpc` with the moved code. In `internal/daemon`, add
  a type alias for each moved type and a one-line forwarder for each moved
  function, so no caller changes.
- Move `isSSH`'s test to `daemonrpc`.
- Change the message at `:1172` to name `daemonrpc.WatchEventsWith`.
- Re-inventory `limitwiring` in `internal/daemon`: zero client entry points
  and zero dial sites.
- Add the same two inventories to `internal/daemonrpc`, so the property "the
  only dials are the client's" is still checked where the dials now live.
  The AST helper is small. It is copied rather than shared, unless a second
  copy appears.
- Add a test asserting that `go list -deps ./internal/daemonrpc` names
  exactly `internal/events`, `internal/paths` and `internal/knownhosts` among
  internal packages, so any growth is visible.

**PR 1b: the callers, and the aliases removed.**
- Point the seven caller files at `daemonrpc`.
- Qualify the 34 test calls in the five daemon test files.
- Delete the forwarders and the aliases in the same PR. No out-of-module
  caller can exist, so nothing is kept for a release.

**Tests that prove it:**
- The full suite passes on each PR.
- No assertion changes. The test diff is the `isSSH` test moving, the two
  `limitwiring` inventories, and the 34 qualified calls.
- The existing round trips in `events_watch_test.go` (`get` and an
  `events.watch` stream against a started daemon) now cross the package
  line. They are the wire check.

**Rollback:**
- Revert 1b, then 1a.
- No stored format, wire format or flag changes, so no data step is needed.

### Move 2: finish `aae-orc-oo62t`, the supervise primitive

**Preconditions, in order:**
1. marvel#799 merges. It rewrites the signal and pidfile path inside the code
   this move relocates, and it already carries V11 (b).
2. MS-8's lifecycle interface in `internal/service`.
   - That is the daemon-facing side: what the daemon calls instead of
     `*bus.Supervisor`.
   - Move 2 adds the driver-facing side: what `workload.Process` calls on a
     driver.
   - MS-8 orders the interface first, so Move 2 starts after it. If that
     order should change, it is a change to MS-8, made there.

**The contract.** `workload.Process` owns:
- spawn (through `workload.Start`);
- the watch loop, with its single ticker;
- crash detection and the restart wait;
- adopt by the proven pidfile;
- `Restart`, terminate and `Stop(keep)`.

A driver supplies what is particular to its child, through an interface:

```go
// Driver is what a managed child's driver supplies to the generic loop.
// Process never calls a Driver method while it holds its own lock.
type Driver interface {
    // Adopted runs on the adopt path, before Ready. The bus sends SIGHUP
    // here so the child reloads the passwords this daemon re-rendered.
    Adopted(child ChildInfo) error
    // Ready blocks until the child serves.
    Ready(ctx context.Context, child ChildInfo) error
    // Started runs once the loop has marked the child ready. how is
    // "started" or "adopted".
    Started(ctx context.Context, how string, child ChildInfo)
    // Tick runs on the loop's one ticker.
    Tick(ctx context.Context)
    // Exited runs when the child exits; the driver emits its own crash event.
    Exited(err error, restarts int)
    // Reload asks the running child to reload.
    Reload(child ChildInfo) error
}

func New(spec ProcessSpec, d Driver) *Process
```

`ChildInfo` carries the pid and the start environment as values. A driver may
call `Process` read methods at any time.

**The lock rule.** `Process` calls no `Driver` method while it holds its
mutex. State crosses as values. This removes the lock-order question: there
is one order, driver then `Process`, and only through `Process`'s public
methods.

**The consequence for the ready invariant.** Today, no caller can see
`ready` before the first structural reading, because one lock covers both.
Under the rule, `Process` marks itself ready and only then calls `Started`,
so there is a window. The bus driver closes it itself:
- `bus.Supervisor.Ready()`, which the daemon serves, is true only when the
  `Process` is ready and the driver has taken its first structural reading
  in `Started`.
- What anything outside the bus sees is therefore unchanged.

**The ticker.** One ticker stays, in the `Process` loop, and calls `Tick`.
The bus's `Tick` runs `pollLeaf` then `checkStructure`, as `watch` does
today.

**What stays in the bus driver:**
- the binary lookup;
- the listener dial (`Ready`);
- provisioning, `confirmAsync`, the first structural reading,
  `bus.started` and `bus.leaf.unenrolled` (`Started`);
- the leaf poll and the structural check (`Tick`);
- `bus.crashed` (`Exited`);
- SIGHUP (`Adopted` and `Reload`);
- `mgr`, the leaf and domain status, and every `bus.*` event.

`bus.unavailable` stays in `team/bushold.go`.

**Not built:** a "signal 0 means restart" rule. No driver asks for it (SOUL
section 7).

**The restart schedule.** A value type in a small `internal/backoff` package,
not a function in the spawn package:

```go
type Schedule struct{ Initial, Max time.Duration }
func (s Schedule) Wait(n int) time.Duration
var Restart = Schedule{Initial: 30 * time.Second, Max: 5 * time.Minute}
```

The team controller and the bus driver each take `backoff.Restart`. The bus
keeps its injectable field, now a `Schedule`, so its tests can shorten it. A
session restart is not a `Process`: the two share the schedule, not the
lifecycle.

**PR 2a: `internal/backoff`.**
- Add the package with the table test from
  `TestComputeBackoffMatchesRoleSchedule`.
- Point both call sites at it.
- Delete the bus copy of that test, which would otherwise compare the
  function with itself. This is a test edit, named in the PR.

**PR 2b: `Driver`, `New` and `Process`, with their tests.** Nothing calls
them yet.

**PR 2c: `bus.Supervisor` becomes a driver.** Any supervisor test edits
happen here, under D1.

**PR 2d: remove the dead lifecycle from `internal/bus`.** It also amends
`services-list.md` 3.4 item 3 to match whatever D1 rules.

**Tests that prove it, in `internal/workload`:**
- the allowlist tests in `services-list.md` 3.4, items 1 and 2;
- a crash, then a restart on an injected `Schedule` and clock;
- `Stop(keep=true)` leaves the child and its pidfile in place;
- adopt accepts only a proven child, reusing #799's `childproof` tests;
- **the lock rule:**
  - a test driver whose every method calls `TryLock` on the process mutex
    through a test hook, and fails if the lock is held;
  - a test that runs a crash, `Status()` and `Tick` concurrently under
    `-race`. CI runs `-race`, which catches data races. The `TryLock` check
    covers the lock order, which `-race` does not.

**Tests that prove it, in `internal/bus`:**
- every supervisor test passes, edited only as D1 allows;
- the seed test also asserts that `BEADS_DOLT_PASSWORD` and
  `MARVEL_HEARTBEAT_TOKEN` are absent from the broker's environment;
- **event parity:** one scripted sequence (start, a structural miss, a hub
  with no leaf credential, a crash, a restart, a stop) yields the same
  `bus.*` kinds in the same order before and after.
  - The golden list is recorded in PR 2b against the old code, and asserted
    in PR 2c.
  - The count of kinds is re-taken after #799.

**The gate checks, as source tests:**
- no non-test file in `internal/bus` starts a child. The one allowed
  `exec.Command` is `binaryVersion`'s `--version` run (`supervisor.go:355-365`);
- no non-test file in `internal/bus` calls `syscall.Kill`. #799 routes every
  signal through its prober, so this may already be checked by #799's
  `signal_source_test.go`, which the builder reads first.

**Rollback:**
- Revert 2d, then 2c. That restores the bus's own lifecycle.
- 2a and 2b are additive and may stay.
- The pidfile format is #799's, and Move 2 does not change it. A daemon on
  either side of a revert reads the other's pidfile, and a running broker
  survives a reexec across the revert by the proven adopt.

## 4. Order, and the workflow rule

1. Move 1 (PRs 1a, 1b). It is independent of everything open, and it can
   start now.
2. marvel#799, then MS-8's lifecycle interface.
3. Move 2 (PRs 2a to 2d). PR 2a may land any time, because it touches only
   the two backoff sites.

Move 1 and Move 2 touch different packages, so their stacks may be open at
the same time. Move 2's 2b to 2d may not be open while #799 is.

**The sequencing rule (d), as workflow and not as a check:**
- Land a feature as one PR, or as a chain where each PR is based on the one
  before and merges in order (`stacked-pr-merge-order.md`).
- Never land it as parallel sibling PRs off main against the same files.
- The 2026-10-04 usage-limits wave (#548, #549, #551, #552, #556) is the
  specimen. It carries four of the nine `daemon.go` conflicts that 4m2m
  measured.
- Supervisors apply the rule when they dispatch a builder. Nothing in CI
  enforces it. A CI rule would be a gate created by accretion, which needs
  its own ratified decision.

**The habit (a).** When a handler in `daemon.go`, `controller.go` or
`manager.go` is next edited for a feature, it may move to its own file in the
same package, in a separate commit inside that PR. Never as a sweep.

## 5. Decision, deferred

**D1. May the supervisor tests be edited for Move 2?** This is not asked
yet, because the inventory it rests on changes when #799 merges.

At `2329fa1`, every lifecycle reach in the bus tests is one of two kinds:
- configuration set before `Start` (`s.backoff`, `s.dialTimeout`);
- a file path (`s.pidFile`, `s.logPath`).

Its six `s.mu` reaches guard structure state, which stays with the driver.
No test reads live lifecycle state, so at `2329fa1` a no-edit route works.
#799's tests add reads of `s.child`, `s.pid` and `s.prober`, which are live
state.

The step, as part of PR 2c's preparation:
1. After #799 merges, re-count the reaches in three classes: pre-`Start`
   configuration, path reads, and live-state reads.
2. Present the options against that count:
   - (a) scoped edits to re-point live-state reads at the `Process` or a
     test hook on it, with no assertion changed and each edit listed;
   - (b) no edits, where the configuration and path classes can be served
     by the driver's own fields;
   - (c) defer Move 2.

The likely shape is (b) for the first two classes and (a) only for the
live-state class. That is a forecast, not the recommendation; the
recommendation follows the count. The architect re-checks this when #799
merges or on 2026-10-23, whichever comes first.

## 6. Work items, proposed

None is filed until this plan has been reviewed. Each would land as its own
ticket, with the edges shown.

| id | work | depends on |
|---|---|---|
| R1 | Move 1, PR 1a | none |
| R2 | Move 1, PR 1b | R1 |
| R3 | `internal/backoff` (PR 2a) | none |
| R4 | `Driver`, `New`, `Process` and their tests (PR 2b) | marvel#799, MS-8's interface |
| R5 | the D1 inventory and ruling request | marvel#799 |
| R6 | bus as a driver, and the dead code removed (PRs 2c, 2d) | R4, R5 |

`aae-orc-oo62t` is the existing ticket for R4 to R6. Its REMAINING note would
point here, rather than a new ticket being filed beside it.

## 7. Round-1 review, finding by finding

Every finding was checked at `2329fa1` (and #799 at `92f4376`) before
anything changed. All of them held, and none was rejected.

| finding | what changed |
|---|---|
| B1, a commit is not a revert unit under squash | Section 3 makes the PR the unit, with stacked PRs per move. The wrong "reverting commit 2 restores the forwarders" line is gone |
| B2, the hooks cannot express `becomeReady` and `watch`, and the lock order | A `Driver` interface; the lock rule; the ready window closed by the driver; one ticker kept, with `Tick`; the `TryLock` and concurrent tests |
| B3, the D1 inventory is soft now and stale after #799 | D1 deferred to a three-class count after #799 (section 5) |
| S1, the `limitwiring` inventories | Re-inventoried in `daemon` and added to `daemonrpc` in PR 1a |
| S2, shared names | `DefaultMRVLPort`, `listenNetwork` and `isMRVL` move; `busLeafCredential` stays; the `:1172` text changes |
| S3, aliases for a caller that cannot exist | Aliases removed in PR 1b; the former R2 is dropped |
| S4, the test-time claim and the test diff | Section 1 states the real gain; PR 1b names the 34 calls |
| S5, the backoff's package and shape | `internal/backoff.Schedule`; the self-comparing test is deleted, and named as a test edit |
| S6, event attribution | `bus.unavailable` stays in `bushold.go`; the kinds are re-counted after #799; the golden sequence covers a structural miss and an unenrolled hub |
| S7, stale #799 facts and MS-8(a) | #799 at `92f4376` with V11 (b) in it; Move 2 placed after MS-8's interface |
| S8, nats-server policy in the generic loop | SIGHUP-on-adopt goes to `Adopted`; the signal-0 rule is not built |
| N1 | 5 files and 34 calls |
| N2 | `wire.go` and `client.go` |
| N3 | The dependency test asserts the exact set |
| N4 | Function spans are cited (`:2899-3288`) |
| N5 | `isSSH`'s test moves in PR 1a |
| N6 | The `syscall.Kill` source check is added |
