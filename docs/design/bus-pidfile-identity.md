# Bus pidfile identity: prove the process before signalling it

Proposal, 2026-10-09. Issue: #794. Code read at marvel `9c1541e` (`9c1541ea858afb8fbf971e18833332d2bffa803c`).

- Author: the architect seat, team arcaven.
- Builder: red tests first, then green, after this design merges.
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
| darwin: `kern.proc.pid` gives `p_starttime` and `e_pgid`; `kern.procargs2` gives the exec path | `golang.org/x/sys` v0.48.0 (`KinfoProc.Proc.P_starttime`, `Eproc.Pgid`) | MEASURED: a scratch program on darwin arm64 read `p_starttime` 1791533001.303086 for a `/bin/sleep` child, matching `ps -o lstart` to the second, and exec path `/bin/sleep` |
| darwin: a pid that does not exist returns an error from `kern.proc.pid`, not an empty record | same program, pid 999999 | MEASURED: `input/output error` |
| linux: `/proc/<pid>/stat` field 22 is the start time in clock ticks since boot; `/proc/sys/kernel/random/boot_id` names the boot; `/proc/<pid>/exe` is the executable | proc(5) | NOT MEASURED here (no Linux host). CI runs the tests on ubuntu. |

## 2. Every signalling site

Enumerated at `9c1541e` with:

```
grep -rnE 'syscall\.Kill|unix\.Kill|\.Signal\(|Process\.Kill|\.Kill\(\)|FindProcess|pkill|"kill' --include='*.go' . | grep -v _test.go
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
| `:826`, `:839` | pidfile read, signal 0 | pidfile | this is the proof | replaced by `proveIdentity` |

`terminate` is called from `Restart` (`:501`) and `Stop` (`:590`). Between a child's exit and the next spawn, `s.pid` can name a pid the kernel has reaped. That is why every site re-proves rather than trusting a proof taken at `Start`.

**Out of scope, recorded so the list is complete:**

- `internal/daemon/daemon.go:915-919` (`checkPidFileFree`) and `cmd/marvel/daemon_lock_hint.go:20-24`. These probe the daemon's own pidfile with signal 0 and only ever refuse or hint; neither sends a signal that does anything. A reused pid makes a second daemon refuse when it need not (fail closed). #744 is the adjacent issue for the same guard.
- `internal/runtime/codex_home.go:246` (`cmd.Process.Kill` on a child it holds).
- `internal/tmux/tmuxtest/sweep.go:71` (test helper).
- The other 20 of the command's 37 lines send no signal to a pid. They are tmux `kill-pane`, `kill-session` and `kill-server` calls, which name panes, sessions and servers (`internal/tmux/driver.go:640-691`, `tmuxtest/sweep.go:86`), and strings that contain "kill": messages and errors in `session/manager.go` and `runtime/instance_tmux.go`, JSON field names, CLI help, and a simulator function name.

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

One function per platform, behind one seam:

```go
// readIdentity reads what the kernel says about pid now. Any error, including
// "no such process", is returned; the caller treats an error as unproven.
var readIdentity = func(pid int) (procIdentity, error)

