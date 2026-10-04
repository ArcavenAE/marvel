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
  faed978, and the live kinu processes on 2026-10-04 at about 03:45Z
  (read-only).

## 1. What runs today

| Piece | Live state | Touched by this cutover |
|---|---|---|
| Phase-0 broker | `nats-server -c director/probe/nats-phase-0/nats-server.conf`, started by `start.sh` on 2026-09-26; 127.0.0.1:4222, monitor 8222, anonymous, store `~/.director/nats/store`, one leaf out to the hub (`include "leaf-kinu.conf"`) | **replaced** |
| Global hub | `nats-server --config ~/.director/nats-global/nats-server.conf`, started 2026-09-25; 0.0.0.0:4242, monitor 127.0.0.1:8242, leafnodes 0.0.0.0:7442, domain `global`. Three leaves: kinu's phase-0, mokuzai, and a third that is probably corporate (inferred from its address) | **not touched**: no stop, reload or edit |
| marvel daemon | b533ca5 today; the release from the store-migration window by the time this runs. Its cwd is `~/work/aae-orc`; `~/.marvel/config.yaml` has no bus entry; `marvel bus status` says none is configured; `marvel credential list` is empty | gains a bus entry; restarted |
| kinu sessions | 32, across 6 teams (arcaven 13) | every one respawned |
| The operator's director session | its `director-mcp` entry in `~/.claude.json` has no `DIRECTOR_NATS_USER` or `DIRECTOR_NATS_PASS_FILE` key | gains both |

## 2. Preconditions (all before the window is set)

1. **Daemon build.** It includes aae-orc-wzexa (959bf90), aae-orc-vy6k7
   (18ea860) and #279 (b798131). b533ca5 already does; the store-migration
   window comes first anyway.
2. **The director seat credential (NOT MET today).** The operator sets
   `DIRECTOR_NATS_USER=director` and `DIRECTOR_NATS_PASS_FILE=<StateDir>/nats/director.pass`
   in the `director-mcp` entry of the aae-orc project in `~/.claude.json`, by
   hand at a terminal. Without it the flip turns authorization on and locks
   the human out of the bus (`bus-as-service.md` 6.2). Whether the shim can
   carry these keys against today's anonymous broker before the window is
   rehearsal step 6; if it cannot, they are set in the window, at step 8.
3. **The leaf seed.** The operator has the kinu leaf NKey seed as a file for
   `credential put bus/leaf --value-file`, and has checked, without printing
   the seed, that its public key is the kinu leaf user in the hub's conf
   (line 35). A mismatch leaves kinu with a working local bus and no hub link
   (`bus.leaf.unenrolled`): survivable, and recovered in the window.
4. **Rehearsal passed** (section 3), its transcript linked from #534, with the
   broker gap and the per-seat respawn time measured.
5. **Quiet fleet.** No merge, shift or apply in flight. mokuzai's and
   corporate's supervisors are told kinu seats will be unreachable for the
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
# scratch ports: phase-0 14222 (monitor 18222), hub 14242 / leaf 17442,
# a second scratch leaf standing in for mokuzai on 24222
```

The daemon reads `~/.marvel/config.yaml` through `os.UserHomeDir`
(`internal/config/config.go:742-748`), so `HOME="$S"` gives the scratch daemon
its own config; the client must still name `--socket` (marvel `CLAUDE.md`).

**Isolation checks, before each scratch process starts:**

- `env | grep '^MARVEL_'` prints nothing in the shell that starts it.
- Its port is free: `lsof -nP -iTCP:<port> -sTCP:LISTEN` prints nothing for
  14222, 18222, 14242, 17442 and 24222. This is the guard against a scratch
  broker binding a live port.
- A scratch nats-server's `-c` path and store are under `$S`, never
  `~/.director`. Read back with `ps -o command= -p <pid>` after it starts.
- The scratch daemon's socket is `$S/m.sock`, and `MARVEL_TMUX_SOCKET` is
  `rehearse-a3`. `tmux -L rehearse-a3 ls` lists only the scratch server. Its
  first call is a read-only `bus status` with no "is rooted at" warning.
  It cannot take the live socket, since it takes the socket lock first
  (`daemon.go:447-457`).
- Every client command names its server: `nats -s nats://127.0.0.1:14222` (or
  the scratch hub), never a default context. The shim gets an explicit
  `NATS_URL` to a scratch port.
- After each step, the live checks are unchanged: the phase-0 broker on 4222 and the hub's
  `/leafz` show the same processes and leaves as at the start.

Steps, each with a pass condition:

1. **Build today's shape.** Copy `nats-server.conf` to `$S` with only its
   ports and its leaf include changed. Start it through `start.sh` with
   `DIRECTOR_PHASE0_HOME=$S/p0`. Provision the streams as `verify-auth.sh` does,
   and publish a few messages to AGENT_INBOX and AGENT_AUDIT. Start a scratch
   hub (domain `global`, scratch kinu-leaf and mokuzai-leaf NKeys) and the
   scratch mokuzai leaf. Pass: the hub's `/leafz` lists both leaves.
2. **Flip.** Write `$S/.marvel/config.yaml` with the section 6.1 entry on
   scratch ports, `store_dir: $S/p0`, and `hub.url` at the scratch hub. Stop
   the scratch phase-0 broker. Copy the store cold. Start the scratch daemon
   (`--socket`, `--state-bolt`, `--log-file`, `--pidfile`,
   `MARVEL_TMUX_SOCKET=rehearse-a3`), then `credential put bus/leaf` with the
   scratch seed. Pass: `bus status` reads ready, provisioned, authorized,
   leaf up; the stream message counts match step 1; an anonymous `sub '>'` is
   refused. **Measure** the gap from phase-0 stop to ready.
3. **Spawn with a credential.** Apply a one-role scratch team whose runtime is
   a shell, not a harness. Pass: the session's env carries `NATS_URL` and a
   team user, and that user can subscribe its inbox. **Measure** one respawn.
4. **The seat.** Run the director shim with `DIRECTOR_NATS_USER=director` and
   `DIRECTOR_NATS_PASS_FILE=$S/.marvel/state/nats/director.pass` (the rendered
   path under the scratch StateDir). Pass: it connects and `inbox_summary`
   answers.
5. **The other leaf held.** Pass: the scratch mokuzai leaf never left the
   hub's `/leafz` during steps 2 to 4.
6. **Credentials against an anonymous broker.** Point the shim, with its
   `DIRECTOR_NATS_*` set, and the step-3 session at an anonymous scratch
   broker. Record whether each connects. This decides precondition 2's timing
   and rollback step 7.
7. **Roll back** exactly as section 6, on the scratch processes. Pass: the
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
- **mokuzai and corporate seats keep their own buses and their hub links**
  (section 5). They lose only their reach to kinu seats, as above.

## 5. Why mokuzai's and corporate's leaf links survive

Their links end at the hub, the 0.0.0.0:7442 leafnode listener of the
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
   team into `$W/teams-workdir` (precondition 6); the daemon's start command.

Then:

1. Director broadcasts to kinu seats: write your handoff now, start no new
   work. Wait for the markers or the deadline.
2. Add the bus entry to the `local` cluster (`bus-as-service.md` 6.1: class
   `message-bus`, provider `nats-server`, mode `managed`, listen and url
   127.0.0.1:4222, store_dir `~/.director/nats`, seat `{workspace: aae-orc,
   team: ops}`, hub url `nats-leaf://127.0.0.1:7442`). One spelling only: a
   `bus:` block or a Services entry, never both (`admin-guide.md` around line
   452).
3. Stop the phase-0 broker: `kill -TERM $(cat ~/.director/nats/nats-server.pid)`,
   then confirm 4222 is closed. **The bus is down from here.**
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
8. The director seat: reconnect `director-mcp` in the operator's session (set
   precondition 2's keys first if rehearsal step 6 said to). Pass:
   `inbox_summary` answers.
9. Roll kinu sessions, supervisors first, then the rest, team by team, each
   after its handoff marker. Pass per team: a respawned seat answers a
   roll-call message; a global publish from a kinu supervisor lands in
   `GLOBAL_TO_mokuzai`; one respawned seat's `#{pane_start_path}` is the
   team's `WorkDir` from `$W` (precondition 6). A seat that starts somewhere
   else stops the roll for that team until the operator decides.
10. Watch 30 minutes. Keep `store.pre-managed-*` and `$W` until the operator
   deletes them.

After the window: every daemon stop, start or reexec now drops the leaf seed
until it is pushed again (#339), so the seed push joins kinu's restart steps.

## 7. Rollback (on a failed check at step 7 not fixed in 15 minutes, a failed step 9, or the operator's word)

1. `marvel stop`, without `--keep-bus`, so the managed broker stops with the
   daemon. Panes survive. Confirm 4222 is closed.
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
6. Start the daemon with the recorded command. It adopts the panes.
7. Seats. Unrolled seats reconnect by themselves. Respawned seats carry a
   team user to an anonymous broker. If rehearsal step 6 showed they connect,
   nothing more is needed. If not, roll them once more. The director seat
   keeps or drops its `DIRECTOR_NATS_*` keys the same way.
8. Leave the `bus/leaf` credential unpushed. The phase-0 broker reads its own
   `leaf-kinu.conf`.

Downgrade caution for later: once the managed broker renders a per-role
(dotted) user, a daemon downgrade below that change breaks those roles.
Revert the render first (marvel `CLAUDE.md`, global roles).
