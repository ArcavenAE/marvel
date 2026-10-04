# Runbook: kinu's bus from the hand-started phase-0 broker to `mode: managed`

Status: runbook for review, 2026-10-04. Docs only. **Nothing here runs on
kinu without the operator present.** The rehearsal (section 3) is an
operator-attended session of its own and never touches the live bus, hub or
daemon; the cutover (section 6) is a scheduled window after it, run only when
the operator says go. Written to assume the cutover goes badly.

- Issue: #534 (the runbook link goes there). Tracks aae-orc-bxg5f. Design:
  `bus-as-service.md` sections 6 and 8.2; director brief 10
  (`sim/design/local-broker-supervision.md` section 8, item 8, which spells
  it `managed: true`; the config spells it `mode: managed`).
- Run after `kinu-store-migration-runbook.md`, in its own window (that
  runbook, section 7).
- Checked against marvel `origin/main` c99ce98, director `origin/main`
  faed978, and the live kinu processes on 2026-10-04 between 03:28Z and
  03:37Z (read-only), with the director registration and the broker pidfile
  rechecked at about 04:15Z.

## 1. What runs today

| Piece | Live state | Touched by this cutover |
|---|---|---|
| Phase-0 broker | `nats-server -c director/probe/nats-phase-0/nats-server.conf`, started by `start.sh` on 2026-09-26; 127.0.0.1:4222, monitor 8222, anonymous, store `~/.director/nats/store`, one leaf out to the hub (`include "leaf-kinu.conf"`) | **replaced** |
| Global hub | `nats-server --config ~/.director/nats-global/nats-server.conf`, started 2026-09-25; 0.0.0.0:4242, monitor 127.0.0.1:8242, leafnodes 0.0.0.0:7442, domain `global`. Three leaves: kinu's phase-0, mokuzai, and a third that may be corporate's (inferred from its address; the checked-in hub conf names only the kinu and mokuzai leaf users) | **not touched**: no stop, reload or edit |
| marvel daemon | b533ca5 today; the release from the store-migration window by the time this runs. Its cwd is `~/work/aae-orc`; `~/.marvel/config.yaml` has no bus entry; `marvel bus status` says none is configured; `marvel credential list` is empty | gains a bus entry; restarted |
| kinu sessions | 32, across 6 teams (arcaven 13) | every one respawned |
| The operator's director session | its `director-mcp` entry in `~/.claude.json` runs the bare `director-mcp` binary and carries no `DIRECTOR_NATS_*` key (counted, values not read) | switched to the `director-mcp-seat` wrapper at step 8 |

## 2. Preconditions (all before the window is set)

1. **Daemon build.** It includes aae-orc-wzexa (959bf90), aae-orc-vy6k7
   (18ea860) and #279 (b798131). b533ca5 already does; the store-migration
   window comes first anyway.
2. **The director seat credential (NOT MET today).** What each client
   actually reads:
   - **The bare `director-mcp` binary** reads `DIRECTOR_NATS_CREDS` (a creds
     file), or else `DIRECTOR_NATS_USER` plus `DIRECTOR_NATS_PASS`, the
     password **value** (director `bus.go:94-98`). Nothing reads a
     `DIRECTOR_NATS_PASS_FILE`; `bus-as-service.md` 6.2 names that variable,
     and that is a defect in the design text (marvel-managed-bus-review:121).
   - **The `director-mcp-seat` wrapper** (`probe/nats-phase-0/director-mcp-seat`)
     uses `DIRECTOR_NATS_USER`/`DIRECTOR_NATS_PASS` when both are set.
     Otherwise it reads `$MARVEL_BUS_STATE/director.pass` (default
     `~/.marvel/state/nats/director.pass`, the file the managed daemon renders
     under its StateDir) and exports `DIRECTOR_NATS_USER=director` and the
     password from it. With no such file it exits 1 (lines 57, 83-93). It also
     needs `DIRECTOR_TEAM` and `DIRECTOR_WORKSPACE` set outside a marvel
     session.

   The operator's registration runs the bare binary with no `DIRECTOR_NATS_*`
   keys, so after the flip it offers no credential and is refused. The plan:
   prepare a `director-mcp-seat` registration (command, `DIRECTOR_TEAM`,
   `DIRECTOR_WORKSPACE`, and no `DIRECTOR_NATS_*` keys), and switch to it **at
   window step 8, not before**: until the daemon renders `director.pass`, the
   wrapper exits 1. Putting a password value into `~/.claude.json` for the bare
   binary is the fallback, not the plan, because it leaves the secret at rest
   in the harness config. Without one of the two, the flip locks the human out
   of the bus (`bus-as-service.md` 6.2).