type procIdentity struct {
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
| pid | matches or present | not proven | yes | stale as above, then refuse as a stranger: something holds the port and it is not the recorded broker |
| pid | **none** (legacy) | | | **V11** (section 5) |

### 4.3 Group signals

`-pid` addresses the process group whose id is `pid`. `workload.Start` makes the broker a group leader, so for a proven broker `pgid == pid`. Before any `-pid` signal, the supervisor re-reads the identity. If the proof fails, it sends nothing. If the proof holds but `pgid != pid` (something moved the broker into another group), it signals `pid` alone. The group is signalled only when the proven process leads it.

### 4.4 The adopted-broker watch

`:417` polls signal 0 every 2 s. It becomes an identity read with the start-time compare. A pid that is alive but now belongs to another process reads as "adopted nats-server exited", the same crash path as today, and the restart does not signal it, because `terminate` re-proves first.

## 5. V11: a pidfile from a binary that wrote no sidecar

This is the first daemon start after the upgrade that adds the sidecar. The running broker was spawned by the old binary, so its pidfile holds a bare pid and nothing proves it. It is the common case for every cluster, once. The services review left V11 open, and the operator has not ruled it (`svc-v11-pidfile`). The build carries both options behind one switch:

```go
// legacyPidfile decides a pidfile with no identity sidecar (V11). Set in code
// by the ruling, never by config: an operator cannot widen what marvel signals.
const legacyPidfile = legacyAdoptOnExecMatch // or legacyRefuseUnproven
```

**(a) `legacyAdoptOnExecMatch`.** If the listener answers and the process's `exe` base name is `nats-server`, and its argv carries `-c` followed by this daemon's conf path, then:
- adopt once;
- send SIGHUP to `pid` only, never to the group;
- write the sidecar from `readIdentity(pid)` at once, so the broker is proven from then on.

Otherwise, signal nothing. Remove the pidfile; spawn if the port is free, and refuse as a stranger if it is held.

**(b) `legacyRefuseUnproven`.** Neither adopt nor signal. If the listener answers, the bus stays down and reports "port held, child unproven". This shows in `bus status` and in a `bus.pidfile-unproven` event, which names the pid, the port and the remedy: stop the old broker by hand, or set `bus.managed: false` and `bus.url` to adopt it explicitly. If the listener does not answer, remove the pidfile and spawn.

**What each costs.**
- (a) keeps every session's bus connection across the upgrade, and the check it relies on is argv and executable without start time. A reused pid would have to be a nats-server started with this daemon's own conf path for (a) to adopt it wrongly. Even then it sends only a SIGHUP, which a nats-server answers by re-reading its config.
- (b) never acts on an unproven process. It strands the live broker at the first upgrade on every cluster: new spawns hold on the bus gate until the operator acts, which is a planned outage per cluster.

**What is V11-independent.** Sections 2, 3, 4 and 6, and the new-format paths in section 7, are the same under either option. The switch touches one branch of `Start`: pidfile present, sidecar absent.

**#339.** A reexec keeps the broker running for the successor to adopt (`Stop(true)`, `:584-587`). With the sidecar, that adoption is proven and unchanged in effect. The first reexec onto this build is exactly the V11 case, so under (b) it would stop the bus where today it adopts. The leaf-seed loss #339 tracks is a separate defect on the same path, and this design does not change it.

## 6. Events and status

- `bus.pidfile-stale` (warning): pid, reason (`dead`, `start`, `exe`, `argv`, `sidecar-pid`), the action taken (removed, spawned or refused). Emitted once per start.
- `bus.pidfile-unproven` (warning, V11 (b) only): pid, port, remedy.
- `bus status` gains `pidfile: proven | stale | legacy | none`.

## 7. Test plan (red first)

Every test signals only processes the test started, or none.

1. **Seams.** `readIdentity` replaces `probePID` as the identity seam, and a `sendSignal func(pid int, sig syscall.Signal) error` seam records calls. Unit tests drive both fakes; the existing `pidfile_alive_test.go` moves to the new seam.
2. **Reused pid, not listening** (the #794 case). The fake `readIdentity` returns a start time different from the sidecar's; the listener is closed. Assert no `sendSignal` call, both files removed, one `bus.pidfile-stale` with reason `start`, and a spawn.
3. **Reused pid, real kernel.** The test starts its own `sleep` with `Setpgid` and records its real identity. It then rewrites the sidecar's `start` to another value and calls `Start`. Assert the `sleep` is still alive afterwards (`readIdentity` succeeds with the real start time) and received nothing: its state shows not stopped, and the test reaps it itself.
4. **HUP path.** Adoption with a proven identity sends exactly one SIGHUP to `pid`. A mismatch at adoption sends none. `Reload` and the structural SIGHUP (`:540`, `:654`) each send none when the identity read fails between the proof and the signal: the fake changes its answer between calls.
5. **Terminate path.** A proven broker with `pgid == pid` gets TERM, then KILL, to `-pid`. With `pgid != pid`, both go to `pid`. When the identity changes between TERM and KILL, KILL is not sent.
6. **Old-format pidfile.** A bare pid with no sidecar. Run once per switch value:
   - under (a), the matching argv and exe case adopts with one SIGHUP to `pid` and writes a sidecar, and the non-matching case signals nothing;
   - under (b), it signals nothing and reports unproven when the listener answers.
   Table-driven over both constants, so whichever is ruled, the other stays tested until it is deleted.
7. **Write order.** The sidecar exists and parses before the pidfile does. A sidecar whose pid differs from the pidfile's reads as stale with reason `sidecar-pid`.
8. **Platforms.** `readIdentity` on the test's own child returns a start time that is stable across two reads and different for a second child. This runs on darwin locally and on ubuntu in CI; the linux reads are unmeasured until that test runs there.

## 8. Plan

One flat ticket for the builder, with red tests 2 to 8 before green. I recommend the V11 constant ship set to (a), with a code comment naming the open ruling, unless the operator rules before the build starts. The reason is the cost in section 5: (b) takes the bus down on every cluster at its first upgrade, and (a)'s worst case is a SIGHUP to a nats-server started with this daemon's own conf path. If the ruling is (b), the change is the constant and the test that asserts the default. That default is a recommendation, valid until 2026-10-23 or until V11 is ruled, whichever comes first; the architect re-checks it then.
