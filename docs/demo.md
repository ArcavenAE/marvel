# Marvel three-act demo

Marvel is a control plane for agent sessions. It keeps a declared team
running, notices when a session dies, and repairs it; it observes what every
session is doing, in one event vocabulary, across different agent harnesses;
and it changes a running agent's permission contract by editing a manifest,
with no restart. This runbook shows those three properties as copy-pasteable
command sequences, each mapped to the exact events marvel emits.

Every command below was run top to bottom against a live daemon on 2026-07-31.
Where the code does not produce what an operator might expect, the runbook says
so at that beat rather than papering over it.

## Prerequisites

- macOS or Linux with `tmux` and `just` on PATH.
- `just build` (produces `bin/marvel` and `bin/simulator`).
- Act 1 and Act 3 need no model auth. Act 1 uses only `sleep`; Act 3 needs the
  `claude` binary on PATH (the projection events fire whether or not claude is
  authenticated, because marvel writes the settings file itself).
- Act 2 needs the three harness binaries (`claude`, `codex`, `opencode`) and
  working auth for each. The per-session CPU and RSS columns populate without
  auth; the `agent.*` token and cost events need a real model turn.

Start each act from a clean daemon. Marvel persists state to
`~/.marvel/state/marvel.bolt`; to avoid one act's sessions bleeding into the
next, tear down and clear state between acts:

```sh
./bin/marvel stop --teardown        # end the daemon and every agent
rm -f ~/.marvel/state/marvel.bolt   # forget persisted resources
./bin/marvel daemon &               # fresh daemon
```

`marvel events` reads a bounded in-memory ring, so it is empty on a fresh
daemon and fills as the act runs.

## Watching live

The acts read better watched than replayed. Two tools:

- `marvel events --follow` (`-f`) tails the ring: it prints the current
  tail, then polls once a second and prints each new event exactly once,
  in order, using a ring-assigned sequence cursor. All the usual filters
  compose with it (`--workspace`, `--kind`, `--warnings`).
- `just demo-watch` builds a four-pane tmux operator console (driver
  shell, live session table, live event tail, daemon log poll). Attach
  with `tmux attach -t marvel-watch`, drive the beats from the top-left
  pane, and watch states flip in real time. The panes poll until a
  daemon appears, so start it in whichever order you like.

---

## Act 1 — Recover

Marvel keeps a team at its declared replica count. Three distinct loss paths
produce three distinct events. This act runs them one at a time.

### 1a. Unplanned pane loss: `session.crashed` then `session.created`

`demo-act1-recovery` runs two `sleep` workers with a process-alive healthcheck
(pane exists means healthy). Nothing removes a session on its own, so the only
loss is the one you cause.

```sh
./bin/marvel work examples/demo-act1-recovery.toml
sleep 5
./bin/marvel get sessions            # two workers, STATE running, HEALTH healthy
```

Kill one worker's pane out of band, the way a crashed agent process would
vanish. Read its pane id first, then kill the pane:

```sh
./bin/marvel describe session recover/line-worker-g1-0   # note "PaneID": "%N"
tmux kill-pane -t %1                                     # use the PaneID you saw
```

Marvel detects the vacated pane on its next reconcile tick, marks the session
crashed, and (after a crash-loop backoff) spawns a replacement:

```sh
./bin/marvel events --kind session.crashed     # "pane %1 gone"  (immediate)
sleep 35
./bin/marvel events --kind session.created     # replacement line-worker-g1-2
./bin/marvel get sessions                       # back to two healthy workers
```

Timing note: marvel treats a vanished pane as a crash and applies a 30-second
crash-loop backoff before respawning, so the replacement appears roughly 30 to
60 seconds after the kill, not instantly. That backoff is deliberate: it is
what stops a process that exits on startup from respawning in a tight loop.

TODO(finding): the task brief described this beat as `session.crashed` then
`session.restarted`. That is not what the pane-loss path emits. The recovery
pair for an unplanned loss is `session.crashed` then `session.created`.
`session.restarted` is a separate, health-driven event; see 1c below. The two
are different mechanisms and marvel does not conflate them.

If instead you use marvel's own kill command, the loss is an operator-initiated
delete, not a crash, and there is no backoff:

```sh
./bin/marvel kill recover/line-worker-g1-1
./bin/marvel events --kind session.deleted     # marvel-initiated removal
./bin/marvel events --kind session.created     # replacement spawns promptly
```