3. **The leaf seed.** The operator has the kinu leaf NKey seed as a file for
   `credential put bus/leaf --value-file`, and has checked, without printing
   the seed, that its public key is the kinu leaf user in the hub's conf
   (line 35). A mismatch leaves kinu with a working local bus and no hub link
   (`bus.leaf.unenrolled`): survivable, and recovered in the window.
4. **Rehearsal passed** (section 3), its transcript linked from #534, with the
   broker gap and the per-seat respawn time measured.
5. **Quiet fleet.** No merge, shift or apply in flight. mokuzai's
   supervisor, and corporate's if that cluster leafs into this hub, are told kinu seats will be unreachable for the
   window, and that global mail to them queues at the hub.
6. **Start directories.** The store-migration window has run, and its
   precondition 5 has settled where each team spawns: either each team
   declares its root, or the operator accepted the legacy stamp of the
   daemon's cwd. This window's step 9 is the first fleet-wide spawn since then,
   so every one of the 32 seats lands wherever that decision put it. Today 30
   of the 38 panes on the marvel tmux server start in the tmux server's cwd,
   a worktree path (`tmux list-panes -a -F '#{pane_start_path}'` on the marvel
   socket). After the stamp they would start in `~/work/aae-orc` instead.
   Capture each team's `WorkDir` (`marvel describe team`) into `$W` at step 0.
7. **Handoffs.** Every kinu seat is told before the window to write its
   handoff at the window's start. A respawn without one loses that seat's
   context.

## 3. Rehearsal (operator-attended, on kinu, before the window)

It runs on kinu with the operator present, because it starts nats-servers, a
daemon and a shim beside the live ones. Every process is scratch: scratch
HOME, scratch ports, scratch NKeys, a scratch tmux server. The real seed, the
real stores, the live hub and the live daemon are never read, signalled or
touched, and that is checked, not assumed.

```sh
for v in ${(k)parameters[(I)MARVEL_*]}; do unset "$v"; done   # zsh; bash: unset $(compgen -v MARVEL_)
env | grep '^MARVEL_'                       # must print nothing (CLAUDE.md, #504)
S=$(mktemp -d)                              # scratch HOME: config, StateDir, stores
m() { HOME="$S" marvel --socket "$S/m.sock" "$@"; }   # the ONLY way the rehearsal calls marvel
# scratch ports: phase-0 14222 (monitor 18222), hub 14242 (monitor 18242) / leaf 17442,
# a second scratch leaf standing in for mokuzai on 24222,
# the scratch MANAGED broker on 14222 (monitor 18222) once phase-0 is stopped,
# an anonymous scratch broker for step 6 on 34222 (monitor 38222)
```

The daemon reads `~/.marvel/config.yaml` through `os.UserHomeDir`
(`internal/config/config.go:742-748`), so `HOME="$S"` gives the scratch daemon
its own config; the client must still name `--socket` (marvel `CLAUDE.md`).
**Every marvel command in this section goes through `m`.** A bare `marvel`
with `MARVEL_*` unset resolves the live daemon: a bare `apply` would put a
shell team on the live fleet, and a bare `credential put bus/leaf` would store
the scratch seed in the live store, so window step 5 would boot with the
wrong seed. The operator reads each step's commands for a bare `marvel`
before running it.

Run the rehearsal from a fresh operator terminal, never a seat pane: a seat's
shell carries the live `NATS_URL` and `DIRECTOR_*` values.

**Isolation checks, before each scratch process starts:**

