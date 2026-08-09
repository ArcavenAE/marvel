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
  authenticated, because marvel writes the settings file itself). The Act 3
  extension is the exception: CTX% only appears once a session takes a real
  turn, so that beat needs working claude auth and spends two short turns.
- Act 2 needs the three harness binaries (`claude`, `codex`, `opencode`) and
  working auth for each. The per-session CPU and RSS columns populate without
  auth; the `agent.*` token and cost events need a real model turn.

### Run the acts on a scratch layout

Run the demo on its own layout, not on the one your real fleet uses. Every
piece of marvel's state has a flag, and the tmux namespace has an env var:

```sh
D=/tmp/marvel-demo-layout; mkdir -p "$D"
MARVEL_TMUX_SOCKET=marvel-demo-layout ./bin/marvel daemon \
  --socket     "$D/m.sock" \
  --state-bolt "$D/marvel.bolt" \
  --log-file   "$D/daemon.log" \
  --pidfile    "$D/daemon.pid" &

export MARVEL_SOCKET="$D/m.sock" MARVEL_TMUX_SOCKET=marvel-demo-layout
```

Export those two in every shell that drives the acts, including the
`just demo-watch` panes, or the client talks to a different daemon than the
one you started.

`--state-bolt` at a fresh path is the load-bearing one. An empty store has no
desired state to rehydrate, so the daemon adopts nothing and spawns nothing on
its first tick. Pointed at a populated store instead, a daemon reconciles
whatever it finds, which can mean real agent processes and real model spend
you did not ask for.

`MARVEL_TMUX_SOCKET` is the one to not skip. The tmux server name is otherwise
derived from the layout's home, so a daemon started without it lands on the
same tmux server your real sessions live on, where its reconcile pass meets
panes it does not own.

Do not reach for `HOME` to get this. It moves marvel's layout and the
harness's credential lookup together: the daemon is isolated, and the claude
it spawns cannot log in, while `marvel get sessions` reports the session
`running` and `healthy` throughout. See
`_kos/findings/finding-025-home-isolation-breaks-harness-auth.md`.

Between acts, start clean so one act's sessions do not bleed into the next:

```sh
./bin/marvel stop --teardown   # end the daemon and every agent
rm -rf "$D" && mkdir -p "$D"   # forget persisted resources
# then the daemon command above again
```

`marvel events` reads a bounded in-memory ring, so it is empty on a fresh
daemon and fills as the act runs.

### Running against your real fleet instead

If you are demonstrating on the fleet you actually operate, drop the flags and
run `./bin/marvel daemon` on the default `~/.marvel` layout. Then the
between-acts reset above is destructive to real work, so do not use it: the
teardown ends every agent in every workspace, and clearing
`~/.marvel/state/marvel.bolt` discards the desired state they came from.

The two modes are easy to mix up in a terminal you have been pasting into.
`./bin/marvel config tmux-server` tells you which one you are on: the scratch
layout answers with the name you exported, the real one answers
`marvel-<hash>`.

## Watching live

The acts read better watched than replayed. Two tools:

- `marvel events --follow` (`-f`) tails the ring: it prints the current
  tail, then polls once a second and prints each new event exactly once,
  in order, using a ring-assigned sequence cursor. All the usual filters
  compose with it (`--workspace`, `--kind`, `--warnings`).
