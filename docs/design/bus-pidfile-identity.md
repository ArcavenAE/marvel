# Bus pidfile identity: prove the process before signalling it

Proposal, 2026-10-09. Issue: #794. Code read at marvel `9c1541e` (`9c1541ea858afb8fbf971e18833332d2bffa803c`).

- Author: the architect seat, team arcaven.
- Builder: red tests first, then green, after this design merges.
- Requirement: MS-9 in `docs/design/services-shape-requirements.md` ("The pidfile proves the child"). Section 4.5 is how this design meets its seam clause.
- V11 is not ruled. Section 5 designs both options behind one switch; everything else here holds under either.

## 0. Why

After a crash or a reboot, a daemon restart can send SIGTERM and then SIGKILL to an unrelated process group. The bus supervisor treats a pid in its pidfile as its own broker because the pidfile parses and `kill(pid, 0)` succeeds. A pid the kernel has given to another process passes both checks. Marvel already refuses to adopt a stranger on the port (`supervisor.go:262-264`). This design holds the pid to the same standard: no signal reaches a process marvel cannot show it started.

## 1. Premises, checked at `9c1541e`

| claim | where | checked |
|---|---|---|
| A live, non-listening pidfile pid gets SIGTERM, then SIGKILL, to `-pid` | `internal/bus/supervisor.go:256-260` | read |
| Adoption sends SIGHUP to the pidfile pid | `supervisor.go:251` | read |
| `terminate` signals `-pid` with TERM, then KILL, polling with signal 0 | `supervisor.go:517-526` | read |
| The only identity proof is `pidfile.Read` plus signal 0 | `pidFileAlive`, `supervisor.go:825-834`; `probePID`, `:839` | read |
| The pidfile holds a bare pid and a newline | `internal/workload/process.go:89-95` | read |
| The child leads its own process group | `workload/process.go:82` (`Setpgid: true`) | read |
| `procargs` reads exact argv; the darwin parser reads past the executable path and discards it | `internal/procargs/procargs.go`, `ParseProcargs2` | read |
| darwin: `kern.proc.pid` gives `p_starttime` and `e_pgid`; `kern.procargs2` gives the exec path | `golang.org/x/sys` v0.48.0 (`KinfoProc.Proc.P_starttime`, `Eproc.Pgid`) | MEASURED on macOS 26.5.2, arm64 laptop, go1.26.5, with a short Go program that calls `unix.SysctlKinfoProc("kern.proc.pid", pid)` and `unix.SysctlRaw("kern.procargs2", pid)` and prints `P_starttime` and the exec path: a `/bin/sleep` child read 1791533001.303086, matching `ps -o lstart` to the second, and exec path `/bin/sleep`. The reviewer reproduced it on the same OS version with two children, whose values differed and whose repeat reads were identical. |
| darwin: a pid that does not exist returns an error from `kern.proc.pid`, not an empty record | the same program and conditions, pid 999999 | MEASURED: `input/output error`. The reviewer saw the same for pid 999999 and for a child after it exited. |
| linux: `/proc/<pid>/stat` field 22 is the start time in clock ticks since boot; `/proc/sys/kernel/random/boot_id` names the boot; `/proc/<pid>/exe` is the executable | proc(5) | NOT MEASURED here (no Linux host). CI runs the tests on ubuntu. |

## 2. Every signalling site

Enumerated at `9c1541e` with this command, which returns 43 lines:

```
grep -rnE 'syscall\.Kill|unix\.Kill|\.Signal\(|Process\.Kill|\.Kill\(\)|FindProcess|pkill|"kill|CommandContext' --include='*.go' . | grep -v _test.go
```

**In scope: the bus broker's pid, all in `internal/bus/supervisor.go`.**