- `env | grep -E '^(MARVEL_|DIRECTOR_|NATS_)'` prints nothing in the shell
  that starts it, except the variables the step sets on purpose on that one
  command line (step 4's).
- Its port is free: `lsof -nP -iTCP:<port> -sTCP:LISTEN` prints nothing for
  14222, 18222, 14242, 18242, 17442, 24222, 34222 and 38222. This is the guard against a scratch
  broker binding a live port.
- A scratch nats-server's conf names its store as a **literal** path under
  `$S` (expanded when the conf is written), never a variable. The checked-in
  phase-0 conf has `store_dir: $DIRECTOR_PHASE0_STORE` (director
  `probe/nats-phase-0/nats-server.conf:26`); left in, it resolves from the
  environment, and if that points at the live store, two servers share it.
  Before start, this prints **nothing**:
  ```sh
  grep -n -E '\$[A-Z_]|(^|[^0-9])(4222|8222|4242|8242|7442)([^0-9]|$)|include|leaf-kinu|\.director' <conf>
  ```
  The live ports are anchored on non-digits, because every scratch port
  contains a live one (14222 holds 4222, 17442 holds 7442), and an unanchored
  pattern can never pass on a correct conf. Checked 2026-10-04: on a scratch
  conf with ports 14222/18222, a literal store path and a remote to
  `127.0.0.1:17442`, it prints nothing; it matches each of `port: 4222`,
  `http_port: 8222`, `listen: 127.0.0.1:4242`, a remote to `:7442`,
  `store_dir: $DIRECTOR_PHASE0_STORE` and `include "leaf-kinu.conf"`. Run the
  control once at the start (`echo 'port: 4222' | grep -E '<pattern>'` prints
  the line), so a typo in the pattern cannot pass as a clean conf. Also `grep -n store_dir <conf>`
  prints one line naming `$S`'s literal path. After start, the server's own
  report must agree: `curl -s 127.0.0.1:<scratch monitor>/varz | jq -r
  .jetstream.config.store_dir` is under `$S`. `ps -o command=` shows only
  `-c`, so it is not the check.
- The scratch daemon's socket is `$S/m.sock`, and `MARVEL_TMUX_SOCKET` is
  `rehearse-a3`. `tmux -L rehearse-a3 ls` lists only the scratch server. Its
  first call is a read-only `bus status` with no "is rooted at" warning.
  It cannot take the live socket, since it takes the socket lock first
  (`daemon.go:447-457`).
- Every client command names its server: `nats -s nats://127.0.0.1:14222` (or
  the scratch hub), never a default context. The shim gets an explicit
  `NATS_URL` to a scratch port.
- **The live-unchanged check**, at the start and after each step, read-only:
  `curl -s 127.0.0.1:8242/leafz` (the **live** hub's monitor) and `curl -s
  127.0.0.1:8222/jsz` (the live phase-0 monitor), saved and compared with the
  first capture; `lsof -nP -iTCP:4222 -sTCP:LISTEN` and `ps -o
  pid,lstart,command -p <phase-0 pid>,<hub pid>` show the same pids and start
  times. No nats client is ever pointed at 4222 or 4242 during the rehearsal.
  Any change stops the rehearsal.

Steps, each with a pass condition:

1. **Build today's shape. Never use `start.sh` here.** It always runs the
   checked-in `nats-server.conf`, which includes `leaf-kinu.conf` with the real
   kinu seed pointed at the live hub on 7442, so a scratch broker started
   through it would join the live hub as kinu's leaf. Instead, write
   `$S/p0/nats-server.conf` from the checked-in one with the scratch ports, a
   `store_dir: <literal $S path>/p0/store` in place of
   `$DIRECTOR_PHASE0_STORE`, and the leaf include **removed**, then a
   leafnode remote only to the scratch hub (17442) with the scratch kinu-leaf
   NKey. **Check it before it starts**, with the conf and varz checks above
   (monitor 18222). The scratch hub and the scratch mokuzai leaf get the same
   two checks on their own monitors. Then run it directly, `nats-server -c $S/p0/nats-server.conf --pid
   $S/p0/nats-server.pid --log $S/p0/nats-server.log &`, and confirm with `curl
   -s 127.0.0.1:18222/leafz` that its only leaf is the scratch hub. Provision the streams as `verify-auth.sh` does,
   and publish a few messages to AGENT_INBOX and AGENT_AUDIT, every call as
   `nats -s nats://127.0.0.1:14222 ...`. Start a scratch
   hub (domain `global`, scratch kinu-leaf and mokuzai-leaf NKeys) and the
   scratch mokuzai leaf. Pass: the **scratch** hub's `/leafz`
   (`127.0.0.1:18242`) lists both scratch leaves, and the live hub's (`8242`) is
   unchanged.
2. **Flip.** Write `$S/.marvel/config.yaml` with the section 6.1 entry on
   scratch ports, `store_dir: $S/p0`, and `hub.url` at the scratch hub. Stop
   the scratch phase-0 broker. Copy the store cold. Start the scratch daemon
   (`HOME="$S"`, `--socket "$S/m.sock"`, `--state-bolt`, `--log-file`,
   `--pidfile "$S/marvel.pid"`, all under `$S`,
   `MARVEL_TMUX_SOCKET=rehearse-a3`); its managed broker listens on 14222
   (monitor 18222). Before the first write, `m get teams` lists none, which
   proves `m` reached the scratch daemon. Then `m credential put bus/leaf
   --value-file <scratch seed file>`. Pass: `m bus status` reads ready,
   provisioned, authorized, leaf up; the stream message counts match step 1;
   an anonymous `nats -s nats://127.0.0.1:14222 sub '>'` is refused. **Measure** the gap from phase-0 stop to ready.
3. **Spawn with a credential.** `m apply -f $S/team.yaml`, a one-role scratch
   team whose runtime is a shell, not a harness. Pass: the session's env
   carries `NATS_URL` at `nats://127.0.0.1:14222` and a team user, and that
   user can subscribe its inbox (`nats -s nats://127.0.0.1:14222 --user ...`). **Measure** one respawn.
4. **The seat, through the wrapper it will use.** With `DIRECTOR_NATS_*`
   unset, run `director-mcp-seat` with `MARVEL_BUS_STATE=$S/.marvel/state/nats`
   (named explicitly, not derived from `HOME`), `NATS_URL` at the scratch
   broker, and `DIRECTOR_TEAM`/`DIRECTOR_WORKSPACE` set. Pass: it connects as
   `director` and `inbox_summary` answers. Negative control: the same with
   `MARVEL_BUS_STATE` at an empty scratch directory exits 1 with "no seat
   password". So a pass cannot come from a credential the test did not
   intend.
5. **The other leaf held.** Pass: the scratch mokuzai leaf never left the
   scratch hub's `/leafz` (`127.0.0.1:18242`) during steps 2 to 4.
6. **Credentials against an anonymous broker.** Point the bare shim with
   `DIRECTOR_NATS_USER`/`DIRECTOR_NATS_PASS` set, and the step-3 session, at an
   anonymous scratch broker started for this step on 34222 (monitor 38222),
   with the same conf checks, and `NATS_URL=nats://127.0.0.1:34222`. Record whether each connects. This decides
   rollback step 7.
7. **Roll back** as section 7 does, with every path, port and socket replaced
   by its scratch value: `m stop`, then stop the scratch managed broker from
   `$S/.marvel/run/nats-server.pid` if it is still up, by the same pid check
   as rollback step 1 (conf path `$S/.marvel/state/nats/nats-server.conf`,
   the scratch listener port), restore `$S/.marvel/config.yaml`, and start the scratch
   phase-0 again with `nats-server -c $S/p0/nats-server.conf` (never
   `start.sh`). Also rehearse the dead-daemon case: read
   `dpid=$(cat "$S/marvel.pid")` (step 2's `--pidfile`), require `ps -o
   command= -p "$dpid"` to contain `$S/m.sock`, then `kill -KILL "$dpid"`;
   confirm its broker still answers on 18222, then stop the broker from its
   pidfile by the pid check. Pass: the
   anonymous scratch phase-0 serves the step-1 streams. Record whether the
   store the managed broker opened (it adds a JetStream domain) loads under
   the no-domain phase-0 conf. That decides rollback step 3. The forward
   direction was verified on 2026-09-14 (bxg5f); the reverse is not.
8. **Tear down.** Stop every scratch process; `tmux -L rehearse-a3
   kill-server`; remove `$S`.

## 4. Who loses the bus, and for how long

- **Every kinu seat (32) and the director seat**, from step 3 (the phase-0
  stop) until that seat's respawn (step 9, or step 8 for the director seat).
  In that span a seat's sends fail loudly and it receives nothing. After step
  5 an unrolled seat's shim reaches an authorized broker with no credential
  and is refused, which is also loud. Estimate, to be replaced by the
  rehearsal's numbers: a few minutes for steps 3 to 7, then the roll, which
  waits on handoffs. Budget 60 to 90 minutes for the window.
- **kinu's global reach**, over the same span: kinu seats cannot publish to
  or read the global tier. Global mail to them waits at the hub, in streams
  with a 72h max age, and is read after respawn.
- **mokuzai's seats, and corporate's if it is the third leaf, keep their own
  buses and their hub links** (section 5). They lose only their reach to kinu seats, as above.

## 5. Why the other clusters' leaf links survive

mokuzai's link, and the third leaf's (possibly corporate's), end at the hub, the 0.0.0.0:7442 leafnode listener of the
hand-started hub process. kinu's phase-0 broker holds only an outbound leaf to
that same hub. The cutover replaces the phase-0 broker and never stops,
reloads or edits the hub or `~/.director/nats-global`, so their links have
nothing to drop. Checks: the hub's `/leafz` lists both remote leaves before
step 3 and after step 7. The managed broker's own leaf authenticates as kinu's
leaf user; if the hub refuses it, only kinu's link is down.

Out of scope, stated so nobody adds it in the window: moving the hub under
marvel or launchd (aae-orc-qu88n), and any hub conf edit.

## 6. The window

Capture first, into `W=~/.marvel/window-bus-$(date +%Y%m%d)`:

0. `ps -o pid,lstart,command` for both nats-servers; the phase-0 process's
   `DIRECTOR_PHASE0_STORE` path (a path, not a secret); `cp -p
   ~/.marvel/config.yaml $W/config.yaml.pre-bus`; `curl -s
   127.0.0.1:8242/leafz` and `127.0.0.1:8222/jsz?streams=true` into `$W`;
   `marvel get sessions > $W/sessions-before`; `marvel describe team` for each
   team into `$W/teams-workdir` (precondition 6); the daemon's start command
   (`ps -o pid,lstart,command`) and its cwd (`lsof -a -p <pid> -d cwd -Fn`),
   since `ps` shows no cwd.

Then:

1. Director broadcasts to kinu seats: write your handoff now, start no new
   work. Wait for the markers or the deadline.
2. Add the bus entry to the `local` cluster (`bus-as-service.md` 6.1: class
   `message-bus`, provider `nats-server`, mode `managed`, listen and url
   127.0.0.1:4222, store_dir `~/.director/nats`, seat `{workspace: aae-orc,
   team: ops}`, hub url `nats-leaf://127.0.0.1:7442`). One spelling only: a
   `bus:` block or a Services entry, never both (`admin-guide.md` around line
   452).
3. Stop the phase-0 broker, by the pid check of rollback step 1: read
   `pid=$(cat ~/.director/nats/nats-server.pid)` once; `ps -o command= -p
   "$pid"` contains `nats-phase-0/nats-server.conf`; `lsof -nP -a -p "$pid"
   -iTCP:4222 -sTCP:LISTEN` prints a line. Only if both hold, `kill -TERM
   "$pid"`. Then confirm 4222 is closed. **If either check fails,** send
   nothing and stop the window here: the bus is still up and no seat has lost
   it. Restore `$W/config.yaml.pre-bus` over `~/.marvel/config.yaml` (step 2's
   edit), find the 4222 listener with `lsof -nP -iTCP:4222 -sTCP:LISTEN`,
   write what it shows in the window note, and reschedule. **The bus is down from here.**
4. Cold copy: `cp -Rp ~/.director/nats/store
   ~/.director/nats/store.pre-managed-$(date +%Y%m%d)`.
5. `marvel daemon reexec`. The daemon reads its config only at start (marvel#514).
   It renders, starts nats-server on 4222 with authorization, provisions
   against the inherited store, and dials the hub.
6. `marvel credential put bus/leaf --value-file <seed-file>`. A broker that
   booted without a seed is restarted once by marvel to take it
   (`admin-guide.md` around line 478); that restart is expected.
7. Verify, each a pass condition:
   - `marvel bus status` reads ready, provisioned, authorized, leaf up;
   - the hub's `/leafz` lists kinu and both remote leaves;
   - `/jsz` stream counts are at least `$W`'s (AGENT_AUDIT keeps its 720h
     history);
   - an anonymous `nats -s nats://127.0.0.1:4222 sub '>'` is refused.
8. The director seat: confirm `~/.marvel/state/nats/director.pass` exists
   (`test -r`, no read of its content), switch the operator's `director-mcp`
   registration to the prepared `director-mcp-seat` one (precondition 2), and
   reconnect it in the operator's session. Pass: `inbox_summary` answers.
9. Roll kinu sessions, supervisors first, then the rest, team by team, each
   after its handoff marker. Pass per team: a respawned seat answers a
   roll-call message; a global publish from a kinu supervisor lands in
   `GLOBAL_TO_mokuzai`; one respawned seat's `#{pane_start_path}` is the
   team's `WorkDir` from `$W` (precondition 6). A seat that starts somewhere
   else stops the roll for that team until the operator decides.
   **Before the next team, capture every respawned pane** (`marvel capture
   <session>`) and read it. A seat should be at its harness prompt, or working.
   A seat respawned in auto mode can instead stop on a Claude Code onboarding
   dialog with an option already selected. On corporate this was the
   shell-history scan (aae-orc#461 S12-9). If any pane shows such a dialog,
   stop that team's roll, and stop the teams after it. The dialog is **never
   answered automatically**, by marvel, by a seat, or by an inject: the
   operator reads it and decides each one by hand. That is a choice about what
   the harness may read on the operator's machine, not a step in this
   cutover.
10. Watch 30 minutes. Keep `store.pre-managed-*` and `$W` until the operator
   deletes them.

After the window: every daemon stop, start or reexec now drops the leaf seed
until it is pushed again (#339), so the seed push joins kinu's restart steps.

## 7. Rollback (on a failed check at step 7 not fixed in 15 minutes, a failed step 9, or the operator's word)

1. Stop the managed broker and the daemon.
   - **Daemon alive:** `marvel stop`, without `--keep-bus`, stops the broker
     with the daemon. Panes survive.
   - **Daemon dead, broker alive:** the managed broker outlives its daemon by
     design, so the next daemon can adopt it (`internal/bus/supervisor.go:198-209`).
     Stop it from its pidfile (`supervisor.go:179`), with the match checked,
     not eyed:
     ```sh
     pid=$(cat ~/.marvel/run/nats-server.pid)          # read once
     ps -o command= -p "$pid" | grep -F "$HOME/.marvel/state/nats/nats-server.conf"
     lsof -nP -a -p "$pid" -iTCP:4222 -sTCP:LISTEN
     ```
     The conf path is the one the supervisor starts it with (`-c conf`,
     `supervisor.go:274`; the name, `render.go:24`). Only if **both** commands
     print a line, `kill -TERM "$pid"`. If either prints nothing, the pidfile
     is stale or names another process: send nothing, and find the 4222
     listener with `lsof` before going on.

   Either way, confirm 4222 is closed before step 4.
2. `cp -p $W/config.yaml.pre-bus ~/.marvel/config.yaml`.
3. The store. If rehearsal step 7 showed the domain-stamped store loads under
   the phase-0 conf, keep it, and nothing is lost. Otherwise run `mv
   ~/.director/nats/store ~/.director/nats/store.failed-managed-$(date
   +%Y%m%d)` and then `cp -Rp ~/.director/nats/store.pre-managed-*
   ~/.director/nats/store`. Mail sent during the window is then lost; say so
   to the senders.
4. `~/work/aae-orc/director/probe/nats-phase-0/start.sh`, backgrounded as
   before. It is the anonymous base only, by design (its header).
5. Pass: the hub's `/leafz` lists kinu's phase-0 leaf again, through
   `leaf-kinu.conf`, and the two remote leaves.
6. Start the daemon with the recorded command, from the recorded cwd. It
   adopts the panes.
7. Seats. Unrolled seats reconnect by themselves. Respawned seats carry a
   team user to an anonymous broker. If rehearsal step 6 showed they connect,
   nothing more is needed. If not, roll them once more. The director seat goes
   back to its bare `director-mcp` registration, since the wrapper exits 1
   once no daemon renders `director.pass` (or, if the file is left behind, it
   connects with a credential the anonymous broker ignores, per rehearsal step
   6).
8. Leave the `bus/leaf` credential unpushed. The phase-0 broker reads its own
   `leaf-kinu.conf`.

Downgrade caution for later: once the managed broker renders a per-role
(dotted) user, a daemon downgrade below that change breaks those roles.
Revert the render first (marvel `CLAUDE.md`, global roles).
