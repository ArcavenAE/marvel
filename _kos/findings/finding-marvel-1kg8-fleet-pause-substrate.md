# finding-marvel-1kg8: a frozen fleet comes back, but only if tmux is frozen first, and marvel cannot answer while it is

Date: 2026-10-08
Status: frontier
Brief: `_kos/probes/brief-fleet-pause-signals.md`
Idea: `_kos/ideas/fleet-pause-resume.md`

## Why this matters

The operator asked whether marvel could suspend every running harness and resume it later, without losing in-flight work or connections. This finding gives the substrate answer.

- Yes, for up to an hour, on Darwin, for the four harnesses tested.
- The stop has to reach the tmux server before it reaches the seats.
- While tmux is frozen, today's daemon cannot answer any call, even a status call, or start, or shut down.

The last point is a set of standalone defects (#715, #716, #717), whatever is decided about the feature.

Tags:
- **MEASURED:** run on an isolated scratch daemon, with its own HOME, socket, state file and tmux server; never the live fleet.
- **CODE:** read at marvel `9e47465`.
- **INFERRED:** reasoning, not observed.

Rig:
- Darwin, tmux 3.7b, marvel `9e47465` from Homebrew.
- Harnesses: claude 2.1.292 and codex-cli 0.160.1 on their cloud models; opencode 1.18.15 and crush v0.88.1 on a local qwen3:0.6b.
- Two seats per harness: idle, and mid-turn (streaming a count of 300 lines).

## 1. tmux undoes a seat stop unless the server is stopped first

- **MEASURED:** a `SIGSTOP` to a pane's own process is undone by the tmux server within a second. That process is the harness itself in a marvel seat, so a per-seat stop does not hold. `SIGTSTP`, `SIGTTIN` and `SIGTTOU` did not stop a pane process either.
- **MEASURED:** stopping the tmux server first, then every seat pid, holds. A second scratch probe found the reverse order (trees first, then the server) undone within 50 ms (Darwin only).
- **MEASURED (arm 2):** a process one level below the pane process (a worker under a wrapper `sh`, same process group) stays stopped with tmux running:
  - `T` at +1, +5, +10 and +20 s, and after a `resize-pane` and a `capture-pane`;
  - control: the wrapper itself, stopped alone, was resumed by tmux within 1 s.

  So tmux resumes only its direct child. **INFERRED:** a per-seat suspend without freezing the server is possible only for a harness launched under a wrapper. That is phase 2 at most.
- **MEASURED:** the 2026-10-07 runs were void. `kill` returned 0 and the seat never stopped, because tmux resumed it. Every later run read `ps -o stat` and required `T` twice before counting a seat as frozen.

## 2. What survives a freeze

Sets F1 (1 min), F2 (10 min) and F3 (60 min). Each froze the scratch tmux server, then every seat pid, all read `T` twice. Resume sent `SIGCONT` to every recorded pid.

- **MEASURED:** 8 of 8 seats resumed in every set, and every seat answered a fresh prompt afterwards.
  - In F3, opencode on the local model answered after the 120 s window; it was slow, not dead.
- **MEASURED, mid-turn:**
  - codex and opencode finished their interrupted counts after resume in all three sets (F3: codex from 3165 to 3300).
  - claude had finished before the freeze took hold, and crush printed no count, so mid-turn survival is shown for 2 of 4 harnesses.
- **MEASURED, per pid (F3):**
  - before `SIGCONT`, 10 of the 11 processes in the trees read `T`: the server, the harnesses, and a `caffeinate` child in one seat's process group. The 11th was a non-seat shell.
  - at +2 s and +30 s after resume, none read `T`.
  - no `setsid` child was present in these trees; the second probe's arm C covers that case (it stays stopped).
- **MEASURED (S9, bus and MCP):**
  - Setup: a claude seat with the director shim against a scratch NATS broker on a random port. The broker was not frozen. Freeze 10 min.
  - Presence in `AGENT_STATE` (90 s TTL) was empty at +120 s frozen. The same presence key, with the same instance id, was back within 5 s of resume.
  - The first `send_message` and `wait_for_message` after resume both worked; the inbox stream went from 1 to 2.
  - **INFERRED:** a presence reader sees a suspended seat as gone, so a suspend surface must say so.
  - codex, opencode and crush shims are untested.

## 3. What marvel does while tmux is frozen

- **MEASURED:** `marvel get sessions` did not return within a 20 s bound at any point during F1, F2 and F3.
  - **CODE:** `Driver.cmd` builds every tmux exec with no deadline (`internal/tmux/driver.go:164-176`), and the reconcile pass holds the controller lock across the reap (`internal/team/controller.go:494-497`). Filed as #715.
- **MEASURED:** a fresh daemon started under a frozen tmux never answered.
  - `describe daemon` timed out on 6 of 6 tries over 130 s. The socket file existed, so connections queued.
  - The daemon logged nothing until tmux resumed. One second later it logged 8 `AdoptOrLeave ... adopted pane` lines, then `listening`.
  - **CODE:** listen at `daemon.go:527`, adopt at `:565-569`, accept at `:652-656`. Filed as #716, with this measurement as a comment.
  - Positive control: the same start with tmux running answered at once, with no tmux connect errors.
  - One run was void and discarded: the daemon had been started without `MARVEL_TMUX_SOCKET`, so it ran on another tmux server and answered in 96 ms.
- **MEASURED:** a daemon sent `SIGTERM` under a frozen tmux did not exit within 30 s. It exited about 1 s after resume (`detached, 8 session(s) left running`). Shutdown waits on tmux too.
- **MEASURED:** no session was restarted, killed or shifted by any freeze. The only event in the window was a pre-existing `heartbeat.refused` for one codex seat, which also occurs with no freeze.
- **CODE:** the watch view fetches inside its key loop (`cmd/marvel/main.go:2911`, inside the select at `:2986`), so a blocked fetch makes `q` and Ctrl-C dead. Filed as #717.

## 4. Other freeze mechanisms

- **MEASURED (Darwin, `task_suspend`):** Gatekeeper is on, SIP is on, and developer mode is off.
  - A helper with no entitlement got `task_for_pid` = 5 against every target: Apple `/bin/sleep`, Homebrew `gsleep` (ad-hoc signed) and an unsigned counter.
  - With `com.apple.security.cs.debugger`, it got 5 at once against the Apple platform binary, and blocked (bounded at 10 s) against non-platform targets.
  - **INFERRED:** this is the task-port authorization, not Gatekeeper, and it cannot be answered unattended with developer mode off. Seat trees are non-platform. Untested: a Developer ID helper, developer mode on, and root.
- **Not measured:** Linux stop state under tmux, and a cgroup freeze. They wait on a Linux machine.

## 5. What follows

- The three defects in section 3 are filed flat as #715, #716 (blocked by #715) and #717, each its own PR.
- The suspend design is `docs/design/fleet-suspend-resume.md`. Its build waits on the Linux stop-state measurement. Every other gate item named for it (the S2 log, F3 at 60 min, and S9) is measured here.