- `just demo-watch` builds a four-pane tmux operator console:

  ```
  +---------------------------+----------+
  | live session table (149w) | CLI      |   50% height
  +---------------------------+----------+
  | live event tail                      |   full width
  +--------------------------------------+
  | daemon log poll                      |   full width
  +--------------------------------------+
  ```

  Attach with `tmux attach -t marvel-watch` and drive the beats from the
  CLI pane, top right, already selected on attach. The session table sits
  beside it because those two are the pair you work in: you type a command
  and watch the states flip next to it. Events and logs run full width
  underneath because their lines are long, and a half-width pane wraps
  them into noise.

  The table pane is 149 columns, which is every column of `marvel get
  sessions` plus 12 characters of the model. That is where the LLM column
  starts in the worst case the demo actually produces, a
  `crashloop-backoff` state (Act 1 demonstrates it) beside a 26-character
  agent name. Rows are clipped to the pane rather than wrapped: a row
  carrying a real model name runs past 160 columns, and a wrapped table
  defeats the one thing this pane is for. Resize the pane and the clip
  follows it.

  **The console wants a terminal at least 250 columns wide.** The table is
  content-fixed and the CLI is not, so the CLI takes every column the table
  does not: at 250 that is 149 and 100, at 300 it is 149 and 150. tmux
  would otherwise scale both proportionally, which is wrong in both
  directions, stretching the table into blank padding on a wide terminal
  and dropping its LLM column on a narrow one. A resize hook re-pins it,
  including on attach, since attaching is itself a resize.

  Below 250 the CLI keeps a 100-column floor and the table clips instead,
  because a full table beside an unusable shell is the worse trade. At 220
  the table runs 119 columns and loses LLM and RUNTIME. Both numbers are
  `session_pane_width` and `cli_min_width` at the top of the justfile if
  you want the other trade.

  The panes poll until a daemon appears, so start it in whichever order you
  like.

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
tmux -L "$(./bin/marvel config tmux-server)" kill-pane -t %1   # use the PaneID you saw
```

The `-L` is load-bearing. Marvel's panes live on a per-HOME tmux server, so a
bare `tmux kill-pane -t %1` reaches tmux's shared default server instead and
kills whatever `%1` is there, which is some other pane of yours.

Marvel detects the vacated pane on its next reconcile tick, marks the session
crashed, and (after a crash-loop backoff) spawns a replacement:

```sh
./bin/marvel events --kind session.crashed     # "pane %1 gone"  (immediate)
./bin/marvel get sessions                       # crashed row, DESK blank, RSS 0B
sleep 75
./bin/marvel events --kind session.created     # replacement line-worker-g1-2
./bin/marvel get sessions                       # back to two healthy workers
```

Timing note, measured 2026-08-09: the crash lands within about 2 seconds and
the replacement about 62 seconds after the kill. Marvel treats a vanished pane
as a crash and charges the role a restart, and the first charge sets a
60-second window; the second is 2 minutes, the third 4, doubling to a 5-minute
ceiling. So kill the same role twice in a session and the second wait is twice
as long. That backoff is deliberate: it is what stops a process that exits on
startup from respawning in a tight loop.

Nothing announces the wait. `health.crashloop-backoff` fires only when a live
session is re-evaluated inside its window, and on this path the session is a
crashed marker rather than a live one, so the ring stays quiet between the two
events above. The crashed row in `get sessions` is the whole signal: it holds
its place with `DESK` blank and `RSS` at `0B` until the replacement spawns. If
you sample too early, read the row, not the empty event filter.

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
  the role goes terminal. The reconciler freezes replacement spawns, so the
  failed row and its pane stay visible for post-mortem. Recovery is
  `marvel delete team` + re-apply, same as a saturated role.
- `capped` (restart_policy = always, max_restarts = 1): restarts once, then hits
  its cap and emits `role.saturated` plus `session.failed`, and is not respawned.

```sh
./bin/marvel work examples/demo-act1-health.toml
sleep 12
./bin/marvel events --workspace health --warnings
./bin/marvel events --kind health.failed        # first staleness on each role
./bin/marvel events --kind session.restarted     # restarter + capped, restart #1
./bin/marvel events --kind session.failed         # failstop, restart_policy=never

