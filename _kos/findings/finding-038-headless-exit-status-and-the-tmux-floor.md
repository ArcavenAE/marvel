# finding-038: a finished headless run now holds its slot; the tmux exit-status floor is 3.5

Date: 2026-09-12
Serves: `aae-orc-bxeh` (the write path); SP1 and SP2 of `probe-headless-terminal-state.md` (`aae-orc-vsju`)
Measured on: kinu, macOS arm64, tmux 3.7b (host); Linux aarch64 in containers with tmux 3.0a (ubuntu:20.04), 3.2a (22.04), 3.3a (debian bookworm), 3.4 (ubuntu 24.04), 3.5a (debian trixie); marvel `1003277` plus this change
Extends: finding-036 (churn reproduced, consequence 3 non-reproduction on 3.7c), finding-027 (priced), ADR-010 (the ruling), marvel PR #244 (the seam)

## What changed

The churn finding-036 measured stops. A headless role whose pane exits 0 is
marked `succeeded`, keeps its `ExitStatus`, holds its replica slot through
`api.OccupiesReplicaSlot` (the #244 seam), and is never refilled. Everything
else stays a crash: a non-zero exit (recorded as `crashed (exit N)` and
retried under the usual backoff), a signal death, an unknown status, and every
interactive role.

The mechanism is tmux's own: `remain-on-exit` is now the server-global default
on marvel's dedicated tmux server, so a headless window is kept after its
command exits and `pane_dead_status` carries the code; interactive windows are
switched back to close-on-exit per window at creation, which keeps their
contract byte-for-byte. `ReapDead` reads `pane_dead` / `pane_dead_status` in one
`list-panes` call, reclaims the dead pane (fence: only panes carrying the
`@marvel_pane` marker), and maps the result. No shim, no kxce pull.

Under the daemon, end to end (isolated daemon, bolt state, `MARVEL_TMUX_SOCKET`
scoped, three roles: `sh -c 'echo ok; exit 0'` headless, `sh -c 'exit 3'`
headless, `sleep 600` interactive):

```
18:13:44  info     session.created           sp2/jobs-done-g1-0    pane %1
18:13:45  info     session.succeeded         sp2/jobs-done-g1-0    pane %1 exited 0; replica satisfied
18:13:45  warning  session.crashed           sp2/jobs-broken-g1-0  pane %2 gone (exited 3)
18:13:45  warning  health.crashloop-backoff  sp2/jobs              charged 1, suppressed 0; restart #1, backoff until 18:14:45Z
18:14:47  info     session.created           sp2/jobs-broken-g1-0  pane %4       <- the broken one retries
18:14:49  warning  session.crashed           sp2/jobs-broken-g1-0  pane %4 gone (exited 3)
```

`get sessions` reads `succeeded (exit 0)` / `crashed (exit 3)` / `running`;
`plan` reads `done: desired 1 actual 1 steady`; `reap` reports "Nothing to
reap"; the dead panes are gone from `list-panes`. Ten minutes of ticks
re-ran nothing for `done`. Daemon stop (detach) and restart against the same
bolt: the `succeeded` row survives, `long` is adopted, `plan` still steady;
`marvel work` re-apply of the same manifest spawns nothing for `done`.
`scale --replicas 2` on the completed role ran exactly one more job, which
completed and holds its slot (two `succeeded` rows, `actual 2`). A `shift`
re-ran every role at generation 2 and drained generation 1, and the
generation-2 jobs completed and hold: a shift is an explicit rerun, which is
the right reading.

## SP1: the tmux floor, measured

Six versions, macOS and Linux, four cases each through marvel's exact launch
form (`sh -c '...' > sink < /dev/null`, a redirection, so the shell execs and
the status is the harness's own), plus the two consequences that mattered.

| tmux | `pane_dead` | `pane_dead_status` (exit 0/3/7) | status lost (instant exit, 10 to 50 trials) | `pane_dead_signal` (SIGKILL) |
|---|---|---|---|---|
| 3.0a (Linux) | yes | 0 / 3 / 7 | 3/10 | absent |
| 3.2a (Linux) | yes | 0 / 3 / 7 | 2 to 3/10 per form | absent |
| 3.3a (Linux) | yes | 0 / 3 / 7 | **6/30, never arrives later (checked at 9s)** | absent |
| 3.4 (Linux) | yes | 0 / 3 / 7 | 1 to 2/10 per form | absent |
| 3.5a (Linux) | yes | 0 / 3 / 7 | **0/50** | `9` |
| 3.7b (macOS) | yes | 0 / 3 / 7 | **0/80** | `kill` |

Three results:

1. **`pane_dead_status` exists at every version down to 3.0a**, so the route
   survives Linux. The brief's kill condition ("a missing `pane_dead_status`
   is not survivable") does not fire anywhere.
2. **The reliability floor is tmux 3.5.** Below it a finished pane's status is
   lost for good in roughly 10 to 30 percent of trials: `pane_dead=1`,
   `pane_dead_status` empty, and it never arrives. An empty status is also
   how tmux encodes death by signal, so below 3.5 a clean exit and a kill are
   indistinguishable for that share of exits. Raspberry Pi OS bookworm ships
   3.3a, so the desk Pi is on the lossy side; Ubuntu 24.04 (3.4) is too.
3. **`pane_dead_signal` is decoration.** Absent before 3.5, a number on 3.5a,
   a name on 3.7. It is read into `PaneStatus.Signal` for logs and never
   decides anything.

The design consequence is the one load-bearing rule in the mapping:
**an empty status is UNKNOWN, never success.** `succeeded` requires a literal
`"0"`. On an old tmux the cost of that rule is one extra run of a finished job
under backoff (it then very probably reports its status); the cost of the
opposite rule would be a killed job parked as a satisfied replica forever.

Consequence 3 from the brief (`new-window -t <session>` fails with "index 1
in use" when a dead window is current) **does not reproduce on any of the six
versions with marvel's exact argument vector**, dead window current or not,
one dead window or four, per-window or global `remain-on-exit`. It reproduces
only with an explicit `-t session:index` target, which marvel never passes.
finding-036 found the same on 3.7c and suspected a patch-release change; it
was an unstated precondition in the 3.7b measurement. Retired.

Consequence 4 (the set-option race) holds everywhere and is why the default is
server-global: an instant exit under the global default reports
`dead=1 status=N` on every version. The interactive per-window `off` is
applied after `new-window` returns, so an interactive command that exits inside
that gap persists dead with its status and reads `crashed (exit N)` instead of
plain `crashed`; ReapDead reclaims it the same tick. Benign, and two tests
carry a one-second sleep to stay off it.

## SP2: the four consequences under the daemon

1. `HasPane` returning true for a dead pane: **corrected in the driver.**
   `HasPane` now means "exists and not dead"; `PaneStatus` is the read that
   separates the two. The pane-liveness question every caller asked was
   never "does tmux still hold a window".
2. Dead panes persisting: **reaped.** `ReapDead` kills the dead pane after
   reading its status, only when the `@marvel_pane` marker is present
   (finding-014's fence). `marvel reap` reports clean on the healthy fleet
   above, which is the property this change was most likely to break.
3. Spawn failure with a dead window current: **retired** (SP1 above).
4. The race: **closed by the global default** (SP1 above).

Not exercised: adoption of a pane that is already dead across a daemon
restart (the restart above adopted a live pane and rehydrated a completed
row from bolt, which is the common shape).

## Three premises corrected on the way

- `aae-orc-bxeh` and `api.OccupiesReplicaSlot`'s comment said nothing writes
  `SessionSucceeded`; the reap path now does, and the #244 guard
  `TestCountReplicaSlotsIsBehaviourIdenticalToday` is reframed as
  `...MatchesLivenessOutsideCompletion` (every pair other than headless +
  succeeded must still count the same under both predicates).
- The brief said consequence 3 takes down spawning for a whole session. It
  does not, on any version, with marvel's form.
- The direct launch path (`directCommand`, ad-hoc sessions and session-package
  tests) joins `Runtime.Args` as raw shell text while the adapter path
  (`buildCommand`) quotes each arg. `sh -c exit 3` on the direct path runs
  `exit` with `$0=3` and exits 0. Not changed here; the tests carry the
  quoting each path expects and say so.

## Left open, deliberately

- **Retry-on-failure for headless work.** A non-zero exit is reported as
  `crashed` and retried by the existing crash-loop path, because ADR-010 left
  the policy question open and `on-failure` is not wired on the reap path.
  The code is on `ExitStatus`; the state is not a distinct `failed` verdict.
- **Shift readiness for headless roles.** `allReady` gates on
  `SessionRunning`; a generation-2 job that completes before the ready check
  observes it running would stall the shift until `ShiftTimeout`. The run
  above passed because the check saw `running` at spawn. Completion should
  probably count as ready for a headless role; not changed here.
- **A `tmux -V` advisory.** The daemon does not yet warn when its tmux is
  below 3.5. The behaviour degrades to today's (one extra run) rather than
  to anything worse, so this is a log line, not a gate.
- **`MetricsAt` on crashed rows** still reads `0.0 / 0B` (finding-027 cell 3),
  unchanged.

## Reproduction

SP1 scripts (`measure.sh`, `c3.sh`, `instant.sh`, `late.sh`) are POSIX sh and
ran unchanged on macOS and in `docker run --rm <image> sh -c 'apt-get install
-y tmux; sh /sp1/<script>'`. SP2 is `oneshot.toml` (three roles as above)
against `marvel daemon --socket /tmp/<short>/d.sock --state-bolt ... --log-file
... --pidfile ...` with `MARVEL_TMUX_SOCKET` set; keep the socket path short
(finding-036's `sun_path` note). Tests:
`TestPaneStatusCarriesExitStatusForKeptWindow` (driver),
`TestReapDeadMapsExitStatus` and `TestReapDeadEmptyStatusIsNotCompletion`
(session), `TestHeadlessCompletionHoldsSlotAndIsNotRefilled` (team).
