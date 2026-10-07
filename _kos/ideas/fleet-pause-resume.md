# A pause button: suspend every running harness, then resume it

**Status: idea. Pre-hypothesis. Nothing here is designed or measured.**

Captured: 2026-10-07
Source: operator, relayed by director.

## The operator's words

> "kos idea marvel what if we had a "pause" button, some way to suspend all
> the running harnesses (does such a mechamism exist, like ctrl-z/SIGTSTP,
> would that work?) could we pause/resume all the agents? would they resume?
> what happens to disconnected sessions when we resume?"

## Where this lives, and why

Filed in marvel's graph by the subject test. marvel is the process that
holds every seat's pane, process tree and health record, so a fleet-wide
pause is a marvel verb and a marvel state. The harnesses and director are
the objects it would act on.

## What exists today (checked against `5a972a4`)

- Nothing pauses a running seat. No code under `internal/` or `cmd/`
  sends or handles `SIGSTOP`, `SIGTSTP` or `SIGCONT`.
- The start-line hold (convergence posture `hold`, which `marvel converge`
  releases) only withholds cold spawns. Running seats keep running.
- A role's `schedule.suspend` (`internal/api/schedule.go`) only stops
  scheduled firings.
- crush has no marvel adapter, so it would run under the generic fallback.

## Starting notes (director's judgement; unmeasured)

Everything in this section is reasoning, not measurement.

**Mechanisms.**
- `SIGTSTP` is what Ctrl-Z sends. A TUI can catch or ignore it, so it may
  not stop anything.
- `SIGSTOP` cannot be caught. It freezes the process, and `SIGCONT`
  resumes it.
- Either signal has to reach each seat's whole process tree: the harness,
  its tool subprocesses, and any helper daemon (codex's app-server). The
  pane's top pid is not enough.

**Measured since (2026-10-07): tmux undoes a stop of its pane's process.**
When a pane's own process stops for any signal but `SIGTTIN` or `SIGTTOU`,
the tmux server sends `SIGCONT` to the pane's process group (`killpg`). The
reviewer of this idea read that in tmux `server.c:511-528`, identical in 3.7b
and 3.7c, and measured it on 3.7c. I measured the same on 3.7b, on scratch
servers only:

| sent | state after |
|---|---|
| `SIGSTOP` to a process the pane process spawned (not tmux's child) | stopped (`T`) |
| `SIGSTOP` to the pane process | running again within 2 s |
| `SIGSTOP` or `SIGTSTP` to the pane's whole process group | running again within 2 s (reviewer, 3.7c) |
| `SIGSTOP` to the pane process with the tmux server itself stopped | stopped until the server resumes |

A marvel harness is its pane's process, so a raw `SIGSTOP` cannot hold a
seat. A tool subprocess outside the pane's process group does stay stopped,
so a tree can end up half paused. This retires the judgement above that
`SIGSTOP` "freezes the process"; it does, until tmux resumes it.

**What resume might break.**
- A model request in flight during the stop breaks, and the harness has to
  retry it.
- The director MCP connection, the bus and other sockets drop and have to
  reconnect.
- Short-lived credentials may expire during a long pause.
- Prompt caches go cold, so the first turn after a resume costs more.
- marvel's own watchdog, heartbeat health and shift triggers would read a
  paused seat as stalled or dead. So a pause has to be a marvel state the
  controller knows about, not a raw signal sent past it.

**"Disconnected sessions."** This is an open question for each harness.
- Claude Code keeps the conversation client-side, so a retried request
  may simply carry on.
- codex, where a turn chains to a stored previous response, may not.
- opencode and crush are unknown.

## Questions this raises

- Does a stopped harness resume at all, and does it finish a turn that
  was in flight?
- How long can a pause last before something does not come back
  (sockets, credentials, a server-side session)?
- What does marvel show for a paused seat, and which of its timers stop
  counting while it is paused?
- Is "pause" one verb for the fleet, a team, or a single seat, or all three?
- Does a resume need a nudge to start the next turn (compare #391, the
  missing first prompt)?

## A cheap probe, if this crystallizes

Run one scratch seat per harness (claude, codex, opencode, crush) on an
isolated daemon: every `MARVEL_*` unset, its own HOME, socket and tmux
server. `SIGSTOP` each seat's whole process tree for 1, 10 and 60 minutes,
once mid-turn and once idle. Because tmux resumes a stopped pane process
(above), the stop has to be confirmed, not assumed: record every process in
each seat's tree with `ps -o pid,pgid,stat`, right after the stop and at each
interval, before any `SIGCONT`. A run where the harness process is not `T`
throughout is void, not a clean pause, and the process groups show a tree
that is half paused. Then `SIGCONT` and record:

- whether the harness resumes;
- whether the in-flight turn completes, retries or errors;
- whether each socket reconnects;
- what marvel's health, heartbeat and watchdog reported during the pause.

Record each harness's version.

## Not this

- Not the start-line hold, which leaves running seats alone.
- Not `marvel stop`, which detaches the daemon and leaves every agent
  running.
- Not a kill and respawn, which loses each seat's context.
