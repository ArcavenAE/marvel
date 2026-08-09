# finding-019: one loss event was charged once per replica, and marvel cannot see that it was external

Date: 2026-08-09
Observed on: `internal/team` and `internal/session` at `0886801`, reproduced
in the package test rig (per-package tmux server, simulator-grade `sleep`
runtimes, no model tokens)
Bears on: `question-daemon-isolation-boundary` (the residual it names),
`question-healthchecks`
Work item: `aae-orc-4bz2`

## What was seen

A three-replica role whose whole tmux session is destroyed out of band, the
shape a foreign daemon's reclaim produced on 2026-08-06, is charged three
crashes in the tick that notices:

```
reap: session repro/squad-worker-g1-0 crashed (role repro/squad/worker restart #1, next backoff=59.9s)
reap: session repro/squad-worker-g1-1 crashed (role repro/squad/worker restart #2, next backoff=2m0s)
reap: session repro/squad-worker-g1-2 crashed (role repro/squad/worker restart #3, next backoff=4m0s)
role health: restarts=3 backoff_until=... (in 3m59.9s)
```

`RoleHealth` is keyed by `workspace/team/role`, so a per-replica charge
scales one event by the replica count. Two consequences follow, and the
second is the one the ticket calls suppressed recovery.

1. **The backoff exponent tracks replica count, not crash history.** One
   event moved the window from 30s to 4m.
2. **The restart budget is spent in a single event.** With
   `max_restarts = 3`, the same three-replica role has nothing left; the
   next loss saturates, `noteCrashAndBackoff` freezes `BackoffUntil` at the
   year-9999 sentinel, and the only documented recovery is deleting the
   team and re-applying, which destroys whatever else it still runs.

All three sessions also reported `state=crashed` alongside
`health=healthy`. `ReapDead` recorded the state transition and left the
health reading taken while the pane was alive.

## Why the obvious fix is not available

The ticket asks what distinguishes an external kill from an application
crash at the point `ReapDead` runs. Nothing marvel can see does.

Both present identically: a pane ID tmux no longer lists. The tempting
discriminator, "the whole tmux session is gone, so something external took
it", does not survive a check. A single-pane session collapses the server
when its process exits:

```
$ tmux -L repro-3dc644b1 new-session -d -s marvel-solo 'sleep 300'
$ tmux -L repro-3dc644b1 list-sessions
marvel-solo: 1 windows (created Sun Aug  9 00:55:12 2026)
$ tmux -L repro-3dc644b1 kill-pane -t %0
$ tmux -L repro-3dc644b1 list-sessions
no server running on /private/tmp/tmux-501/repro-3dc644b1
```

A harness that finishes and exits leaves the same evidence a reclaim
leaves. Any classifier built on that signal would have to choose which way
to be wrong, and being wrong toward "external" reopens marvel#11: the
respawn-instantly-forever loop that crash-loop backoff exists to stop.

So the fix is cause-agnostic. Marvel does not guess why the panes went; it
stops letting the blast radius of one event scale with the replica count.

## What changed

- `Controller.ReconcileOnce` carries a per-tick `charged` set into
  `noteReapedCrash`. A tick that finds k panes of one role gone records one
  crash for that role. Roles are charged independently, per-session
  handling (including the `restart_policy=never` verdict) is unchanged, and
  flapping still escalates because flapping repeats across ticks.
- `Manager.ReapDead` sets `HealthState = unhealthy` when it marks a session
  crashed. The pane's absence is the process-alive verdict; the health path
  already defers to the reap path for that check
  (`controller.go`, `evaluateHealth`).

Tests: `TestReapChargesOneCrashPerRolePerTick` (table: one replica, three
replicas of one role, two roles losing together),
`TestExternalLossDoesNotExhaustMaxRestarts` (budget intact, backoff holds
the tick, all three replicas back when the window elapses),
`TestReapDeadClearsStaleHealthReading` (table over the prior reading).

## Direction (a) is untouched and still live

The ticket's forward direction, a daemon respawning agents from stale
desired state, is unchanged by this and by everything shipped for
`aae-orc-kvcs`. Checked directly: a store opened against a bolt file
holding only a team record spawns processes on the first tick.

```
rehydrated teams=1 sessions=0 resource_version=2
spawned: repro-forward/squad-worker-g1-1 state=running pane=%2 cmd=sleep
spawned: repro-forward/squad-worker-g1-0 state=running pane=%1 cmd=sleep
```

`sleep` costs nothing. A `claude` or `codex` runtime in the same manifest
is live model spend against whatever workspace the record still names, and
`marvel daemon` is the whole trigger. The 2026-08-07 posture ("err on
accumulation rather than destruction") governs panes marvel finds; it says
nothing about desired state marvel rehydrates. Ticket items 3 and 4 are
scoped to assess rather than implement, so this is recorded, not fixed.

## Left open

- **`RestartCount` never decays.** There is no success-based reset, so a
  role that loses a pane once a month walks to saturation and freezes. K8s
  resets after a container stays up; marvel has no equivalent. This is a
  pre-existing property of `MaxRestarts`, not something an external kill
  introduces, but it is the reason saturation is terminal.
- **No recovery verb short of `marvel delete team`.** `ClearRoleHealthForTeam`
  is reachable only through the delete cascade, which destroys sessions to
  clear a counter.
- **The crashed-marker cap does not hold within a tick.**
  `clearStaleCrashed` reads the snapshot taken before the loop, where the
  sessions about to be marked still read `running`, so a mass loss leaves
  one marker per replica rather than the documented one per role. Cosmetic
  while the markers clear on the next spawn.
- **The victim daemon still records nothing** (`aae-orc-tt5e`).