sleep 90
./bin/marvel events --kind role.saturated          # capped hit max_restarts=1
./bin/marvel events --kind session.restarted        # restarter now on restart #2
```

The healthcheck uses a 6-second timeout and a threshold of 1, so the first
action lands about 7 seconds after spawn; `capped` saturates about 70 seconds
after that, once its one backoff window has elapsed and the replacement has
gone stale in turn.

Then check the table, because the three policies leave three different rows
behind and only one of them is the row a reader expects. Measured 2026-08-09,
about two minutes in:

```sh
./bin/marvel get sessions   # capped + failstop, both failed; restarter absent
```

- `failstop` holds its row from the first failure onward: `failed`,
  `unhealthy`, pane alive. It never leaves the table.
- `capped` leaves the table for about 60 seconds after its one restart, comes
  back, saturates, and then holds its row permanently as `failed` /
  `unhealthy`. Saturation is what makes the row stay, not what removes it.
- `restarter` is the one that goes missing. Every restart deletes the session
  and the replacement waits out the backoff, so the row is absent for 60
  seconds, then 2 minutes, then 4. Look at an arbitrary moment a few minutes
  in and a healthy always-restart role is more likely to be missing from the
  table than present.

A beat that verifies only an event cannot catch a state defect, which is how
the 1d rollback bug (`aae-orc-d0pt`) survived a verified run. Read the table
after the events, every time.

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

Check the state the abort left behind, not just the event. The team must be
back on the generation the message names, and every session it holds must
carry that generation:

```sh
./bin/marvel describe team health/ward    # Generation: 1, no shift in progress
./bin/marvel get sessions                 # every row a g1 name
```

An abort that has already drained a role cannot roll back, because that
role's old generation is gone. It stops where it stands instead and says so:
`shift stuck in launching past 15s, stopped at gen 2 with 1 of 3 roles
shifted`. Normal reconciliation converges the roles that never got a turn.

### 1e. Whole-role loss: one crash charged, not one per replica

Beat 1a kills one pane of a two-replica role, which charges that role one
crash. It would also charge one crash under the accounting marvel used
before #162, so it cannot show what changed. This beat loses every replica
of a role at the same instant, which is what a dying tmux server, a host
reboot, or a foreign daemon's reclaim actually looks like.

`demo-act1-fleet-loss` is one role with three replicas. Three is the
smallest count where the difference is unmistakable.

```sh
./bin/marvel work examples/demo-act1-fleet-loss.toml
sleep 6
./bin/marvel get sessions   # three workers, healthy
```

Kill the workspace's whole tmux session rather than one pane. The session is
named for the workspace, so this reaches `bulk` and nothing else:

```sh
tmux -L "$(./bin/marvel config tmux-server)" kill-session -t marvel-bulk
sleep 10
```

The ring reports the loss per replica, because three sessions did die:

```sh
./bin/marvel events --kind session.crashed   # three events
```

The charge is in the log, not the ring, and that is where the change shows:

```sh
./bin/marvel daemon logs -n 200 | grep reap
```

A verified run, 2026-08-09, one second after the kill:

```
reap: session bulk/line-worker-g1-0 crashed (role bulk/line/worker restart #1, next backoff=59.99s)
reap: session bulk/line-worker-g1-1 crashed with the rest of role bulk/line/worker in one tick; already charged
reap: session bulk/line-worker-g1-2 crashed with the rest of role bulk/line/worker in one tick; already charged
```

One charge, two refusals to charge again, and the role sits at restart #1
with a 60-second window. Charged per replica the same event would have put
it at restart #3 with a 4-minute window, and a role with `max_restarts = 3`
would have had nothing left after a single incident that killed no more
agents than one tick's worth.

All three replicas come back together when that one window elapses:

```sh
sleep 70
./bin/marvel get sessions   # three workers, healthy, ~64s after the kill
```

They also come back under their original names. The index is
`max(existing)+1` with no gap filling, so a role that loses every replica
frees every key and gets all of them back, while a role that loses one
replica out of three sees the replacement take a new index.

Marvel is deliberately not guessing at the cause here. It cannot tell an
external kill from an application exit at the point the reap runs: both
present as a pane id tmux no longer lists, and a single-pane session
collapses its server when its own process exits, so the tempting
discriminator does not survive a check. The rule bounds the blast radius of
one tick instead of classifying it. Flapping still escalates, because
flapping repeats across ticks.

---

## Act 2 — Observe

One team, three different agent harnesses, one event stream — and both CTX%
producers side by side. `mixed-adapters` runs a `claude`, a `codex`, and an
`opencode` role headless with a small prompt, plus a fourth role: the same
claude binary run as an interactive TUI.

Role names carry the mode so the table reads at a glance: `-p` =
print/headless (`analyst-p`, `builder-p`, `scout-p`), `-t` = TUI
(`analyst-t`). The suffix rides into every session name and event tag.

Marvel redirects each headless harness's structured output through its
adapter, normalizes the harness-specific dialect into one event vocabulary,
and tags every event with workspace, team, role, and session.

```sh
./bin/marvel work examples/mixed-adapters.toml
sleep 4
./bin/marvel get sessions        # CPU% and RSS populated for all four sessions
```

`marvel get sessions` samples the process table, so CPU% and RSS are real for
every session regardless of harness, and need no auth. Watch the normalized
agent stream from the three headless roles:

```sh
./bin/marvel events --workspace mixed
./bin/marvel events --kind agent.turn.completed   # tokens in/out per turn
./bin/marvel events --kind agent.session.ended    # per-session cost, tokens, duration
```

The `-t` row is the CTX% beat. A TUI emits no parseable stream, so its
context figure arrives through the other producer: `context_feed =
"statusline"` projects statusline hooks that forward the harness's own
measurement through `marvel ctx-forward` to the heartbeat RPC
(finding-011). CTX% is `-` until the session takes a turn, so inject one:

```sh
./bin/marvel inject mixed/matrix-analyst-t-g1-0 "say only the word ready" -e
sleep 20
./bin/marvel get sessions   # -p rows meter via stream; the -t row via statusline
```

The `-t` pane's own bottom line shows the human-facing half of the same
feed ("Haiku 4.5 · CTX 13% · $0.04"); attach to it via the DESK column to
drive it by hand.

A verified run produced, across the three harnesses uniformly:
`agent.session.started`, `agent.turn.started`, `agent.turn.completed` (with
token counts), `agent.message.completed`, and `agent.session.ended` (with cost,
token totals, and duration). That those same five kinds describe three
unrelated harnesses is the point of the act. They are five of the thirty-five
the ring carries; `marvel events --list-kinds` prints the rest.

Deterministic vs auth-dependent:

- Deterministic (no auth): the three sessions spawn, and the CPU% and RSS
  columns populate.
- Auth-dependent: the `agent.*` token, message, and cost events require each
  harness to authenticate and take a real model turn. Without auth a harness
  exits early and you see `session.crashed` instead of an agent stream.

CTX% producers, current state (supersedes an earlier TODO here that said
the heartbeat RPC was the only producer): there are three. The stream-fed
usage accountant meters headless sessions; the statusline feed
(`context_feed`, finding-011) meters interactive claude via the heartbeat
RPC; and the heartbeat RPC accepts any cooperative reporter, which now
covers codex as well as the simulator. Codex reports through a hook on
its rollout file rather than a statusline, because its `exec --json`
stream carries a running total instead of a level; the operator installs
that stanza in `~/.codex/config.toml` by hand, since marvel does not
write to `CODEX_HOME`. Setup for both feeds is in the user guide under
"Interactive claude context pressure" and "Codex context pressure".
Interactive opencode remains unmetered (aae-orc-7hzb).

Because each `-p` role is headless with a one-shot prompt, the harness exits
when its turn completes, and marvel reaps the vacated pane with
`session.crashed`. That is expected for a headless one-shot: the work is
done, the process is gone. The `-t` role stays running.

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

### Act 3 extension: shift onto a new metering contract

Policy content re-projects onto a running session. A runtime field does not.
This beat shows the difference, and shows `marvel shift` as the migration
path for the half that cannot be retrofitted. Verified end to end on
2026-08-09; it needs claude auth and spends two short model turns.

Start a team whose interactive claude role has no `context_feed`, and give
it a turn so the `-` means something:

```sh
./bin/marvel work examples/context-feed-off.toml
sleep 8
./bin/marvel inject feed/watch-watcher-g1-0 "say only the word ready" -e
sleep 30
./bin/marvel get sessions   # CTX% is "-", LLM is "-"
./bin/marvel capture feed/watch-watcher-g1-0   # the pane's own line has both
```

That contrast is the whole motivation. In the verified run the pane reported
14% and $1.18 on its own status line while marvel's CTX% cell stayed `-`. The
harness is metering itself and printing the figure to the human, and marvel
cannot see any of it.

Now add the feed and re-apply. Nothing happens, and nothing is supposed to:

```sh
./bin/marvel work examples/context-feed.toml
sleep 6
./bin/marvel get sessions                        # still GEN 1, still "-"
./bin/marvel events --kind policy.projected      # "no events"
```

A session's runtime is frozen at spawn, and re-projection reads
`context_feed` from the session's copy rather than the role's, so a manifest
change reaches only sessions created after it. The live re-projection that
works for policy content does not retrofit the feed. Tracked as
`aae-orc-mjrm`; until it is fixed, a shift is the migration path, and the
`no events` above is how you tell the wrinkle from a typo in your manifest.

Shift, and the new generation spawns with the feed:

```sh
./bin/marvel shift feed/watch
sleep 25
./bin/marvel events --kind policy.projected   # "projected at spawn" for g2
./bin/marvel get sessions                      # GEN 2, CTX% still "-" (no turn yet)
./bin/marvel inject feed/watch-watcher-g2-0 "say only the word ready" -e
sleep 35
./bin/marvel get sessions                      # CTX% populated, LLM populated
```

A verified run produced this event sequence in five seconds, and the g2 row
then read `CTX% 14%` with `LLM Opus 5 (1M context)`. It ran against an
equivalent manifest pair in workspace `act3`, so the keys below read
`act3/meter-watcher-*` where the commands above give you
`feed/watch-watcher-*`:

```
team.shift-started     act3/meter               gen 1→2 roles=[watcher]
policy.projected       act3/meter-watcher-g2-0  policy "" projected at spawn: ...
session.created        act3/meter-watcher-g2-0  pane %2
team.shift-role-ready  act3/meter               gen 2 ready, gate=running, draining gen 1
session.deleted        act3/meter-watcher-g1-0  session deleted
team.shift-completed   act3/meter               gen 2 active
```

Note `policy "" projected at spawn`. The role declares no policy, so the
projected file is the feed and nothing else. That empty policy name is the
signal that `context_feed` alone caused the projection.

Two things worth expecting before you run this in front of anyone.

**Marvel's status line replaces yours in that pane.** The projected
`--settings` file wins over your own `~/.claude/settings.json` for the
`statusLine` key, so the g2 pane shows marvel's format (`Opus 5 (1M context)
· CTX 14% · $1.18 · acct ...`) where g1 showed whatever you have configured.
Nothing outside the pane changes, and your settings file is not touched.

**`ContextLimit` stays 0 on a fed session.** `marvel describe session` shows
`ContextPercent` and `ContextModel` populated with `ContextLimit: 0` and an
empty `ContextLimitSource`, because the feed hands over a percentage the
harness already computed rather than tokens for marvel to divide. A blank
limit here is the feed working, not a resolution failure.

### Act 5 — Meter (PLANNED, not written)

A dedicated metering act: `context-feed.toml` + injected turns + watching
CTX% climb across the table, tied into budget admission (`max_tokens`
clauses refusing over-budget work). Deferred until the per-subagent context
surface and the OTEL topology decision (aae-orc-mqgf) land, so the act
demonstrates a settled layer rather than a moving one.

---

## Cleanup

On the scratch layout from Prerequisites, cleanup is the daemon plus the
directory that held all of its state:

```sh
./bin/marvel stop --teardown
rm -rf /tmp/marvel-demo-layout
unset MARVEL_SOCKET MARVEL_TMUX_SOCKET
```

`unset` matters as much as the `rm`. A leftover `MARVEL_SOCKET` in your shell
points every later `marvel` command at a socket that no longer exists, and the
connection error that produces looks nothing like its cause.

On the default layout, the equivalent is `./bin/marvel stop --teardown` and
`rm -f ~/.marvel/state/marvel.bolt`. That second command discards the desired
state of your real fleet, so run it only if that is what you mean.

`just clean` also removes `bin/` and the default socket, but it only kills the
`marvel-demo` tmux session; `stop --teardown` is what ends every agent across
all workspaces.

Since #128 each HOME has its own tmux server, so anything reaching marvel's
sessions with raw tmux needs `-L`. Ask the binary for the name rather than
recomputing it:

```sh
tmux -L "$(marvel config tmux-server)" attach -t marvel-demo
tmux -L "$(marvel config tmux-server)" list-sessions
```

A bare `tmux kill-session -t marvel-demo` now targets tmux's shared default
server and silently reaches nothing. `just clean` was fixed to pass `-L`, and
degrades with a message when `bin/marvel` is not built rather than pretending
it cleaned up.

The `marvel-watch` operator console from `just demo-watch` is unaffected. It is
a plain tmux session of your own on the default server, and its panes reach the
daemon over the control socket, so `tmux attach -t marvel-watch` is still
correct as written above.