| site | signal | pid source | today's proof | rule after this design |
|---|---|---|---|---|
| `:251` adopt | SIGHUP to `pid` | pidfile | signal 0 | proven (section 4), or V11 (section 5) for a legacy pidfile |
| `:258` wedged | SIGTERM to `-pid` | pidfile | signal 0 | proven, and group only when `pgid == pid` (section 4.3) |
| `:260` wedged | SIGKILL to `-pid` | pidfile | signal 0 | proven again immediately before; group rule as above |
| `:417` watch | signal 0 to `pid`, every 2 s, adopted broker only | `s.pid` | none | an identity read; a start-time change counts as the broker exiting |
| `:518` terminate | SIGTERM to `-pid` | `s.pid` (own child or adopted) | none | proven immediately before; group rule |
| `:520`, `:523` terminate | signal 0 poll | `s.pid` | none | identity read |
| `:524` terminate | SIGKILL to `-pid` | `s.pid` | signal 0 | proven immediately before; group rule |
| `:540` `Reload` | SIGHUP to `pid` | `s.pid` | `ready` flag | proven immediately before |
| `:654` structural miss | SIGHUP to `pid` | `s.pid` | `pid != 0` | proven immediately before |
| `:826`, `:839` | pidfile read, signal 0 | pidfile | this is the proof | replaced by `Prober.Prove` (section 4.5) |

`terminate` is called from `Restart` (`:501`) and `Stop` (`:590`). Between a child's exit and the next spawn, `s.pid` can name a pid the kernel has reaped. That is why every site re-proves rather than trusting a proof taken at `Start`.

**Out of scope, recorded so the list is complete:**

- `internal/daemon/daemon.go:915-919` (`checkPidFileFree`) and `cmd/marvel/daemon_lock_hint.go:20-24`. These probe the daemon's own pidfile with signal 0 and only ever refuse or hint; neither sends a signal that does anything. A reused pid makes a second daemon refuse when it need not (fail closed). #744 is the adjacent issue for the same guard.
- `internal/runtime/codex_home.go:246` (`cmd.Process.Kill` on a child it holds).
- `internal/tmux/tmuxtest/sweep.go:71` (test helper).
- `exec.CommandContext` children, which the runtime kills when their context ends: `internal/runtime/codex_home.go:226`, `internal/view/view.go:295` and `:331`, `internal/tmux/absence.go:65`, `internal/tmux/driver.go:197` (with `WaitDelay` at `:201`), and `internal/api/trusted_folders.go:34`. Each signals its own child before that child is reaped, so a reused pid cannot redirect it, the same reasoning as `codex_home.go:246`.
- The other 20 lines send no signal to a pid. They are tmux `kill-pane`, `kill-session` and `kill-server` calls, which name panes, sessions and servers (`internal/tmux/driver.go:640-691`, `tmuxtest/sweep.go:86`), and strings that contain "kill": messages and errors in `session/manager.go` and `runtime/instance_tmux.go`, JSON field names, CLI help, and a simulator function name.

## 3. What the pidfile records

### 3.1 Shape

Keep `nats-server.pid` exactly as it is: the bare pid and a newline. Add a sidecar `nats-server.pid.identity`, JSON, mode 0600:

```json
{
  "schema": 1,
  "pid": 41237,
  "start": "darwin:1791533001.303086",
  "exe": "/opt/homebrew/Cellar/nats-server/2.14.6/bin/nats-server",
  "argv": ["/opt/homebrew/bin/nats-server", "-c", "<StateDir>/nats/nats-server.conf"],
  "written_at": "2026-10-09T08:30:00Z"
}
```

- `start` is platform-tagged and compared as a string: `darwin:<sec>.<usec>` from `p_starttime`, and `linux:<boot_id>:<ticks>` from `boot_id` and stat field 22. A state directory copied between systems never matches.
- `exe` is what the kernel reports for the running process at write time: darwin, the `kern.procargs2` exec path; linux, `readlink /proc/<pid>/exe` with a trailing ` (deleted)` removed. It is read from the process, not copied from the spawn spec, so the write and the later check use the same source.
- `argv` is `procargs.Read(pid)` at write time.

**Why a sidecar, not a richer pidfile.** `pidfile.Parse` rejects any text after the number, by design (`internal/pidfile/pidfile.go:12-18`). A richer pidfile would read as no pidfile to an older binary after a downgrade, which then finds the listener answering and refuses it as a stranger. With a sidecar, an older binary keeps its current behavior, and the newer one has the proof.

