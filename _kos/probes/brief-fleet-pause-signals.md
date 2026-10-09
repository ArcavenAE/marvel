# Probe brief: does a frozen harness come back, and what does marvel see meanwhile?

**Status:** COMPLETE 2026-10-08 for Darwin, except the Linux arms, which wait on a Linux machine. The void 2026-10-07 sets (S1, S2, T) are superseded by F1, F2 and F3 and the arms below. Finding: `_kos/findings/finding-marvel-1kg8-fleet-pause-substrate.md`.
**Question:** `question-fleet-pause-resume` (frontier node, harvest 2026-10-09),
from the pause idea, `_kos/ideas/fleet-pause-resume.md`. Carried forward there:
the unattended pid_suspend re-run (finding-marvel-1eds section 5) and the Linux arms.
**Medium:** live harness sessions on an isolated scratch marvel daemon, built at
`5a972a4`. claude 2.1.292 and codex-cli 0.160.1 call their cloud models; opencode
1.18.15 and crush v0.88.1 call a local ollama model (qwen3:0.6b).
**Commissioned by:** the operator ("yes run the probe"), relayed by director.

## Why this probe exists

The idea asks whether marvel could pause every running harness and resume it,
and what happens to in-flight work and connections. Every answer in the idea
file is reasoning. This probe measures it.

## Isolation, checked before the first signal

- A scratch daemon with its own HOME, socket and tmux server, under per-run
  unique paths. Every `MARVEL_*` variable is unset in the shell that starts it.
- Positive control before any signal: the scratch socket answers `get teams`
  and lists only the probe team; the live daemon's session and pane counts are
  recorded before the probe and checked after every phase.
- Every pid that gets a signal must descend from the scratch tmux server's pid.
  The check runs before each signal, and a pid outside that tree aborts the run.
- The eight probe seats run with no MCP servers (`--strict-mcp-config` for
  claude, and a scratch codex home holding only the auth link and a minimal
  config), so nothing reaches the live bus. The bus and MCP arm (S9) adds one
  claude seat whose director shim talks to a scratch NATS broker on a random
  loopback port, provisioned for this run alone.

## Runs

Two seats per harness (idle, mid-turn), so eight seats, frozen together for
each duration. The original plan (S1, S2, S3 and T, each signalling only the
seat trees) was void, see Run log; the sets that ran are:

| set | freeze | then |
|---|---|---|
| F1 | stop the scratch tmux server, then every seat pid, for 1 minute | `SIGCONT` every pid, observe 3 minutes |
| F2 | the same, for 10 minutes | the same |
| F3 | the same, for 60 minutes, with `ps -o pid,pgid,stat` per pid before and after resume | the same |
| start | freeze, kill the daemon, start a fresh one, poll `describe daemon` | resume |
| S9 | one claude seat with the director shim on a scratch broker, 10 minutes | one MCP send and receive |
| arm 1 | Mach `task_suspend` on throwaway processes, signed and unsigned targets | |
| arm 2 | stop a child of the pane process, with tmux running; control: the pane process | |

- **Mid-turn** means a short prompt that streams a long answer (count from 1
  to 300), frozen a few seconds into the stream.
- **Idle** means the prompt is empty and the last turn is done.
- After each resume, every seat gets one more short prompt, to show whether
  the session and its credentials still work.

## What is recorded per seat and run

- Did it resume?
- Did the in-flight answer complete, retry, fail with an error, or hang?
- Did the next prompt work, as a check on credentials and session survival?
- What did marvel report while the seat was frozen: `get sessions` health and
  the `(stalled)` advisory, events, and whether any restart, kill or shift
  fired?
- The process tree that was signalled, and whether anything in it was missed.

## Run log

- **2026-10-07, S1, S2 and T (sent with `kill`): void.** The signals never
  landed. From the seat running the probe, `kill` to a pid outside its own
  process tree returned 0 and changed nothing: the claude seat stayed in
  state `S`, its CPU time kept rising, and its statusline heartbeat kept
  arriving every 15 s. Positive control: a `sleep` that the probing seat
  spawned itself went to state `T`, and a `sleep` held by the scratch tmux
  server did not. The first guess (a harness sandbox dropping signals) was
  wrong; the operator's own shell failed the same way.
- **Cause, measured 2026-10-07 on tmux 3.7b:** the tmux server resumes its
  pane's own process when that process stops. A grandchild under a scratch
  pane stopped (`TN+`); the pane's own process read `SNs+` right after
  SIGSTOP; with the scratch tmux server itself stopped, the same SIGSTOP held
  (`TNs+`), and it was undone as soon as the server resumed. A marvel
  harness is the pane process, so a raw SIGSTOP cannot hold a seat.
  SIGTTIN, SIGTTOU and SIGTSTP did not stop a pane process either. The checks
  below were silently void too, because the script checked ancestry and
  `kill`'s exit code but never the stopped state. Re-runs must read `ps -o
  stat` and see `T` before counting a seat as frozen.
- **Still valid:** the Ctrl-Z key sent through tmux (`send-keys C-z`).
  claude printed "Claude Code has been suspended" and kept running, codex
  showed no effect, and opencode and crush blanked their UI and waited
  until `SIGCONT`.

- **2026-10-08, F1, F2, F3, start, S9, arms 1 and 2:** results in the finding.
  One start run was void (the daemon ran without `MARVEL_TMUX_SOCKET`, so on
  another tmux server) and is discarded; the reported run has a positive
  control.

## Not in scope

- Linux stop state and cgroup freeze (wait on a Linux machine).
- A pause verb in marvel. The idea has crystallized; its design is
  `docs/design/fleet-suspend-resume.md`.