### 1b. Role removed from the manifest: `role.removed`

`demo-act1-roles` declares `primary` (1 replica) and `helper` (2 replicas).
`demo-act1-roles-removed` is the same team with `helper` dropped.

```sh
./bin/marvel work examples/demo-act1-roles.toml
sleep 4
./bin/marvel get sessions            # primary + two helpers

./bin/marvel work examples/demo-act1-roles-removed.toml
sleep 4
./bin/marvel events --kind role.removed      # "role removed from manifest, drained 2 session(s)"
./bin/marvel events --kind session.deleted   # one per drained helper
./bin/marvel get sessions                     # only primary remains
```

### 1c. Health-driven terminal cases: `session.restarted`, `session.failed`, `role.saturated`

`demo-act1-health` runs three `sleep` roles, each with a heartbeat healthcheck.
`sleep` never sends a heartbeat, so each role goes stale and marvel applies that
role's restart policy. This exercises the health path with no agent and no auth.

- `restarter` (restart_policy = always): restarts on every staleness, so it
  loops through `session.restarted` with a growing backoff.
- `failstop` (restart_policy = never): the stale session is marked failed and
  never restarted. The reconciler keeps the replica count by launching a fresh
  replica, so `session.failed` recurs as each replacement in turn goes stale.
- `capped` (restart_policy = always, max_restarts = 1): restarts once, then hits
  its cap and emits `role.saturated` plus `session.failed`, and is not respawned.

```sh
./bin/marvel work examples/demo-act1-health.toml
sleep 12
./bin/marvel events --workspace health --warnings
./bin/marvel events --kind health.failed        # first staleness on each role
./bin/marvel events --kind session.restarted     # restarter + capped, restart #1
./bin/marvel events --kind session.failed         # failstop, restart_policy=never

sleep 60
./bin/marvel events --kind role.saturated          # capped hit max_restarts=1
./bin/marvel events --kind session.restarted        # restarter now on restart #2
```

The healthcheck uses a 6-second timeout and a threshold of 1, so the first
action lands about 8 to 10 seconds after spawn; `capped` saturates about a
minute later, after one backoff window.

TODO(finding): two events in the health vocabulary are not observable through
this beat.

- `health.failed` fires only on the first transition to unhealthy, and the
  evaluator marks a session unhealthy while its failure count is still below
  the threshold. With `failure_threshold` greater than 1 the transition is
  therefore consumed before the threshold is reached and no `health.failed`
  event is emitted, even though the restart or fail action still fires. This
  manifest uses `failure_threshold = 1` precisely so `health.failed` is visible.
- `health.crashloop-backoff` is emitted only if a live session is re-evaluated
  inside its backoff window. On the always-restart path the session is deleted
  the moment it restarts, so no session exists to re-evaluate during the
  backoff, and the event does not surface in this beat. It remains in the
  vocabulary and is covered by the controller's tests.

### 1d. Stuck shift: `team.shift-timed-out`

A shift that never reaches readiness is aborted and rolled back with a
`team.shift-timed-out` event. The shift timeout defaults to 10 minutes, but the
daemon now takes a `--shift-timeout` flag (and the `MARVEL_SHIFT_TIMEOUT` env
var) so you can shorten it and see the beat without a 10-minute wait. Start a
daemon with a short timeout, apply a team with a never-heartbeats role, and
shift it: the new generation never becomes ready, so the shift aborts.

```sh
MARVEL_SHIFT_TIMEOUT=15s ./bin/marvel daemon &   # or: --shift-timeout 15s
./bin/marvel work examples/demo-act1-health.toml  # heartbeat roles that never beat
sleep 3
./bin/marvel shift health/ward
sleep 20
./bin/marvel events --kind team.shift-timed-out   # "shift stuck in launching past 15s, rolled back to gen 1"
```

A verified run against a one-role team with a heartbeat healthcheck and
`MARVEL_SHIFT_TIMEOUT=15s` emitted `team.shift-timed-out` roughly 17 seconds
after the shift started: `shift stuck in launching past 15s, rolled back to
gen 1`. The flag wins when set; otherwise the env var is parsed as a Go
duration; unset leaves the built-in 10-minute default.

---

## Act 2 — Observe