**Write order.** `workload.Start` writes the pidfile today. After `cmd.Start`, the driver reads the child's identity, writes the sidecar to a temporary name, renames it into place, then writes the pidfile. A crash between the two leaves a sidecar with no pidfile. The next start ignores it: a pidfile is what names a candidate, and a missing pidfile means "no candidate". `Stop(false)` removes both. A stale sidecar whose `pid` differs from the pidfile's is a mismatch (section 4.2).

### 3.2 Reading a live process's identity

One function per platform, behind one seam. It lives in `internal/childproof` beside the proven child (section 4.5), as the default `read` seam that `New` installs in a `Prober`:

```go
// readIdentity reads what the kernel says about pid now. Any error, including
// "no such process", is returned; the caller treats an error as unproven.
func readIdentity(pid int) (Identity, error)

type Identity struct {
	Start string   // platform-tagged, as recorded
	Exe   string
	Argv  []string
	Pgid  int
}
```

- **darwin.** `unix.SysctlKinfoProc("kern.proc.pid", pid)` for `Proc.P_starttime` and `Eproc.Pgid`; `unix.SysctlRaw("kern.procargs2", pid)` for the exec path and argv. `procargs` gains a function that returns the exec path it already parses. No cgo.
- **linux.** `/proc/<pid>/stat` for field 22 (start ticks) and field 5 (pgrp), parsed after the last `)` as `procstat` does (`internal/procstat/proc.go:22-27`); `/proc/sys/kernel/random/boot_id`; `readlink /proc/<pid>/exe`; `/proc/<pid>/cmdline`.
- **other systems.** `readIdentity` returns an error. Nothing is ever proven, so the broker is never adopted or signalled; marvel spawns afresh when the port is free and refuses when it is held. That is fail closed.

## 4. The match rule

The pid that is signalled only ever comes from `pidfile.Parse` (a positive decimal that fits 32 bits, `internal/pidfile/pidfile.go:12-18`) or from `cmd.Process.Pid`, never from the sidecar. A wide or negative `pid` in the sidecar cannot match under rule 1.

### 4.1 Proven

A pidfile pid is proven when all of these hold:

1. the sidecar parses, its `schema` is 1, and its `pid` equals the pidfile's;
2. `readIdentity(pid)` succeeds;
3. `start` is equal, byte for byte;
4. `exe` is equal;
5. `argv` is equal, element by element.

Start time is the identity: on one boot, the pair (pid, start time) names one process. Executable and argv add nothing to uniqueness, but they turn a recording mistake into a refusal rather than a signal, and they give the event a readable reason.

`s.pid` for a child this daemon spawned has its identity recorded at spawn, held in memory, and re-proven the same way before each signal. A child that has exited and been reaped fails rule 2 or rule 3.

### 4.2 Outcomes at `Start`

