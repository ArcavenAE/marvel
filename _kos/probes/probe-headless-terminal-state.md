# Probe brief: what a headless role's terminal state means, and whether tmux can carry it

**Status:** OPEN (brief only; not started). Scheduled as bd `aae-orc-vsju`,
which blocks `aae-orc-bxeh`.
**Question:** `question-substrate` (the supervision half of the tmux boundary),
bears on `question-runtime-adapter-framework`
**Probe medium:** measurement (tmux on macOS and Linux) then code (the daemon)
**Timebox:** 1 session
**Ticket it serves:** `aae-orc-bxeh`. Companion finding: `finding-015`.
**Prior work it extends:** finding-015 (the defect), finding-014 (the pane
ownership fence this route depends on), finding-004 and
`decision-brief-kxce-substrate` (the shim ruling this probe must not preempt).

## Why this probe exists

`aae-orc-bxeh` names three decisions and says to settle them before building.
Its first one assumes the exit status marvel needs is not available on the
tmux path, and that getting it "may argue for the shim". Measurement on
2026-08-07/08 (kinu, macOS 26, tmux 3.7b) shows tmux carries the status
natively, which moves the hard part of the ticket from plumbing to semantics.

The probe exists so that the semantics are ruled on evidence rather than on
the first plausible mapping, and so the plumbing is verified on the platform
floor before any of it lands. Writing `succeeded` naively inverts the defect
into an infinite respawn loop, which is the specific failure this brief is
meant to stop.

## What is already measured, so the probe does not redo it

Raw tmux, private server socket, tmux 3.7b on macOS. With
`remain-on-exit on`, a pane persists after its command exits and carries the
status:

| case | `pane_dead` | `pane_dead_status` | `pane_dead_signal` |
|---|---|---|---|
| `exit 0` | 1 | `0` | |
| `exit 3` | 1 | `3` | |
| SIGKILL | 1 | (empty) | `kill` |
| `sh -c 'exit 7' > fifo < /dev/null` | 1 | `7` | |

The last row is the launch form marvel actually uses
(`redirectStdout` / `redirectStdin`, `internal/runtime/adapter.go:205-220`).
It is a redirection rather than a pipeline, so the shell execs the harness and
the reported status is the harness's own.

Four consequences of turning `remain-on-exit` on, all measured:

1. `Driver.HasPane` (`internal/tmux/driver.go:289`) returns true for a dead
   pane, so `ReapDead` would stop firing entirely. Liveness has to become
   `pane_dead`, not existence.
2. Dead panes persist until killed, so the reap path has to kill the pane
   after recording status. The `@marvel_pane` fence from finding-014 already
   makes that safe.
3. `tmux new-window -t <session>` fails with `create window failed: index 1 in
   use` when a dead window is current. `NewPane` (`driver.go:246`) uses exactly
   that form. `-t '<session>:'` and `-a -t <session>` both succeed. The failure
   takes down spawning for the whole tmux session, not just the dead role.
4. `NewPane` sets `remain-on-exit` at line 271, after `new-window` returns, so
   a harness that dies immediately still vanishes before the option lands.
   Setting it as the server-global window default at session creation closes
   the race (verified: an instant-exit window under the global default reports
   `dead=1 status=5`). Marvel runs its own tmux server, so a global default
   touches nothing of the operator's.

Two premises from the ticket re-verified in source: `SessionSucceeded`
(`internal/api/types.go:16`) is written nowhere in production code, and
`RestartOnFailure` is reachable (`internal/team/controller.go:668`) but
indistinguishable from `always` for the same reason. One fix revives both
contracts.

## What is not established

- Any of the above on Linux, or the tmux version floor. `pane_dead_signal` is
  newer than `pane_dead_status`, marvel documents no minimum tmux version, and
  B13 says platform-specific behavior needs platform-specific testing.
- Any of it under the daemon rather than under raw tmux commands. Adoption,
  `UnrecordedTmuxState`, `PanePID`, and procstat all meet dead panes for the
  first time under this change.
- What a completed one-shot means to the reconciler. `succeeded` is excluded
  from `CountsAsAlive` (`types.go:42`) and the default policy is `always`
  (`internal/api/manifest.go:544`), so a naive mapping respawns a finished job
  forever.
- Whether a one-shot is a state on Session at all, or wants its own resource
  kind. `Schedule` (CronJob) is model-only per CLAUDE.md, and k8s answered the
  same question by splitting Job from Pod rather than adding a Pod state.