One team, three different agent harnesses, one event stream. `mixed-adapters`
runs a `claude`, a `codex`, and an `opencode` role, each headless with a small
prompt. Marvel redirects each harness's structured output through its adapter,
normalizes the harness-specific dialect into one event vocabulary, and tags
every event with workspace, team, role, and session.

```sh
./bin/marvel work examples/mixed-adapters.toml
sleep 4
./bin/marvel get sessions        # CPU% and RSS populated for all three harnesses
```

`marvel get sessions` samples the process table, so CPU% and RSS are real for
every session regardless of harness, and need no auth. Watch the normalized
agent stream from all three:

```sh
./bin/marvel events --workspace mixed
./bin/marvel events --kind agent.turn.completed   # tokens in/out per turn
./bin/marvel events --kind agent.session.ended    # per-session cost, tokens, duration
```

A verified run produced, across the three harnesses uniformly:
`agent.session.started`, `agent.turn.started`, `agent.turn.completed` (with
token counts), `agent.message.completed`, and `agent.session.ended` (with cost,
token totals, and duration). That the same five event kinds describe three
unrelated harnesses is the point of the act.

Deterministic vs auth-dependent:

- Deterministic (no auth): the three sessions spawn, and the CPU% and RSS
  columns populate.
- Auth-dependent: the `agent.*` token, message, and cost events require each
  harness to authenticate and take a real model turn. Without auth a harness
  exits early and you see `session.crashed` instead of an agent stream.

TODO(finding): the CTX% column shows `-` for these harnesses. Context pressure
has exactly one producer in marvel today, the heartbeat RPC, and none of the
claude, codex, or opencode adapters send it. The column exists and is correct
(it renders absence rather than a misleading 0%), but "context" is not an
observable signal for these three harnesses yet. The task brief's Act 2
"cpu/rss/context" is accurate for cpu and rss; context is not populated.

Because each role is headless with a one-shot prompt, the harness exits when its
turn completes, and marvel reaps the vacated pane with `session.crashed`. That
is expected for a headless one-shot: the work is done, the process is gone.

Minor: the claude adapter logged one `agent.error` "unmapped: assistant thinking
block (no v1 event kind)" during the verified run. It is a benign parse gap (a
thinking block has no mapped v1 event kind), not a session failure.

---

## Act 3 — Control plane

Marvel projects a Policy (a Claude Code settings fragment) into a per-session
file the harness reads, and re-projects it when the manifest changes, so a
running agent's permission contract changes with no restart.

`policy-projection.toml` declares `reviewer-contract` version 1 (read-only: allow
Read/Grep/Glob, deny Bash/Write/Edit) and a claude reviewer that references it.

```sh
./bin/marvel work examples/policy-projection.toml
sleep 4
./bin/marvel get policies                       # VERSION 1
./bin/marvel events --kind policy.projected     # "projected at spawn"
./bin/marvel get sessions                        # reviewer running, GEN 1
```

`policy-projection-v2.toml` is the same workspace, team, and role, with the
policy edited: version 2 now allows Edit and adds a SessionStart hook. Apply it
over the running v1 state:

```sh
./bin/marvel work examples/policy-projection-v2.toml
sleep 3
./bin/marvel get policies                       # VERSION now 2
./bin/marvel events --kind policy.projected     # a second event: "re-projected after manifest change"
./bin/marvel get sessions                        # same session, same GEN 1, no restart
```

The second `policy.projected` event fires against the same running session
(same name, same generation, no restart). Marvel rewrote that session's settings
file in place; a verified run showed the file's content change from the v1
allow/deny lists to the v2 lists plus the SessionStart hook. Claude Code's file
watcher re-reads the settings file, so the contract change lands live.

This act is deterministic given the `claude` binary on PATH. The projection
events fire because marvel writes the file, independent of whether claude is
authenticated. The one requirement for the live re-projection beat is that the
reviewer session is still running when you apply v2 (an interactive claude
session stays up while it waits for input, so it is).

Roles whose harness has no Claude Code settings surface (codex, opencode,
generic) log the policy as advisory and are not projected. Nothing is dropped
silently.

---

## Cleanup

```sh
./bin/marvel stop --teardown
rm -f ~/.marvel/state/marvel.bolt
```

`just clean` also removes `bin/` and the default socket, but it only kills the
`marvel-demo` tmux session; `stop --teardown` is what ends every agent across
all workspaces.