| pidfile | sidecar | process | listener | action |
|---|---|---|---|---|
| none | any | | no | spawn |
| none | any | | yes | refuse as a stranger (today's `:262-264`) |
| pid | matches | proven | yes | adopt; SIGHUP to `pid` (`:251`) |
| pid | matches | proven | no | ours and wedged: terminate (section 4.3), remove both files, spawn |
| pid | matches or present | not proven: dead, or start, exe or argv differ | no | stale: remove both files, signal nothing, emit `bus.pidfile-stale` with the reason, spawn |
| pid | matches or present | not proven | yes | stale as above, signal nothing, and report "port held, child unproven" (MS-9) in `bus status` and in one `bus.pidfile-unproven` event; the bus stays down, with no crash loop |
| pid | **none** (legacy) | | | **V11** (section 5) |

### 4.3 Group signals

`-pid` addresses the process group whose id is `pid`. `workload.Start` makes the broker a group leader, so for a proven broker `pgid == pid`. Before any `-pid` signal, the supervisor re-reads the identity. If the proof fails, it sends nothing. A group signal also needs `pid > 1`: `Parse` accepts 1, and `kill(-1, ...)` is the every-process broadcast, not a group. If the proof holds but `pgid != pid` (something moved the broker into another group), it signals `pid` alone. The group is signalled only when the proven process leads it.

### 4.4 The adopted-broker watch

`:417` polls signal 0 every 2 s. It becomes an identity read with the start-time compare. A pid that is alive but now belongs to another process reads as "adopted nats-server exited", the same crash path as today, and the restart does not signal it, because `terminate` re-proves first.

### 4.5 The signal seam takes a proven child (MS-9)

MS-9 asks for "one injectable function that takes a proven child". Unexported fields keep a composite literal out of other packages but not out of the package that declares the type, so the proven child lives in a small package of its own, `internal/childproof`. `internal/bus` cannot build a `Child` except through the constructors below, and `childproof` itself is one file with no other code in it.

```go
package childproof

// Child is a process whose identity was read and matched. Its fields are
// unexported, so only Prove and AdoptLegacy build one. It records the
// Prober that built it. The zero value has pid 0 and no Prober, and Signal
// refuses it.
type Child struct {
	pid int
	id  Identity
	by  *Prober
}

// Prober holds the two seams. Both are unexported, so code outside this
// package cannot build a Prober whose Read lies or whose Kill is real by
// accident; it gets one from New or NewForTest.
type Prober struct {
	read func(pid int) (Identity, error)
	kill func(pid int, sig syscall.Signal) error
}

// New returns the production Prober: the platform reader (section 3.2) and
// syscall.Kill.
func New() *Prober

// NewForTest returns a Prober with the given seams. It returns an error
// unless testing.Testing() reports a test binary (Go 1.21 and later; the
// module is on 1.26), or when either seam is nil, so a test cannot get the
// real kill by leaving one unset. testing.Testing() is also true for
// non-test code linked into a test binary, so a source test (section 7,
// item 9) fails on any reference to NewForTest outside a _test.go file.
func NewForTest(read func(int) (Identity, error), kill func(int, syscall.Signal) error) (*Prober, error)

// Prove reads pid's live identity and returns a Child when it equals want
// (section 4.1, rules 2 to 5). Like AdoptLegacy and Signal, it returns an
// error for a nil Prober or a nil seam rather than panicking, so a wiring
// mistake leaves the daemon up with the bus unproven.
func (p *Prober) Prove(pid int, want Identity) (Child, error)

// AdoptLegacy is the one constructor without a recorded identity: V11 (a)
// only. It returns a Child when the live executable's base name is
// nats-server and argv carries -c and conf. Deleted if V11 is ruled (b).
func (p *Prober) AdoptLegacy(pid int, conf string) (Child, error)

// Signal refuses a nil Prober, a nil seam, a Child built by another Prober,
// and pid <= 1, for every signal. It then re-reads the identity, refuses on
// any difference, and sends sig: to -pid when group is set and the live
// pgid == pid, otherwise to pid.
func (p *Prober) Signal(c Child, sig syscall.Signal, group bool) error

// Identity returns what the proof read, so the caller can write the sidecar.
func (c Child) Identity() Identity
```

The bus supervisor takes a `*Prober` at construction: `New()` in production, `NewForTest` in its tests. Binding a `Child` to its `Prober` means a `Child` proven by a test's fake reader can never be signalled through the production `Prober`, or the other way round.

Every site in section 2 calls `Signal`. Because `Signal` re-reads the identity itself, "re-proven immediately before" is part of the seam, not a rule each site has to remember. The raw `kill` is only ever called inside `Signal`. Refusing pid 0 and pid 1 for every signal matters for more than the group form: `kill(0, sig)` signals the caller's own process group, so a zero `Child` would otherwise reach the daemon.

### 4.6 What the rule does not close

- **A corrupt sidecar** fails rule 1 and reaches the not-proven rows, which signal nothing. That is fail closed.
- **linux resolution.** Stat field 22 counts clock ticks, commonly 10 ms. Two processes on one boot that share a pid and start within one tick of each other read alike. A pid reused that fast must also match the recorded executable and argv to be signalled.
- **Proof to kill.** A pid can be reused between `Signal`'s re-read and its `kill`. On linux, `pidfd_open` at the proof and `pidfd_send_signal` would close that window; this design leaves it as a follow-up. On darwin nothing closes it. The window has no fixed bound, since a scheduler can stall the daemon between the two calls, but for a wrong signal the original broker must exit, be reaped, and its pid be handed to a new process inside it. The current code trusts the same chain across the whole gap between a pidfile write and a restart.

## 5. V11: a pidfile from a binary that wrote no sidecar

This is the first daemon start after the upgrade that adds the sidecar. The running broker was spawned by the old binary, so its pidfile holds a bare pid and nothing proves it. It is the common case for every cluster, once. V11 is an open split in `docs/design/services-shape-requirements.md` section 6: plurality (b), 5 to 4, one abstention, and the operator has not ruled it. MS-9 asks that the legacy handling ship in the same PR as the proof. The build carries both options behind one switch:

```go
// legacyPidfile decides a pidfile with no identity sidecar (V11). Set in code
// by the ruling, never by config: an operator cannot widen what marvel signals.
const legacyPidfile = <the ruled value> // legacyAdoptOnExecMatch or legacyRefuseUnproven
```

**(a) `legacyAdoptOnExecMatch`.** If the listener answers and the process's `exe` base name is `nats-server`, and its argv carries `-c` followed by this daemon's conf path, then:
- build the `Child` with `AdoptLegacy(pid, conf)`, the one unproven adoption, named so a reader sees it;
- send SIGHUP through `Signal(c, SIGHUP, false)`, to `pid` only, never to the group. `Signal` re-reads against the identity `AdoptLegacy` read a moment earlier, so this one SIGHUP rests on argv and executable, not start time;
- write the sidecar from `c.Identity()` at once, so the broker is proven from then on.

Otherwise, signal nothing. Remove the pidfile; spawn if the port is free, and refuse as a stranger if it is held.

**(b) `legacyRefuseUnproven`.** Neither adopt nor signal. If the listener answers, the bus stays down and reports "port held, child unproven". This shows in `bus status` and in a `bus.pidfile-unproven` event, which names the pid, the port and the remedy: stop the old broker by hand, or set `bus.managed: false` and `bus.url` to adopt it explicitly. If the listener does not answer, remove the pidfile and spawn.

**What `AdoptLegacy` cannot tell apart.** It checks only the executable's base name and `-c <conf>`, so it also adopts a nats-server someone started by hand with marvel's conf. For that process the worst case is:
- the adoption SIGHUP makes it reload marvel's freshly rendered authorization;
- the sidecar written from it makes it proven from then on;
- a later `terminate` sends TERM and then KILL to its process group. A process started from a shell leads its own job group, so the group can include its pipeline partners.

The operator weighs this in V11 alongside the cost of (b).

**What each costs.**
- (a) keeps every session's bus connection across the upgrade. It adopts on argv and executable without start time, which is weaker evidence than the MS-9 proof. Its full worst case: the sidecar it writes at adoption makes the adopted process "proven" from then on, so a wrong adopt is not limited to one SIGHUP. `terminate` can later send that process TERM and KILL, and send them to its group when it leads one.
- (b) never acts on an unproven process. It strands the live broker at the first upgrade on every cluster: new spawns hold on the bus gate until the operator acts, which is one manual stop per cluster.

**Side (b)'s argument, and my answer.** Side (b) says (a) "adopts on evidence that is not yet the MS-9 proof, and that a wrong adopt of a stateful child risks its data". The evidence (a) requires is a nats-server executable whose argv carries `-c` and this daemon's own conf path, while that daemon's listener answers. The conf names the JetStream `store_dir` (`internal/bus/render.go:157`). A process that matches is therefore already serving this daemon's port from this daemon's store. A wrong adopt can only pick a process that holds the data at risk already, and later stopping it is what marvel does to its own broker. The case this does not cover is a nats-server someone started by hand with marvel's conf; (a) would take that process over.

**What is V11-independent.** Sections 2, 3, 4 and 6, and the new-format paths in section 7, are the same under either option. The switch touches one branch of `Start`: pidfile present, sidecar absent.

**#339.** A reexec keeps the broker running for the successor to adopt (`Stop(true)`, `:584-587`). With the sidecar, that adoption is proven and unchanged in effect. The first reexec onto this build is exactly the V11 case, so under (b) it would stop the bus where today it adopts. The leaf-seed loss #339 tracks is a separate defect on the same path, and this design does not change it.

## 6. Events and status

- `bus.pidfile-stale` (warning): pid, reason (`dead`, `start`, `exe`, `argv`, `sidecar-pid`), the action taken (removed, spawned or refused). Emitted once per start.
- `bus.pidfile-unproven` (warning): pid, port, remedy. Emitted for a mismatch with the listener answering, and for a legacy pidfile under V11 (b).
- `bus status` gains `pidfile: proven | stale | legacy | none`.

## 7. Test plan (red first)

Every test signals only processes the test started, or none. Two kinds of test meet that in different ways.

**Unit tests in `internal/bus`** build their `Prober` with `NewForTest` and a `kill` that fails the test on any pid outside the children the test registered. No fixture uses a literal pid such as 4242, which can name a live process; every fixture pid is a child the test started. A test that misses the swap, or a mutant that drops a guard, fails instead of signalling.

**Tests in the tree that start a real broker or signal a process.** These were found with this command at `9c1541e`, which returns 65 lines:

```
grep -rnE 'newTestSupervisor|NewSupervisor|NewAdopted|attachTestBus|syscall\.Kill|Process\.Kill' --include='*_test.go' .
```

Each line is in exactly one class:

| class | lines | count | what the build does |
|---|---|---|---|
| The `newTestSupervisor` helper (comment, signature, its `NewSupervisor`) | `internal/bus/supervisor_test.go:80`, `:83`, `:101` | 3 | the single registration point: it builds the supervisor with a `NewForTest` `Prober` and sets the spawn hook |
| Helper callers that `Start` a real broker | `supervisor_test.go:123`, `:172`, `:207`, `:224`, `:236`, `:264`, `:305`, `:365`; `provision_test.go:24`, `:107`; `provision_transport_test.go:60`; `supervisor_env_test.go:74`; `health_test.go:42`, `:127` | 14 | covered by the helper; no change at the call site |
| Helper callers that never call `Start` | `test_bounds_test.go:26`, `:41`; `leaf_duration_test.go:21`, `:43`, `:118`; `leaf_observed_test.go:63` | 6 | no broker, no signal |
| Direct `NewSupervisor` | `role_users_test.go:298`; `supervisor_test.go:185`; `provision_test.go:138`; `internal/daemon/bus_leaf_restart_test.go:66`; `supervisor_test.go:287` (a test name) and `:294` (missing binary, no `Start`) | 6 | the first three are in `internal/bus` and attach the same test `Prober` as the helper, sharing the test's registry, so the second supervisor at `:185` and `:138` can signal the broker the first one spawned. `bus_leaf_restart_test.go:66` is the only real broker outside `internal/bus`; it cannot reach the unexported hook, so it builds with `New()`, whose real signals reach only the broker its own supervisor spawned and proved. The last two start nothing |
| Test probes with signal 0 on the test's own broker | `supervisor_test.go:150`, `:157`, `:160`, `:178`, `:202`, `:258`, `:357` | 7 | observation only; unchanged |
| Direct test kills of the test's own child, outside the seam | `supervisor_test.go:181` (Cleanup, `-pid` SIGKILL), `:241` (SIGKILL to simulate a crash), `:371` (SIGKILL); `provision_test.go:120` (Cleanup, `-pid` SIGKILL); `health_test.go:220` (Cleanup of the bare broker the test started at `:207`) | 5 | each signals a pid the test spawned and read itself; unchanged, and named here so the seam's "only inside `Signal`" claim is scoped to non-test code |
| No broker process: `NewAdopted` and `attachTestBus` | `render_test.go:434`; `internal/daemon/status_test.go:173`; `bus_status_test.go:22`; `bus_test.go:16`, `:18`, `:44`, `:91`; `leaf_no_hub_test.go:17` | 8 | `NewAdopted` records a URL and `attachTestBus` builds only a `Manager`; neither spawns or signals |
| Own-child signals in other packages, never the broker | `cmd/marvel/daemon_lock_hint_test.go:176`; `daemon_fastpath_test.go:101`, `:186`; `internal/daemon/shutdown_frozen_tmux_test.go:69`, `:72`, `:88`; `start_frozen_tmux_test.go:33`, `:36`; `internal/view/seal_test.go:183`; `internal/tmux/absence_plant_test.go:61`, `:62`, `:105`; `absence_test.go:262`; `internal/workload/process_test.go:102`; `internal/session/tmux_timeout_test.go:52`, `:56` | 16 | each targets a child or a tmux server the test started; out of scope |

The total is 3 + 14 + 6 + 6 + 7 + 5 + 8 + 16 = 65. The registration mechanism: the spawn hook registers `cmd.Process.Pid` in the test's registry as soon as the broker starts, and the test's `kill` forwards to `syscall.Kill` for registered pids only and fails the test on any other. No test in `internal/daemon` or `cmd/marvel` reaches `daemon.go:3381`. The grep cannot see the `attachServices` tests in `internal/daemon/bus_test.go` (`:135`, `:157`, `:185`, `:196`); each configures an adopted bus, so `attachBus` returns at `daemon.go:3368-3372` before the managed supervisor is built.

1. **Seams.** the `Prober`'s `read` replaces `probePID` as the identity seam, and its `kill` is the signal seam behind `Signal` (section 4.5); the test version records calls. `childproof`'s own tests cover `Signal` refusing pid 0 and pid 1 for every signal, a zero `Child`, a `Child` from another `Prober`, and a nil seam; `NewForTest` refusing a nil seam; and `AdoptLegacy` refusing a wrong executable or a missing `-c conf`. Unit tests drive both fakes; the existing `pidfile_alive_test.go` moves to the new seam.
2. **Reused pid, not listening** (the #794 case). The fake `read` returns a start time different from the sidecar's; the listener is closed. Assert no `kill` call, both files removed, one `bus.pidfile-stale` with reason `start`, and a spawn.
3. **Reused pid, real kernel.** The test starts its own `sleep` with `Setpgid` and records its real identity. It then rewrites the sidecar's `start` to another value and calls `Start`. Assert the `sleep` is still alive afterwards (`readIdentity` succeeds with the real start time) and received nothing: its state shows not stopped, and the test reaps it itself.
4. **HUP path.** Adoption with a proven identity sends exactly one SIGHUP to `pid`. A mismatch at adoption sends none. `Reload` and the structural SIGHUP (`:540`, `:654`) each send none when the identity read fails between the proof and the signal: the fake changes its answer between calls.
5. **Terminate path.** A proven broker with `pgid == pid` gets TERM, then KILL, to `-pid`. With `pgid != pid`, both go to `pid`. When the identity changes between TERM and KILL, KILL is not sent.
6. **Old-format pidfile.** A bare pid with no sidecar. Run once per switch value:
   - under (a), the matching argv and exe case adopts with one SIGHUP to `pid` and writes a sidecar, and the non-matching case signals nothing;
   - under (b), it signals nothing and reports unproven when the listener answers.
   Table-driven over both constants, so whichever is ruled, the other stays tested until it is deleted.
7. **Write order.** The sidecar exists and parses before the pidfile does. A sidecar whose pid differs from the pidfile's reads as stale with reason `sidecar-pid`.
8. **Platforms.** `readIdentity` on the test's own child returns a start time that is stable across two reads and different for a second child. This runs on darwin locally and on ubuntu in CI; the linux reads are unmeasured until that test runs there.
9. **Source check.** A test in `internal/childproof` parses every non-test `.go` file in the module and fails on any reference to `NewForTest` outside a `_test.go` file, other than its own declaration.

## 8. Plan

One flat ticket for the builder, with red tests 2 to 9 before green. The builder ships whichever V11 value is ruled.

My recommendation is (a), against the (b) plurality, for the reasons in section 5: (b) costs a manual stop on every cluster, and the cost of a wrong adopt under (a) falls on a process already serving this daemon's port from this daemon's store. This recommendation is valid until 2026-10-23 or until V11 is ruled, whichever comes first; the architect re-checks it then.
