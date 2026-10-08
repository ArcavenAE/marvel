# Fleet suspend and resume: freeze every seat, keep the daemon answering, resume on a human's word

Design for review. The build waits on the gate in section 1; no code lands until this doc is reviewed and the gate is met.

- Author: the arcaven architect seat.
- Idea: `_kos/ideas/fleet-pause-resume.md` (#677).
- Evidence: finding-marvel-1kg8 (`_kos/findings/finding-marvel-1kg8-fleet-pause-substrate.md`). Every MEASURED line below comes from it.
- Standalone fixes this design builds on: #715 (tmux exec timeout), #716 (adopt pass bounded and served), #717 (watch fetch off the key loop), and #719 (the builder PR for #715).
- Checked against marvel `origin/main` 9e47465.
- Rulings: the operator ruled the open items on 2026-10-08, relayed by director. Each is marked **(ruled)**. Everything else is a design default, open to review.

## 1. Why, and the gate

An operator stepping away from a fleet for an hour has two choices today, and both are bad. Leaving the seats running spends quota and lets them act unwatched. Stopping them loses their context, their in-flight turns and their bus presence. A suspend that freezes every seat in place and resumes it later keeps all of that.

The finding shows this works on Darwin for up to an hour across four harnesses. It also shows that today's daemon cannot answer anything while tmux is frozen, which is why the three standalone fixes come first.

**Gate (ruled, O1 option b):** design now, build after the following are measured:
- the S2 log on disk with its stop order: done (finding section 2);
- F3 at 60 minutes: done;
- S9, a frozen seat with MCP: done for a claude seat with the director shim;
- Linux stop state under tmux: **open**. It waits on a Linux machine the operator will provide.

## 2. Mechanism (settled by measurement)

- **Stop order.**
  - Stop the tmux server first, then every recorded pid (MEASURED: the reverse order is undone within 50 ms).
  - Verify `T` on every pid, twice. A pid that exited during the stop is not a failure.
  - Any pid not in `T` means the suspend failed, and it rolls back: `SIGCONT` everything already stopped, server last, then clear the record.
- **Pid collection.**
  - Collect while tmux still answers: the server pid, each pane pid, and each tree walked with `ps -o pid,pgid,ppid,stat,lstart` until two snapshots match.
  - Exclude the daemon, its ancestors, the invoking CLI and the managed bus. MEASURED in S9: the broker stays unfrozen, and a resumed seat's shim reconnects on its own.
  - Write the pid list, with start times, to bolt before the first signal.
  - After the server stops, walk again. Write any new pid to bolt, then signal it.
- **Resume.**
  - `SIGCONT` every recorded pid, and the server. MEASURED: the order is not load-bearing as long as every recorded pid gets its own `SIGCONT`. The design sends seats first and the server last, matching the measured runs.
  - Skip a pid whose start time changed; that pid number now belongs to a different process.
  - Verify that no pid reads `T`.
- **Not used.**
  - Mach `task_suspend`: MEASURED not viable unattended on Darwin with developer mode off.
  - cgroup freeze: phase 2 at most, as a Linux backstop behind the same verbs, and only after a Linux run.

## 3. State and the daemon while suspended

- **The record.**
  - A bolt record with three states, `suspending`, `suspended` and `resuming`. It holds the actor, source, time and pids, and it is written before the first signal.
  - A JSON mirror beside the socket, so `status` works with the daemon down.
- **While suspended.**
  - The reconcile tick returns before `reapDeadLocked`.
  - Watchdog, capture, inject, the pane menu, the limit action and repaint each return `cluster suspended; run marvel resume`.
  - A typed sentinel in `Driver.cmd` is the backstop for every driver exec. It joins #715's error family. The suspend and resume procedure itself bypasses it.
  - The sentinel never feeds `RoleHealth` or `FailureCount`.
- **At daemon start.** Read the record before adopt:
  - `suspended`: skip adopt, `RefreshLiveness` and posture, and serve. MEASURED: an adopt under a frozen tmux blocks the whole start (#716).
  - The recorded server pid and start time are dead: treat it as a reboot. Clear the record with an event, then adopt.
  - `suspending`, meaning a crash mid-suspend: roll back by `SIGCONT` to everything recorded, clear the record, and emit `cluster.suspend-interrupted`.
- **Shutdown.** MEASURED: a daemon sent `SIGTERM` under a frozen tmux waits for tmux. With the record set, shutdown makes no tmux call. The seats stay frozen, the record stays, and the next daemon serves.
- **Timers at resume.**
  - Shift every last-seen stamp and absolute deadline by the pause: heartbeat, `ContextAt`, activity rings, `BackoffUntil` and `HealthySince`. Do it in one transaction, or idempotently with the record cleared last.
  - Age checks of the form `now.Sub(CreatedAt)` subtract a persisted per-session `PausedTotal`. `CreatedAt` itself is never rewritten.
  - The schedule clock skips firings that fell inside the pause.
- **Refused while a record exists:** `--reclaim` and `reap --confirm`. There is no override in phase 1 **(ruled, O5 option a)**; resume first.

## 4. Operator surface

- **Verbs:** `marvel suspend`, `marvel resume` and `marvel status`.
  - `suspend` on a TTY asks `[y/N]`. Without a TTY it refuses unless `--yes` is given.
  - `resume` on the CLI has no dialog.
- **Status.**
  - It is its own daemon method. It reads an `atomic.Pointer`, following the precedent `sshServer` at `internal/daemon/daemon.go:136` and modelled on `handleDaemonStatus` (`internal/daemon/status.go:103-118`). It never takes the controller lock and never calls tmux.
  - With the daemon down, it reads the mirror and says so.
  - It reports the state, since, elapsed, by and source, and the seat counts. Per-seat state is labelled as recorded at stop time. Seats that failed to resume are listed. It also prints the manual recipe (section 6).
  - It says that bus presence lapses during a suspend. MEASURED in S9: `AGENT_STATE` is empty within its 90 s TTL, and the same key is back within 5 s of resume. A presence reader would otherwise report every seat as gone.
  - `--verify` re-reads the pids with `ps`.
  - Exit codes: 0 running, 3 suspended or partial, 1 error.
- **Watch keys (ruled, O2 option a):** in `get sessions -w`, `S` suspends and `U` resumes. Both are capitals and both are unbound today; lowercase `s` is the state sort.
- **Resume confirm in the view (ruled, option a):** `U` asks a one-line confirm, default no. A view may be open on a remote cluster nobody has inspected, and a resume wakes every seat at once.
- **Paste and repeat guard.** This is a design default; the numbers are the operator's to change.
  - When the dialog opens, drop every byte for 300 ms.
  - After that, `y` or `Y` confirms only if no other byte arrived in the 20 ms before it. Any other byte cancels.
  - Drain the key channel afterwards.
  - One review seat preferred 500 ms and called both numbers guesses.
- **Indicator.** Every surface shows the same record:
  - a header field in `get sessions`;
  - an inverse, full-width bar in watch, never colour alone;
  - a `# suspended` line for non-TTY output;
  - a block in `describe daemon`.
- **The view under a slow daemon.** This is #717: fetch off the key loop with its own deadline, show a "daemon not answering, data as of" banner, and keep `q`, Ctrl-C and `U` live. A remote `mrvl://` connection cannot take a deadline (`daemon.go:3055-3056`), so the deadline lives in the view.

## 5. Events and audit

- **Kinds:**
  - `cluster.suspended`, `cluster.resumed`, `cluster.suspend-interrupted` and `session.resume-failed`;
  - `cluster.suspend-long` for the warning in section 6.

  There is no `*-requested` pair. Do not reuse `seat.resume-proposed`, which belongs to usage limits (`internal/events/events.go:74`).
- **Fields:**
  - the source: `cli`, `watch-key` or `mrvl`;
  - for `mrvl`, the key fingerprint and remote address (`sshserver.go:127-128`);
  - the seats requested and the seats confirmed.
- **Durable audit:** a bounded, append-only bolt history bucket, after `schedule_status` (`DefaultScheduleHistoryMax = 50`, `internal/api/schedule_status.go:255-258`). The default is 50 entries, and the operator may change it. The event ring and `logbuf` are memory only.

## 6. Safety

- **No auto-resume, ever.** Nothing resumes on a clock or on silence (SOUL section 8).
- **Long-pause warning (ruled, O3 option c):**
  - a cluster config key, `suspend_warn_after`, default `1h`;
  - past it, the indicator says so, and `cluster.suspend-long` repeats once per interval.

  It is a warning only, never an action.
- **Remote authority (ruled, O4 option b):**
  - a new scope word, `suspend`, covers `suspend` and `resume` over `mrvl://` in phase 1. `admin` includes it.
  - A key with no `marvel-scope` option is admin today (`internal/daemon/scope.go:9-19`), so the new word lets an operator issue a key that can suspend and resume and do nothing else.
  - This is the dissent's side of the review panel's 4-1 vote, carried as ruled.
- **Recovery.**
  - A fresh daemon resumes from the record.
  - A `kill -CONT` recipe is printed at suspend and written to the mirror: the server, then each pid with its start time and pgid.
  - Only the operator runs the recipe, by hand. Never a seat, never a remote key.
- **A seat that fails after resume** is marked with a status field visible in `get sessions`, and `session.resume-failed` is emitted. It is never restarted by this path.
- **After resume, no nudge (ruled, O6 option a).** A silent seat is listed by `status` and `get sessions`; the operator acts. There is no resume stagger in phase 1.

## 7. Phase 2 and later (recorded, not designed)

- **Team or seat scope.** The tmux server is shared, so a seat cannot be frozen through a server stop.
  - MEASURED (arm 2): tmux resumes only its direct child, so a harness launched under a wrapper can be stopped alone with tmux running.
  - A per-seat suspend therefore needs a wrapper launch in front of the harness. Today the harness is the pane process.
- A `--for` marker that flags an overdue pause and never resumes. Its wording is the operator's.
- A cgroup backstop on Linux, after a Linux run. If Linux and Darwin diverge, accepting that is the operator's call.
- Freezing the managed bus is not in phase 1. It would need its own S9-style measurement.

## 8. Candidate requirements (provisional)

Each is tagged with its source class: MEASURED (finding-marvel-1kg8), RULED (operator, 2026-10-08) or DESIGN (this doc, open to review).

- **SR-A (MEASURED):** a suspend stops the tmux server before any seat pid, and counts a pid as frozen only after reading `T` twice.
- **SR-B (MEASURED):** `status` answers while suspended, without the controller lock and without calling tmux.
- **SR-C (RULED):** nothing resumes a suspended cluster except an explicit `resume`. A long pause warns at `suspend_warn_after` and never acts.
- **SR-D (RULED):** over `mrvl://`, suspend and resume need the `suspend` scope or `admin`.
- **SR-E (DESIGN):** a daemon that starts with a `suspended` record serves without adopting and makes no tmux call until resume.
- **SR-F (DESIGN):** at resume, every age and deadline that a pause would otherwise trip is shifted by the paused duration. `CreatedAt` is never rewritten.

## 9. Open before the build

- The Linux stop state under tmux (the gate item still open).
- Mid-turn survival for claude and crush is unmeasured. Neither had a turn in flight at the moment of freeze.
- MCP survival for codex, opencode and crush shims is unmeasured. S9 covered one claude seat.
