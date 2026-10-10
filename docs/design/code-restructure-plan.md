# Code restructure plan: two package moves and a sequencing rule

- **Status:** Proposal, 2026-10-10, revision 4. It is for review. It changes
  no code, and no ticket is filed from it until it has been reviewed.
- **Ruling it implements:** the operator chose (b) plus (d) from
  finding-marvel-4m2m section 6, on 2026-10-09:
  - two package moves;
  - sequencing as a workflow rule only;
  - (a), a same-package file split, kept as a habit.

  The operator also asked for an adversarial architecture review of this
  plan.
- **Revisions:** revision 2 answered review round 1 (at `6abc0af`), and
  revision 3 answered round 2 (at `9d13728`), and revision 4 answers the
  formal review of revision 3 and records D1's ruling. Sections 7 to 9 list
  each finding and what changed for it.
- **Author seat:** architect, team arcaven.
- **Code read at:** marvel `76a7db9` (the squash of #799). `daemon.go`,
  `team/controller.go`, `services-list.md` and
  `services-shape-requirements.md` are byte-identical at `2329fa1`,
  `76a7db9` and main `4f65cc3`, so the Move 1 cites hold at all three.
  Move 2 cites are `76a7db9` line numbers.
- **Builds on:**
  - finding-marvel-4m2m (the seams and their cost);
  - `services-list.md` sections 3.3 to 3.5 (the `workload.Process` design and
    its gate);
  - `services-shape-requirements.md` MS-8 and MS-9;
  - `bus-pidfile-identity.md` (#799's design, section 4.5 for the signal
    seam).

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

At `76a7db9`. The Move 1 facts read the same at `2329fa1`, because `daemon.go` is unchanged.

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
- **The block's callers outside the package.** Seven files call the client
  functions:
  - five in `cmd/marvel`: `accountlimits.go`, `codexctx.go`,
    `ctxforward.go`, `header.go` and `main.go`;
  - `cmd/simulator/main.go`;
  - `internal/simulator/lua.go`.

  Counting every moved name, not only the client functions, 27 files outside
  `internal/daemon` use them at `76a7db9`: 14 non-test and 13 test files, all
  in `cmd/marvel`, `cmd/simulator` and `internal/simulator`
  (`grep -rlE '\bdaemon\.(Request|Response|EventsBatch|DialOptions|Method[A-Z]\w*|ErrWatchUnsupported|SendRequest|SendRequestWith|WatchEventsWith|DefaultMRVLPort)\b'`).

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
- **`workload.Process` does not exist yet.** `internal/workload` has
  `ProcessSpec`, `Child` and `Start`; #799 added `ProcessSpec.BeforePidFile`.
- **The duplicated backoff.**
  - `computeBackoff` is at `team/controller.go:183-192` and at
    `bus/supervisor.go:210-219`, with the same 30 s and 5 min constants
    (`controller.go:149-150`, `supervisor.go:195-196`).
  - The bus injects its schedule as a `backoff` field (`supervisor.go:60`).
  - `TestComputeBackoffMatchesRoleSchedule` (`supervisor_test.go:23`) pins
    the two schedules against each other.
- **The adopt path, as #799 left it.** `Start` (`supervisor.go:268-312`)
  classifies the pidfile (`classifyPidfile`, `:355`, reading the identity
  record from `identity.go`) and then takes one of five branches:
  - proven and answering: SIGHUP through the prober, then `becomeReady`
    ("adopted") (`:277-288`);
  - proven and silent: terminate and remove the identity files (`:290-293`);
  - stale: remove the files, emit `bus.pidfile-stale`, and refuse with
    `bus.pidfile-unproven` if the port answers (`:294-299`, `refuseUnproven`
    `:323`);
  - legacy: `startLegacy` (`:342`);
  - then the stranger check (`strangerRefusal`, `:316`) and a fresh spawn.

  Four of these probe the bus listener (`listenerAnswers`, `:981`). The fresh
  spawn's `BeforePidFile` closure proves the child, writes the identity record
  and, on failure, emits `bus.identity-unrecorded` (`:446-464`). Every one of
  these steps is about the bus: its address, its record format and its
  events.
- **The bus lifecycle and its lock.** `becomeReady` (`:383-410`) runs with
  `s.mu` held:
  1. waits on the listener;
  2. runs `AfterReady`;
  3. sets `ready` (`:392`);
  4. starts `confirmAsync`;
  5. takes the first structural reading (`checkStructureLocked`);
  6. emits `bus.started` and, with a hub and no leaf credential,
     `bus.leaf.unenrolled`;
  7. starts `watch`.

  `checkStructureLocked` drops `s.mu` at `:790` around the broker read and
  retakes it at `:801`. So today a caller of `Ready()` (`:751`, serving
  `readyLocked` `:759`) can see the bus ready before the first reading. On a
  restart it can see ready on the dead child's reading, because neither
  `onCrash` nor `spawnLocked` clears the structure. The comment at `:396-398`
  states an invariant the code does not have.
- **Messages the lifecycle carries.** `bus.crashed` reports the restart count
  and the backoff wait (`:598`). `bus.started`'s `how` is "started",
  "adopted", "restarted (#n)" (`:614`) or "restarted (<reason>)" (`:654`).
  `watch` (`:535-587`) returns without `onCrash` when the stop was requested,
  so `bus.crashed` fires only on an unrequested exit.
- **The signal seam.** Every bus signal goes through
  `s.prober.Signal(child, ...)` on a `childproof.Child`, never a pid
  (`bus-pidfile-identity.md` 4.5). `TestBusSignalsOnlyThroughTheProber`
  (`signal_source_test.go:56`) fails on any other way to signal.
  `childproof` imports only the standard library.
- **The real lock nesting.** `becomeReady` holds `s.mu` and calls
  `AfterReady` (`supervisor.go:387-391`). The daemon's `AfterReady` calls
  `mgr.Admin()` (`daemon.go:3407-3408`), which takes `m.mu`
  (`manager.go:319-321`). So the code nests `s.mu`, then `m.mu`.
  `manager.go:64-68` describes the reverse (`Regenerate` taking `m.mu`, then
  the supervisor lock through `Reload`). But `Regenerate` (`:341-355`) calls
  `Reload` only after `Render` has returned, so at `76a7db9` nothing holds
  `m.mu` across `Reload`. The comment describes an order the code does not
  have.
- **The events.** Fifteen `bus.*` kinds at `76a7db9` (twelve, plus #799's
  `bus.pidfile-stale`, `bus.pidfile-unproven` and
  `bus.identity-unrecorded`). `bus.unavailable` is emitted by
  `team/bushold.go:32`, not by the supervisor.
- **marvel#799 merged** on 2026-10-09T20:48:44Z as `76a7db9`, with the V11 (b)
  handling. It changed `internal/bus/supervisor.go` and `identity.go`,
  `internal/workload/process.go`, `internal/events/events.go`,
  `cmd/marvel/main.go` and several bus test files, among others. It added
  `internal/childproof`, `internal/procargs` and `bus-pidfile-identity.md`.
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

**PR 1b: every user outside the package.**
- Rewrite all 27 files outside `internal/daemon` that name a moved
  identifier (section 2), not only the seven client callers, with the same
  `gofmt -r` rewrites 1c uses. The PR body lists the 27 files.
- Delete the forwarders. The type aliases stay until 1c, so a file 1b
  missed still builds, and 1c's build is the check that none was missed.

**PR 1c: the package's own names, and the aliases removed.** Without the
aliases, every use of the wire types inside `internal/daemon` needs a
`daemonrpc.` qualifier. Counted at `76a7db9` (comments included):
- non-test files: `Request` 9, `Response` 154, `Method*` 10, `EventsBatch` 7,
  `DialOptions` 2, so about 182 uses outside the moved block;
- test files: `Request` 85, `Response` 70, `EventsBatch` 5, `DialOptions` 4,
  `Method*` 3, `ErrWatchUnsupported` 2, so 169 uses, including the 34 client
  calls.

The rewrite is mechanical, with `gofmt -r` per identifier (for example
`gofmt -r 'Response -> daemonrpc.Response'`), and is reviewed as a mechanical
diff, with the counts in the PR body. This is the idiomatic end state: type
aliases exist to carry a move, not to stay.

**Tests that prove it:**
- The full suite passes on each PR.
- No assertion changes. The test diff is the `isSSH` test moving, the two
  `limitwiring` inventories, and the mechanical qualification in 1c.
- The existing round trips in `events_watch_test.go` (`get` and an
  `events.watch` stream against a started daemon) now cross the package
  line. They are the wire check.

**Rollback:**
- Revert 1c, then 1b, then 1a.
- No stored format, wire format or flag changes, so no data step is needed.

### Move 2: finish `aae-orc-oo62t`, the supervise primitive

**Preconditions:**
1. marvel#799. It has merged (`76a7db9`).
2. MS-8's lifecycle interface in `internal/service`. That is the
   daemon-facing side, what the daemon calls instead of `*bus.Supervisor`.
   Move 2 adds the driver-facing side, what `workload.Process` calls on a
   driver. MS-8 orders the interface first, so PRs 2b to 2d start after it.
   If that order should change, it is a change to MS-8, made there. No ticket
   exists for that interface yet; R0 in section 6 names it.

**The split: the driver decides how a child enters, and `Process` runs it
from there.** Round 2 offered two shapes for the adopt path #799 enlarged:
- (a) `Process` decides adoption, through more driver methods, and the
  identity record moves to `workload`;
- (b) the pidfile verdict stays in the driver, and `Process` exposes two
  entry points.

This plan takes (b). Every step of the adopt path is about the bus: its
listener address, its identity-record format and its three events. No second
driver exists to show which parts would generalize (SOUL section 7). With (b),
no event changes hands.

The driver keeps:
- `classifyPidfile`, `identity.go` and the identity sidecar;
- `startLegacy`, `refuseUnproven`, `strangerRefusal` and `listenerAnswers`;
- the SIGHUP on adopt, sent through the prober before it hands the child to
  `Process`;
- the `BeforePidFile` closure that writes the identity record;
- the leaf poll, the structural check, provisioning and `confirmAsync`;
- `mgr`, the leaf and domain status, and every `bus.*` event.

`bus.unavailable` stays in `team/bushold.go`.

`workload.Process` owns everything after a child is in hand:
- spawn and respawn through `workload.Start`, with the driver's `ProcessSpec`.
  `ProcessSpec` gains `Grace`, the wait between SIGTERM and SIGKILL that
  `terminate` uses (`supervisor.go:667`), so the driver keeps its `grace`
  field and passes it in;
- adopt of a child the driver has already proven (`Adopt`);
- the watch loop, with its one ticker;
- crash detection and the restart wait;
- `Restart`, terminate and `Stop(keep)`.

**The interface:**

```go
// Driver is what a managed child's driver supplies to the generic loop.
// Ready, Started, Tick and Exited run on the Process loop goroutine, one at
// a time, with no Process lock held.
type Driver interface {
    // Ready blocks until the child serves.
    Ready(ctx context.Context, c ChildInfo) error
    // Started runs once the loop has marked the child ready.
    Started(ctx context.Context, s Start)
    // Tick runs on the loop's one ticker, under a deadline below its period.
    Tick(ctx context.Context)
    // Exited runs when the child exits.
    Exited(e Exit)
}

type ChildInfo struct {
    Child childproof.Child // the proven child; never a bare pid
    Env   []string         // the start environment; nil for an adopted child
    Gen   uint64           // bumped by Process on every spawn or adopt
}

type StartKind int // Spawned, Adopted, Restarted

type Start struct {
    How      StartKind
    Restarts int
    Reason   string // why a requested restart happened; "" otherwise
    Child    ChildInfo
}

type Exit struct {
    Err       error
    Requested bool          // Stop or Restart asked for it
    Restarts  int
    RetryIn   time.Duration // the backoff wait before the respawn
}

func New(spec ProcessSpec, d Driver, p childproof.Prober, sched backoff.Schedule) *Process
func (p *Process) Spawn(ctx context.Context) error
func (p *Process) Adopt(ctx context.Context, c childproof.Child) error
func (p *Process) Restart(reason string) error
func (p *Process) Stop(keep bool)
func (p *Process) Snapshot() State
```

- `Spawn` and `Adopt` block until the loop reports `Ready`'s result, as
  `Start` returns `becomeReady`'s error today.
- The bus emits `bus.crashed` from `Exited` only when `Requested` is false,
  with `Restarts` and `RetryIn`. It builds `bus.started`'s text from `Start`.
- `Process` holds the prober, because it signals on terminate and stop. The
  driver uses the same prober value for its SIGHUP on adopt and for
  `Reload`. A `Reload` reads the current child from `Snapshot` and signals
  it, with no `Process.Reload`, so the Manager's reload never re-enters the
  driver through `Process`.
- `State` holds the pid, the proven child, the generation, ready, adopted,
  the restart count, the pidfile state and the backoff end, all taken under
  one lock.

`Driver` has four methods and one implementer, which is acceptable now. If a
second driver has no tick, a `Ticker` interface checked by type assertion is
preferred over a no-op method, but not before that driver exists.

**The concurrency contract:**
- `Process` calls no `Driver` method, and no `ProcessSpec` func (`MintedEnv`,
  `Check`, `BeforePidFile`), while it holds its own mutex. State crosses as
  values.
- A driver never holds its own lock while calling a `Process` method.
- `Ready`, `Started`, `Tick` and `Exited` run on the `Process` loop goroutine
  and never overlap. `Spawn`, `Adopt`, `Restart` and `Stop` are requests to
  that goroutine, so spawn, adopt, a requested restart and the crash respawn
  are serialized by the loop, not by holding `p.mu`. `p.mu` guards only the
  state `Snapshot` copies.
- The order is `s.mu`, then `m.mu`, which is the only nesting the code has
  (section 2). `p.mu` is never held while another lock is taken, and never
  taken while `s.mu` is held: a driver takes `Snapshot` first, then `s.mu`.
  `Ready()` and `Status()` both do this. The order is recorded next to the
  `leafAttached` note in `manager.go`, and PR 2c corrects that note's
  `m.mu`-first sentence.

**Readiness: today's window, and the fix.** Today `Ready()` can be true
before the first structural reading, and on a restart true on the dead
child's reading (section 2). This plan closes that window, which is stricter
than today:
- Each structural reading is tagged with the generation of the child it was
  read from (`structureGen`), taken from `Snapshot` when the read starts.
- `Started` clears the reading (`structureKnown = false`) before taking the
  first one, and records `readyGen = s.Child.Gen` when it ends.
- A reading whose generation is not the current one is dropped on arrival.
  This covers a failed first reading, which today keeps the dead child's
  structure (`supervisor.go:803`), and a late reading from Reload's delayed
  `checkStructure` (`:705-716`) that lands after a respawn.
- `bus.Supervisor.Ready()` takes one `Snapshot`, then `s.mu`, and returns
  `snap.Ready && snap.Gen == readyGen && (!structureKnown || (structureGen == snap.Gen && structure.Healthy()))`.
  With no reading for the current child it behaves as today's first start
  does, which is ready with no reading.
- The comment at `supervisor.go:396-398` is corrected in PR 2c.

`bus.Supervisor.Status()` takes the same single `Snapshot` at its top, so its
pid and ready fields come from one child.

**The ticker.** One ticker stays, in the `Process` loop, and calls `Tick`.
The bus's `Tick` runs `pollLeaf` then `checkStructure`, as `watch` does today
(`:572-574`). A tick today can hold the loop for the 10 s structure read
(`health.go:61`) plus the 3 s `/leafz` client (`supervisor.go:918`), which
delays exit detection by up to 13 s. `Tick`'s context carries a deadline
below the ticker period, and the doc comment says so.

**Not built:** a "signal 0 means restart" rule, and a generic adopt verdict.
No driver asks for either.

**The restart schedule.** A value type in a small `internal/backoff`
package:

```go
type Schedule struct{ Initial, Max time.Duration }
func (s Schedule) Wait(n int) time.Duration
var Restart = Schedule{Initial: 30 * time.Second, Max: 5 * time.Minute}
```

The team controller and `Process` each take `backoff.Restart`, and the bus
tests pass a short `Schedule` through `New`. A session restart is not a
`Process`: the two share the schedule, not the lifecycle.

**PR 2a: `internal/backoff`.** It is independent of 2b to 2d.
- Add the package with the table test from
  `TestComputeBackoffMatchesRoleSchedule`.
- Point both call sites at it, and change the bus `backoff` field's type to
  `backoff.Schedule`.
- Delete the bus copy of that test, which would otherwise compare the
  function with itself. This is a test edit, named in the PR.

**PR 2b: `Driver`, `New` and `Process`, with their tests.** Nothing calls
them yet. The golden event list (below) is recorded in this PR against the
old code.

**PR 2c: `bus.Supervisor` becomes a driver.** The supervisor test edits D1
allows happen here.

**PR 2d: remove the dead lifecycle from `internal/bus`.** It also amends
`services-list.md` 3.4 item 3 to match D1.

**Tests that prove it, in `internal/workload`:**
- the allowlist tests in `services-list.md` 3.4, items 1 and 2;
- a crash, then a respawn on an injected `Schedule` and clock, with the
  generation bumped;
- `Stop(keep=true)` leaves the child and its pidfile in place;
- `Adopt` of a proven child, and a requested exit reported with
  `Requested` set;
- **the lock rule, in two separate tests:**
  - single-goroutine: a test driver whose every method, and every
    `ProcessSpec` func, calls `TryLock` on the process mutex through a test
    hook, fails if it cannot take it, and unlocks it at once. This shows the
    lock was free at each scripted call. It does not prove the rule for paths
    the script does not reach, and it is kept apart from the concurrent test,
    where another goroutine's `Snapshot` would make it fail spuriously;
  - concurrent: a crash, `Snapshot` and `Tick` run together under `-race`;
- **readiness, in `internal/bus`:**
  - a crash during `Started` never leaves `Ready()` true for the new child
    before its own first reading;
  - after a respawn, a failed first reading leaves no structure from the
    dead child in effect;
  - a delayed reading from `Reload`, released after a respawn, is dropped and
    does not change `Ready()`.

**Tests that prove it, in `internal/bus`:**
- every supervisor test passes, edited only as D1 allows;
- the seed test also asserts that `BEADS_DOLT_PASSWORD` and
  `MARVEL_HEARTBEAT_TOKEN` are absent from the broker's environment;
- **event parity:** one scripted sequence yields the same `bus.*` kinds in
  the same order before and after. The sequence is: a stale pidfile, an
  unproven port, a fresh start, a structural miss, a hub with no leaf
  credential, a crash, a restart, a stop. The golden list is recorded in 2b
  and asserted in 2c. Readiness is not part of parity, because it becomes
  stricter by design.

**The gate checks, as source tests:**
- no non-test file in `internal/bus` starts a child. The one allowed
  `exec.Command` is `binaryVersion`'s `--version` run (`supervisor.go:490-500`);
- signals: `TestBusSignalsOnlyThroughTheProber` (`signal_source_test.go:56`)
  already covers `syscall.Kill` and `(*os.Process).Signal`. An equivalent is
  added for `internal/workload`.

**Rollback:**
- Revert 2d, then 2c. That restores the bus's own lifecycle.
- 2a and 2b are independent of 2c and 2d and may stay.
- The pidfile and identity formats are #799's, and Move 2 does not change
  them. A daemon on either side of a revert reads the other's files, and a
  running broker survives a reexec across the revert by the proven adopt.

## 4. Order, and the workflow rule

1. Move 1 (PRs 1a, 1b, 1c). It is independent of everything open, and it can
   start now.
2. PR 2a, at any time. It touches only the two backoff sites.
3. MS-8's lifecycle interface (R0), then PRs 2b to 2d.

Move 1 and Move 2 touch different packages, so their stacks may be open at
the same time.

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

## 5. Decision D1, ruled

**D1. May the supervisor tests be edited for Move 2?** The operator ruled on
2026-10-10, relayed by director, verbatim: "yes, the plan may edit the
supervisors tests, allow the five edits". The grant covers the five
live-state reaches in the last row of the table below, and nothing else.

Counted at `76a7db9`
(`grep -oE '\bs\.<field>\b' internal/bus/*_test.go`), sorted by what each
reach does under the split in section 3:

| class | reaches | count | under the split |
|---|---|---|---|
| configuration set before `Start` | `s.backoff` 4, `s.dialTimeout` 5, `s.prober` set 1 (`proctable_test.go:34`), `s.onSpawn` 3, `s.adoptPoll` 1, `s.grace` 1 (`supervisor_identity_test.go:318`) | 15 | the driver keeps these fields and passes them to `New` or its `ProcessSpec` at `Start` (`grace` as `ProcessSpec.Grace`): no edit |
| file paths | `s.pidFile` 22, `s.logPath` 2 | 24 | the driver keeps the paths and puts them in its `ProcessSpec`: no edit |
| structure and leaf state | `s.mu` 6, `s.leafPoll` 5, and the leaf and structure methods | 11 or more | stays in the driver: no edit |
| live lifecycle state | `s.pid` 1 (`proctable_test.go:142`), `s.child` 3 and `s.prober.Same` 1 (`supervisor_identity_test.go:298-299`) | 5, in two tests | lives in `Process` after the split |

`s.restarts`, `s.ready`, `s.backoffTill`, `s.cmd`, `s.watchDone` and
`s.stopping` have no reaches. There are ten `func Test` in
`supervisor_test.go`.

The options were (a) edit the five live-state reaches, (b) no edits, with
a second copy of live state in the driver, and (c) defer Move 2. (a) was
recommended and ruled. If the split as built needs any edit beyond these
five, it comes back as its own decision and is not folded into this grant.

`services-list.md` 3.4 item 3, "the ten existing supervisor tests pass
unedited", is amended in PR 2d to "pass, with five live-state reads in two
tests re-pointed at `Process.Snapshot`".

## 6. Work items, proposed

None is filed until this plan has been reviewed. D1 is ruled (section 5). Each would
land as its own ticket, with the edges shown.

| id | work | depends on |
|---|---|---|
| R0 | MS-8's lifecycle interface in `internal/service` (filed from MS-8, not from this plan; listed so the edge can be drawn) | none |
| R1 | Move 1, PR 1a | none |
| R2 | Move 1, PR 1b | R1 |
| R3 | Move 1, PR 1c | R2 |
| R4 | `internal/backoff` (PR 2a) | none |
| R5 | `Driver`, `New`, `Process` and their tests, and the golden event list (PR 2b) | R0 |
| R6 | bus as a driver, and the dead code removed (PRs 2c, 2d) | R5 |

`aae-orc-oo62t` is the existing ticket for R5 and R6. Its REMAINING note would
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

## 8. Round-2 review, finding by finding

Every finding was checked at `76a7db9` before anything changed. All of them
held, and none was rejected.

| finding | what changed |
|---|---|
| B1, the six-method `Driver` cannot express #799's adopt path | Shape (b): the pidfile verdict, identity record, probes, refusals and the three events stay in the driver; `Process` exposes `Spawn` and `Adopt` and owns the loop after a child is in hand. The owns and keeps lists name each function |
| S1, `ChildInfo` carries a pid | `ChildInfo{Child childproof.Child, Env, Gen}`; `Process` holds the prober, injected through `New`; `Env` is nil for an adopted child |
| S2, the ready premise is false, and the flag is a lost update | Section 2 states today's window. The fix is a generation token, `readyGen`, read against one `Snapshot`. The goroutine ownership of each `Driver` method is stated. Readiness leaves the parity test, because it becomes stricter |
| S3, the reentrancy corollary and the `ProcessSpec` funcs | The contract covers `ProcessSpec` funcs; the driver never holds its lock across a `Process` call; spawn and respawn are serialized on the loop goroutine; the fleet order is recorded; `Reload` stays in the driver and reads `Snapshot`, so nothing re-enters |
| S4, `Exited` and `Started` cannot carry today's messages | `Exit{Err, Requested, Restarts, RetryIn}` and `Start{How StartKind, Restarts, Reason, Child}` |
| S5, PR 1b's size | Split into 1b (callers) and 1c (in-package qualification with `gofmt -r`), with the counts (182 and 169) |
| S6, facts expired, cites pre-#799 | Header at `76a7db9`; #799 marked merged; Move 2 re-cited; the "#799 open" ordering line removed; D1 counted and put up as a decision |
| S7, `Status` as a torn read | One `Snapshot` at the top of `Status()` |
| N1, the `TryLock` test | Single-goroutine only, kept apart from the `-race` test, unlocks after each probe, and claims only the scripted calls |
| N2, `Tick` delays exit detection | Stated, with a deadline below the ticker period |
| N3, MS-8's interface has no ticket | R0 |
| N4, "additive" | "independent of 2c and 2d" |
| N5, #799's file list | "among others", with `identity.go` named |
| N6, interface shape | Noted: optional interfaces only when a second driver needs them |
| N7, `services-list.md` 3.4 item 3 | Amended in PR 2d with D1's wording |

## 9. Review of revision 3 (`c939206`), item by item

Each item was checked at `76a7db9` before anything changed. All of them held.

| item | what changed |
|---|---|
| 1, readiness gates the generation, not the reading | Readings are tagged with their generation; `Started` clears the reading; a reading from another generation is dropped; three readiness tests, including the failed first reading and the late `Reload` reading |
| 2, lock order reversed | Section 2 records the real nesting, `s.mu` then `m.mu`, and that the `manager.go:64-68` comment does not match `Regenerate`. The contract states it, and states `Snapshot` before `s.mu` for `Ready()` and `Status()`; PR 2c corrects the comment |
| 3, PR 1b and 1c scope | 27 files outside `internal/daemon` (14 non-test, 13 test) counted and named as PR 1b's scope; the aliases stay through 1b so 1c's build checks nothing was missed |
| D1, `s.grace` missing | `s.grace` is set before `Start`, so it is in the configuration class (now 15) and reaches `Process` as `ProcessSpec.Grace`. The live-state edits stay at five, inside the operator's grant |
| low, the API omits `Restart` and `Stop` | Both added to the API block |