## Hypothesis

A headless role's terminal state is derivable from the tmux dead-pane status
with no shim, and the correct state machine is exit-status to
`succeeded`/`failed` with `on-failure` as the default for `mode = "headless"`
roles, requiring no new resource kind.

Two ways it can be wrong, both worth finding out cheaply: tmux's dead-pane
status is unavailable or unreliable at the Linux version floor, or one-shot
lifecycle needs a resource kind and the state-plus-policy pair is a shortcut
that manifests will later have to be migrated off.

## Sub-probes and success signals

### SP1. Linux and the version floor

Re-run the four measured cases on Linux (aarch64 per B13's prior instance) and
on the oldest tmux marvel intends to support. Establish which of
`pane_dead`, `pane_dead_status`, `pane_dead_signal` exist at that floor and
what an absent format expands to.

**Success signal:** a stated minimum tmux version with the behavior table
filled per platform, or a measured reason the route does not survive Linux.
Either outcome is a result. A missing `pane_dead_signal` is survivable
(an empty status already implies death by signal); a missing
`pane_dead_status` is not.

### SP2. The four consequences under the daemon

Exercise the change on a live daemon rather than raw tmux: a headless role
that exits cleanly, one that exits non-zero, one killed by signal, one
adopted across a daemon restart while dead, and `marvel reap` / `--reclaim`
with dead panes present.

**Success signal:** each of the four consequences either holds as measured or
is corrected with the daemon-level detail that changes it. `reap` must still
report clean on a healthy fleet, which is the property finding-014 restored
and which this change is most likely to break.

### SP3. What a finished job means to the reconciler

With exit status in hand, decide the mapping and the replica semantics. At
minimum: does `succeeded` suppress a replacement on its own, or does the
policy carry it; should `mode = "headless"` default to `on-failure` rather
than `always`; what does a role with `replicas = 3` mean when the work is a
one-shot; and does a succeeded session stay in the store, and for how long.

**Success signal:** a written mapping from (mode, exit status, restart policy)
to session state and reconciler action, with the respawn-forever case shown
to be unreachable under it.

### SP4. State plus policy, or a resource kind

Weigh SP3's mapping against giving one-shot work its own kind. The cheap shape
is hard to walk back once manifests depend on it, so the alternative gets
written down before the cheap one ships, not after.

**Success signal:** a decision brief with a recommendation and its reasoning,
carried to the operator for ratification per ADR-007. Not a self-ratified
choice.

### SP5. The unexplained non-respawn

finding-015 records that three reaped roles stayed `crashed` for roughly four
minutes with no replacement, and correctly marks it unestablished. Under the
current code that is anomalous: the default policy is `always`, the first
backoff is 30s, and `noteReapedCrash` (`controller.go:283`) should have
replaced them.

**Success signal:** either a reproduction that explains it, or a measured
statement that it belongs to `aae-orc-4bz2` (reconcile-on-start destructive,
backoff suppresses recovery) and not here. Cheapest sub-probe; do it first,
because if the reap path is not reaching the reconciler at all then SP3's
mapping is being designed against a path that does not run.

## Excluded scope

- **Implementing the fix.** The code lands under `aae-orc-bxeh`. This probe
  produces the measurement and the ruling it asked for.
- **The shim.** `decision-brief-kxce-substrate` adopted the shim as direction,
  phased in when a concrete need pulls it, and listed the pulls: race-free
  inject, per-child metrics without pane-pid guessing, and one path carrying
  structured plus rendered output. Exit status is not on that list and the
  pre-probe measurement suggests it does not join it. If SP1 or SP2 changes
  that, say so explicitly rather than quietly widening the pull-list.
- **Schedule / CronJob design.** SP4 rules on whether a kind is wanted, not on
  what it would look like.
- **Interactive roles.** Their pane vanishing still means what it means today.

## What would change the read

A Linux floor without `pane_dead_status`, which would put exit status back on
the shim and make this a kxce pull after all. Or an operator ruling in SP4
that one-shot work gets its own kind, which would make the state mapping a
migration step rather than the destination.

## Harvest

Finding in `_kos/findings/`, numbered next in sequence. Nodes touched:
`question-substrate` (the supervision boundary gains a measured instance),
and whichever of `elem-k8s-resource-model` or the session-state prose SP4
lands on. If SP4 recommends a resource kind, that is an ADR, not a node edit.
